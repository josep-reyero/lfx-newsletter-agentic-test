// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

// Package repository contains the bun-backed implementation of the
// NewsletterRepository port.
package repository

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/uptrace/bun"
	"github.com/uptrace/bun/dialect/pgdialect"

	"github.com/linuxfoundation/lfx-v2-newsletter-service/internal/domain"
	"github.com/linuxfoundation/lfx-v2-newsletter-service/internal/domain/model"
	"github.com/linuxfoundation/lfx-v2-newsletter-service/internal/domain/port"
)

// defaultListLimit is the page size used when ListAll callers don't specify one.
// maxListLimit caps the page size to keep one round-trip bounded.
const (
	defaultListLimit = 20
	maxListLimit     = 100
)

// PostgresNewsletterRepo persists Newsletter aggregates in PostgreSQL via bun.
type PostgresNewsletterRepo struct {
	db *bun.DB
}

// NewPostgresNewsletterRepo wires a Newsletter repository over the given bun.DB.
func NewPostgresNewsletterRepo(db *bun.DB) *PostgresNewsletterRepo {
	return &PostgresNewsletterRepo{db: db}
}

// Create inserts a new Newsletter row. Database defaults populate id, version,
// timestamps; bun copies the generated values back into n.
func (r *PostgresNewsletterRepo) Create(ctx context.Context, n *model.Newsletter) error {
	if _, err := r.db.NewInsert().
		Model(n).
		Returning("*").
		Exec(ctx); err != nil {
		return fmt.Errorf("insert newsletter: %w", err)
	}
	return nil
}

// Get fetches a single newsletter by primary key.
func (r *PostgresNewsletterRepo) Get(ctx context.Context, id uuid.UUID) (*model.Newsletter, error) {
	n := &model.Newsletter{}
	err := r.db.NewSelect().
		Model(n).
		Where("n.id = ?", id).
		Scan(ctx)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, domain.ErrNotFound
		}
		return nil, fmt.Errorf("select newsletter: %w", err)
	}
	return n, nil
}

// List returns all newsletters in the given project, newest first.
func (r *PostgresNewsletterRepo) List(ctx context.Context, projectUID string) ([]*model.Newsletter, error) {
	var rows []*model.Newsletter
	err := r.db.NewSelect().
		Model(&rows).
		Where("project_uid = ?", projectUID).
		Order("created_at DESC").
		Scan(ctx)
	if err != nil {
		return nil, fmt.Errorf("list newsletters: %w", err)
	}
	return rows, nil
}

// ListAll returns a page of newsletters matching the given filters, ordered by
// updated_at DESC (so the most recently touched draft / most recently sent
// newsletter appears first). The cursor encodes (updated_at, id) so the next
// page resumes deterministically even when many rows share an updated_at.
func (r *PostgresNewsletterRepo) ListAll(ctx context.Context, filters port.ListFilters) (*port.ListPage, error) {
	limit := filters.Limit
	if limit <= 0 {
		limit = defaultListLimit
	}
	if limit > maxListLimit {
		limit = maxListLimit
	}

	q := r.db.NewSelect().
		Model((*model.Newsletter)(nil)).
		Where("project_uid = ?", filters.ProjectUID).
		Order("updated_at DESC").
		Order("id DESC").
		Limit(limit + 1)

	if filters.Status != "" {
		q = q.Where("status = ?", filters.Status)
	}

	if filters.PageToken != "" {
		cursor, err := decodeCursor(filters.PageToken)
		if err != nil {
			return nil, fmt.Errorf("%w: invalid pageToken", domain.ErrInvalidRequest)
		}
		// Keyset pagination: continue from the (updated_at, id) tuple of the last
		// row of the previous page. Tuple comparison handles ties on updated_at.
		q = q.Where("(updated_at, id) < (?, ?)", cursor.UpdatedAt, cursor.ID)
	}

	var rows []*model.Newsletter
	if err := q.Scan(ctx, &rows); err != nil {
		return nil, fmt.Errorf("list all newsletters: %w", err)
	}

	page := &port.ListPage{Newsletters: rows}
	if len(rows) > limit {
		// Trim the lookahead row and emit its cursor as the next page token.
		last := rows[limit-1]
		page.Newsletters = rows[:limit]
		page.NextPageToken = encodeCursor(listCursor{UpdatedAt: last.UpdatedAt, ID: last.ID})
	}
	return page, nil
}

// Update applies optimistic-locking-aware mutations. The query gates on
// (id, expectedVersion) and atomically increments version. If no rows are
// affected, the method follows up with an existence check to disambiguate
// ErrNotFound vs ErrVersionMismatch.
func (r *PostgresNewsletterRepo) Update(ctx context.Context, n *model.Newsletter, expectedVersion int64) (*model.Newsletter, error) {
	res, err := r.db.NewUpdate().
		Model(n).
		Set("subject = ?", n.Subject).
		Set("body_html = ?", n.BodyHTML).
		Set("ed_reply_email = ?", n.EDReplyEmail).
		// pgdialect.Array forces a Postgres text[] literal; without it bun
		// json-encodes the slice and PG raises a "malformed array literal".
		Set("committee_uids = ?", pgdialect.Array(n.CommitteeUIDs)).
		Set("project_uid = ?", n.ProjectUID).
		Set("updated_at = now()").
		Set("version = version + 1").
		Where("id = ? AND version = ?", n.ID, expectedVersion).
		Returning("*").
		Exec(ctx)
	if err != nil {
		return nil, fmt.Errorf("update newsletter: %w", err)
	}

	rowsAffected, err := res.RowsAffected()
	if err != nil {
		return nil, fmt.Errorf("update newsletter rows affected: %w", err)
	}
	if rowsAffected == 0 {
		return nil, r.classifyMissing(ctx, n.ID)
	}

	// bun's Returning("*") populated n with the new row state.
	return n, nil
}

// Delete removes a newsletter by id. Returns ErrNotFound if no row was deleted.
func (r *PostgresNewsletterRepo) Delete(ctx context.Context, id uuid.UUID) error {
	res, err := r.db.NewDelete().
		Model((*model.Newsletter)(nil)).
		Where("id = ?", id).
		Exec(ctx)
	if err != nil {
		return fmt.Errorf("delete newsletter: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("delete newsletter rows affected: %w", err)
	}
	if n == 0 {
		return domain.ErrNotFound
	}
	return nil
}

// PersistSendIntent atomically CLAIMS a draft for sending: it durably records
// group_id and bumps the version in a single conditional update before any email
// goes out. This makes the send both durable and idempotent under concurrency:
//
//   - durable: group_id is persisted before fan-out, so a crash or fully-failed
//     attempt leaves the correlation key behind to retry under.
//   - idempotent: bumping the version claims the row. A second concurrent send
//     still holding the pre-claim version fails the optimistic lock here (or at
//     MarkSent) instead of fanning out a duplicate batch under a fresh group.
//
// The row stays in status=draft (so it remains retryable until MarkSent
// finalizes it), but its version advances. The method returns the durable
// group_id and the post-claim version the caller must pass to MarkSent.
//
// Reuse vs. concurrency: a group_id is only reused when the caller's
// expectedVersion still matches the row's CURRENT version — i.e. the *same*
// caller retrying its own claimed-but-unfinished send. A different caller that
// lost the claim race holds a stale version and gets ErrVersionMismatch, so it
// never enters fan-out under the winner's group.
func (r *PostgresNewsletterRepo) PersistSendIntent(ctx context.Context, id uuid.UUID, groupID string, expectedVersion int64) (string, int64, error) {
	existing := &model.Newsletter{}
	if err := r.db.NewSelect().
		Model(existing).
		Where("id = ?", id).
		Scan(ctx); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", 0, domain.ErrNotFound
		}
		return "", 0, fmt.Errorf("persist send intent: load: %w", err)
	}
	if existing.Status == model.StatusSent {
		return "", 0, domain.ErrAlreadySent
	}
	if existing.Version != expectedVersion {
		return "", 0, domain.ErrVersionMismatch
	}
	// Prior unfinished attempt: reuse its group and current version (the claim
	// already advanced the version on that attempt).
	if existing.GroupID != nil && *existing.GroupID != "" {
		return *existing.GroupID, existing.Version, nil
	}
	// Claim the send: set group_id and bump version, guarded on the version and a
	// still-null group_id so a concurrent claimant loses the race deterministically.
	claimed := &model.Newsletter{}
	res, err := r.db.NewUpdate().
		Model(claimed).
		Set("group_id = ?", groupID).
		Set("version = version + 1").
		Set("updated_at = now()").
		Where("id = ? AND version = ? AND status = ? AND group_id IS NULL", id, expectedVersion, model.StatusDraft).
		Returning("*").
		Exec(ctx)
	if err != nil {
		return "", 0, fmt.Errorf("persist send intent: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		// Lost the claim race: another caller claimed the row between our load and
		// update (it bumped the version and/or set group_id). This caller is a
		// loser and MUST NOT proceed to fan-out — returning the winner's group_id
		// would let both callers dispatch the full batch. Re-read only to classify
		// the failure precisely; never return a reusable group here.
		if err := r.db.NewSelect().Model(existing).Where("id = ?", id).Scan(ctx); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return "", 0, domain.ErrNotFound
			}
			return "", 0, fmt.Errorf("persist send intent: reload: %w", err)
		}
		if existing.Status == model.StatusSent {
			return "", 0, domain.ErrAlreadySent
		}
		// The row was claimed concurrently (version advanced) — fail the optimistic
		// lock so only the winner sends.
		return "", 0, domain.ErrVersionMismatch
	}
	return groupID, claimed.Version, nil
}

// MarkSent transitions a draft to status=sent atomically, gated on the expected
// version. Captures the audience size and the lfx-v2-email-service group_id
// at send time so analytics can compute open rates without re-resolving
// committee membership and can locate the per-recipient engagement records.
func (r *PostgresNewsletterRepo) MarkSent(ctx context.Context, id uuid.UUID, sentAt time.Time, totalRecipients int, groupID string, expectedVersion int64) (*model.Newsletter, error) {
	updated := &model.Newsletter{}
	res, err := r.db.NewUpdate().
		Model(updated).
		Set("status = ?", model.StatusSent).
		Set("sent_at = ?", sentAt).
		Set("total_recipients = ?", totalRecipients).
		Set("group_id = ?", groupID).
		Set("updated_at = now()").
		Set("version = version + 1").
		Where("id = ? AND version = ? AND status = ?", id, expectedVersion, model.StatusDraft).
		Returning("*").
		Exec(ctx)
	if err != nil {
		return nil, fmt.Errorf("mark sent: %w", err)
	}
	rowsAffected, err := res.RowsAffected()
	if err != nil {
		return nil, fmt.Errorf("mark sent rows affected: %w", err)
	}
	if rowsAffected == 0 {
		return nil, r.classifyMarkSentMiss(ctx, id, expectedVersion)
	}
	return updated, nil
}

// RecordOpen inserts a single open event. Repeat hits from the same recipient
// within the same hour collapse to a no-op via the
// uq_opens_newsletter_recipient_hour unique index — this bounds growth on the
// unauthenticated tracking pixel without losing unique-open counts.
func (r *PostgresNewsletterRepo) RecordOpen(ctx context.Context, newsletterID uuid.UUID, recipientHash string) error {
	open := &model.NewsletterOpen{
		NewsletterID:  newsletterID,
		RecipientHash: recipientHash,
	}
	// uq_opens_newsletter_recipient_hour is a unique INDEX (CREATE UNIQUE INDEX),
	// not a named table constraint, so ON CONFLICT must infer it from the index
	// columns — `ON CONFLICT ON CONSTRAINT <name>` only matches real constraints
	// and would error at runtime.
	if _, err := r.db.NewInsert().
		Model(open).
		On("CONFLICT (newsletter_id, recipient_hash, opened_at_hour) DO NOTHING").
		Exec(ctx); err != nil {
		return fmt.Errorf("record open: %w", err)
	}
	return nil
}

// Analytics aggregates the engagement metrics for a single newsletter. Returns
// ErrNotFound if the newsletter doesn't exist; returns zero counts (not an
// error) if it exists but has no opens recorded yet.
func (r *PostgresNewsletterRepo) Analytics(ctx context.Context, newsletterID uuid.UUID) (*model.Analytics, error) {
	n, err := r.Get(ctx, newsletterID)
	if err != nil {
		return nil, err
	}

	type aggRow struct {
		TotalOpens  int        `bun:"total_opens"`
		UniqueOpens int        `bun:"unique_opens"`
		LastEventAt *time.Time `bun:"last_event_at"`
	}
	agg := &aggRow{}
	err = r.db.NewSelect().
		ColumnExpr("COUNT(*) AS total_opens").
		ColumnExpr("COUNT(DISTINCT recipient_hash) AS unique_opens").
		ColumnExpr("MAX(opened_at) AS last_event_at").
		Table("newsletter_opens").
		Where("newsletter_id = ?", newsletterID).
		Scan(ctx, agg)
	if err != nil {
		return nil, fmt.Errorf("aggregate opens: %w", err)
	}

	type dailyRow struct {
		Day         time.Time `bun:"day"`
		Opens       int       `bun:"opens"`
		UniqueOpens int       `bun:"unique_opens"`
	}
	var dailyRows []dailyRow
	err = r.db.NewSelect().
		ColumnExpr("date_trunc('day', opened_at) AS day").
		ColumnExpr("COUNT(*) AS opens").
		ColumnExpr("COUNT(DISTINCT recipient_hash) AS unique_opens").
		Table("newsletter_opens").
		Where("newsletter_id = ?", newsletterID).
		GroupExpr("date_trunc('day', opened_at)").
		OrderExpr("date_trunc('day', opened_at) ASC").
		Scan(ctx, &dailyRows)
	if err != nil {
		return nil, fmt.Errorf("aggregate daily opens: %w", err)
	}

	daily := make([]model.DailyOpens, 0, len(dailyRows))
	for _, d := range dailyRows {
		daily = append(daily, model.DailyOpens{Date: d.Day, Opens: d.Opens, UniqueOpens: d.UniqueOpens})
	}

	delivered := n.TotalRecipients
	openRate := 0.0
	if delivered > 0 {
		openRate = float64(agg.UniqueOpens) / float64(delivered)
	}

	return &model.Analytics{
		NewsletterID:    n.ID,
		Subject:         n.Subject,
		Status:          n.Status,
		SentAt:          n.SentAt,
		TotalRecipients: n.TotalRecipients,
		Delivered:       delivered,
		Failed:          0,
		TotalOpens:      agg.TotalOpens,
		UniqueOpens:     agg.UniqueOpens,
		OpenRate:        openRate,
		DailyOpens:      daily,
		LastEventAt:     agg.LastEventAt,
	}, nil
}

// classifyMissing distinguishes ErrNotFound from ErrVersionMismatch after an
// Update affected zero rows.
func (r *PostgresNewsletterRepo) classifyMissing(ctx context.Context, id uuid.UUID) error {
	exists, err := r.db.NewSelect().
		Model((*model.Newsletter)(nil)).
		Where("id = ?", id).
		Exists(ctx)
	if err != nil {
		return fmt.Errorf("classify update miss: %w", err)
	}
	if !exists {
		return domain.ErrNotFound
	}
	return domain.ErrVersionMismatch
}

// classifyMarkSentMiss distinguishes the three reasons a MarkSent update can
// affect zero rows: not found, wrong version, or already sent.
func (r *PostgresNewsletterRepo) classifyMarkSentMiss(ctx context.Context, id uuid.UUID, expectedVersion int64) error {
	existing := &model.Newsletter{}
	err := r.db.NewSelect().
		Model(existing).
		Where("id = ?", id).
		Scan(ctx)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return domain.ErrNotFound
		}
		return fmt.Errorf("classify mark sent miss: %w", err)
	}
	if existing.Status == model.StatusSent {
		return domain.ErrAlreadySent
	}
	if existing.Version != expectedVersion {
		return domain.ErrVersionMismatch
	}
	// Unreachable in practice — fall back to version mismatch.
	return domain.ErrVersionMismatch
}

// listCursor is the keyset cursor encoded into NextPageToken for ListAll.
type listCursor struct {
	UpdatedAt time.Time `json:"u"`
	ID        uuid.UUID `json:"i"`
}

func encodeCursor(c listCursor) string {
	raw, err := json.Marshal(c)
	if err != nil {
		return ""
	}
	return base64.RawURLEncoding.EncodeToString(raw)
}

func decodeCursor(token string) (listCursor, error) {
	raw, err := base64.RawURLEncoding.DecodeString(strings.TrimSpace(token))
	if err != nil {
		return listCursor{}, err
	}
	var c listCursor
	if err := json.Unmarshal(raw, &c); err != nil {
		return listCursor{}, err
	}
	return c, nil
}
