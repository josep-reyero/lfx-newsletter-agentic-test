// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package service

import (
	"context"
	"log/slog"
	"sort"
	"time"

	"github.com/google/uuid"

	"github.com/linuxfoundation/lfx-v2-newsletter-service/internal/domain"
	"github.com/linuxfoundation/lfx-v2-newsletter-service/internal/domain/model"
	"github.com/linuxfoundation/lfx-v2-newsletter-service/internal/domain/port"
)

// dayBucketLayout is the calendar-day key used to group per-recipient opens
// into DailyOpens buckets. UTC keeps the buckets stable regardless of the
// caller's timezone.
const dayBucketLayout = "2006-01-02"

// AnalyticsService aggregates engagement metrics for a sent newsletter,
// combining email-service totals (delivered / failed) with locally-tracked
// open events from the newsletter_opens table.
type AnalyticsService struct {
	repo  port.NewsletterRepository
	email port.EmailDispatcher
}

// NewAnalyticsService wires an AnalyticsService.
func NewAnalyticsService(repo port.NewsletterRepository, email port.EmailDispatcher) *AnalyticsService {
	return &AnalyticsService{repo: repo, email: email}
}

// Get returns aggregated analytics for the given newsletter, gated on project
// ownership. Returns ErrNotFound if the newsletter belongs to a different
// project than the one supplied, so callers can't probe across projects.
//
// Email-service totals are best-effort: if the engagement call fails we still
// return the locally-tracked analytics with the email-service-derived fields
// zeroed. Newsletter is the source of truth for total_recipients (snapshot
// taken at send time).
func (a *AnalyticsService) Get(ctx context.Context, projectUID string, newsletterID uuid.UUID) (*model.Analytics, error) {
	if err := validateProjectUID(projectUID); err != nil {
		return nil, err
	}
	n, err := a.repo.Get(ctx, newsletterID)
	if err != nil {
		return nil, err
	}
	if n.ProjectUID != projectUID {
		return nil, domain.ErrNotFound
	}

	local, err := a.repo.Analytics(ctx, newsletterID)
	if err != nil {
		return nil, err
	}

	// For drafts, skip the email-service call — there's nothing to aggregate.
	if n.Status != model.StatusSent || n.GroupID == nil || *n.GroupID == "" {
		return local, nil
	}

	engagement, engErr := a.email.GetEngagement(ctx, *n.GroupID)
	if engErr != nil {
		slog.WarnContext(ctx, "analytics: email-service engagement fetch failed, returning local-only",
			"newsletter_id", n.ID,
			"group_id", *n.GroupID,
			"error", engErr.Error(),
		)
		return local, nil
	}

	// Email-service-derived fields overlay the local analytics.
	if engagement.TotalSent > 0 {
		local.TotalRecipients = engagement.TotalSent
		local.Delivered = engagement.Delivered
	}
	local.Failed = engagement.Failed
	if engagement.Opened > local.TotalOpens {
		// If email-service is tracking more raw opens than our local pixel
		// observed, surface the bigger number. This is provisional: when the
		// per-recipient series below is available it overrides TotalOpens so
		// it stays reconciled with the DailyOpens raw-open series.
		local.TotalOpens = engagement.Opened
	}

	// The engagement summary is scalar-only. Fetch the per-recipient records
	// to derive TotalOpens, UniqueOpens and the DailyOpens time series. We
	// replace (not merge) the local-table values because the local tracking
	// pixel is not embedded in outgoing emails today, so newsletter_opens is
	// effectively always empty — email-service is the authoritative source.
	records, recErr := a.email.GetStatusByGroupID(ctx, *n.GroupID)
	if recErr != nil {
		slog.WarnContext(ctx, "analytics: email-service group status fetch failed, keeping engagement-only rollup",
			"newsletter_id", n.ID,
			"group_id", *n.GroupID,
			"error", recErr.Error(),
		)
	} else if len(records) > 0 {
		totalOpens, uniqueOpens, daily, lastEvent := aggregatePerRecipient(records)
		// Source TotalOpens from the same per-event series that feeds
		// DailyOpens so the public contract holds: TotalOpens == sum of
		// DailyOpens.Opens, and UniqueOpens <= TotalOpens. This supersedes the
		// scalar engagement.Opened above, which is an independent rollup that
		// can drift from the per-recipient series.
		local.TotalOpens = totalOpens
		local.UniqueOpens = uniqueOpens
		local.DailyOpens = daily
		if lastEvent != nil && (local.LastEventAt == nil || lastEvent.After(*local.LastEventAt)) {
			local.LastEventAt = lastEvent
		}
	}

	denominator := local.TotalRecipients
	if denominator == 0 {
		denominator = n.TotalRecipients
	}
	if denominator > 0 {
		local.OpenRate = float64(local.UniqueOpens) / float64(denominator)
	}
	return local, nil
}

// aggregatePerRecipient buckets per-recipient open events into a sorted
// DailyOpens series and counts both total raw opens and unique opens (one per
// recipient that ever opened). Per-day Opens count every event; per-day
// UniqueOpens counts distinct recipients with at least one event on that day.
//
// When email-service exposes opened_at_list, each entry becomes its own
// counted event so repeat opens by the same recipient show up in Opens. With
// older email-service builds that only emit a flat opened_at, the dispatcher
// fills OpenedAtList with a single element so the same code path applies.
//
// The returned total open count equals the sum of every bucket's Opens, so
// callers can set Analytics.TotalOpens from the same series that feeds
// DailyOpens and keep the two reconciled (TotalOpens == sum of DailyOpens.Opens,
// and UniqueOpens <= TotalOpens).
//
// Returns: total raw opens, unique opens, the daily series, and the last
// observed open timestamp so callers can advance LastEventAt.
func aggregatePerRecipient(records []port.EmailRecipientRecord) (int, int, []model.DailyOpens, *time.Time) {
	type bucket struct {
		date             time.Time
		opens            int
		uniqueRecipients map[string]struct{}
	}
	buckets := map[string]*bucket{}
	unique := 0
	total := 0
	var lastEvent *time.Time
	for _, r := range records {
		events := r.OpenedAtList
		if len(events) == 0 && r.Opened && r.LastOpened != nil {
			// Defensive fallback if the dispatcher hands us a record built
			// from a wire shape that lacked both opened_at_list and
			// opened_at but still set Opened=true with a LastOpened.
			events = []time.Time{*r.LastOpened}
		}
		if len(events) == 0 {
			continue
		}
		unique++
		recipientKey := r.EmailID
		if recipientKey == "" {
			recipientKey = r.To
		}
		for _, ev := range events {
			total++
			opened := ev.UTC()
			if lastEvent == nil || opened.After(*lastEvent) {
				cp := opened
				lastEvent = &cp
			}
			key := opened.Format(dayBucketLayout)
			b, ok := buckets[key]
			if !ok {
				day, _ := time.Parse(dayBucketLayout, key)
				b = &bucket{date: day, uniqueRecipients: map[string]struct{}{}}
				buckets[key] = b
			}
			b.opens++
			b.uniqueRecipients[recipientKey] = struct{}{}
		}
	}
	daily := make([]model.DailyOpens, 0, len(buckets))
	for _, b := range buckets {
		daily = append(daily, model.DailyOpens{Date: b.date, Opens: b.opens, UniqueOpens: len(b.uniqueRecipients)})
	}
	sort.Slice(daily, func(i, j int) bool { return daily[i].Date.Before(daily[j].Date) })
	return total, unique, daily, lastEvent
}
