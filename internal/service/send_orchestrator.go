// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package service

import (
	"context"
	"fmt"
	"log/slog"
	"net/mail"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"golang.org/x/sync/errgroup"

	"github.com/linuxfoundation/lfx-v2-newsletter-service/internal/domain"
	"github.com/linuxfoundation/lfx-v2-newsletter-service/internal/domain/model"
	"github.com/linuxfoundation/lfx-v2-newsletter-service/internal/domain/port"
	"github.com/linuxfoundation/lfx-v2-newsletter-service/internal/service/render"
)

// defaultSendConcurrency caps in-flight email-service requests during fan-out.
// Mirrors the lfx-v2-ui Express bridge that this orchestrator replaces (a
// worker pool of 5 also bounded that fan-out).
const defaultSendConcurrency = 5

// SendOrchestrator coordinates recipient resolution, email-chrome rendering,
// per-recipient fan-out to lfx-v2-email-service, and the draft → sent state
// transition. It owns the email-service integration; the UI no longer talks
// to email-service directly.
type SendOrchestrator struct {
	repo             port.NewsletterRepository
	committee        port.CommitteeClient
	project          port.ProjectMetadataClient
	email            port.EmailDispatcher
	concurrency      int
	fanoutEnabled    bool
	publicAPIBaseURL string
}

// SendOrchestratorConfig configures a SendOrchestrator.
type SendOrchestratorConfig struct {
	Repo        port.NewsletterRepository
	Committee   port.CommitteeClient
	Project     port.ProjectMetadataClient
	Email       port.EmailDispatcher
	Concurrency int
	// FanoutEnabled is the feature toggle for the per-recipient send loop.
	// Defaults to true; flip false in environments where we want to validate
	// the recipient-resolution path without sending real mail.
	FanoutEnabled bool
	// PublicAPIBaseURL is the externally reachable base URL used to build the
	// per-recipient open-tracking pixel. When empty, no pixel is injected.
	PublicAPIBaseURL string
}

// NewSendOrchestrator wires a SendOrchestrator.
func NewSendOrchestrator(cfg SendOrchestratorConfig) *SendOrchestrator {
	c := cfg.Concurrency
	if c <= 0 {
		c = defaultSendConcurrency
	}
	return &SendOrchestrator{
		repo:             cfg.Repo,
		committee:        cfg.Committee,
		project:          cfg.Project,
		email:            cfg.Email,
		concurrency:      c,
		fanoutEnabled:    cfg.FanoutEnabled,
		publicAPIBaseURL: strings.TrimRight(strings.TrimSpace(cfg.PublicAPIBaseURL), "/"),
	}
}

// SendNewsletterInput is the typed input for SendNewsletter.
type SendNewsletterInput struct {
	ProjectUID      string
	NewsletterID    uuid.UUID
	ExpectedVersion int64
	EDName          string
}

// SendFailure describes a single per-recipient failure surfaced from the
// fan-out loop.
type SendFailure struct {
	Email string
	Error string
}

// SendResult is the typed result returned by SendNewsletter.
type SendResult struct {
	Newsletter      *model.Newsletter
	GroupID         string
	TotalRecipients int
	Sent            int
	Failed          int
	Failures        []SendFailure
}

// SendNewsletter resolves the draft, mints group_id, renders the email envelope,
// fans out per-recipient sends to email-service, and transitions the draft to
// status=sent.
func (o *SendOrchestrator) SendNewsletter(ctx context.Context, in SendNewsletterInput) (*SendResult, error) {
	if err := validateProjectUID(in.ProjectUID); err != nil {
		return nil, err
	}

	draft, err := o.repo.Get(ctx, in.NewsletterID)
	if err != nil {
		return nil, err
	}
	if draft.ProjectUID != in.ProjectUID {
		return nil, domain.ErrNotFound
	}
	if draft.Status == model.StatusSent {
		return nil, domain.ErrAlreadySent
	}
	if in.ExpectedVersion != 0 && draft.Version != in.ExpectedVersion {
		return nil, domain.ErrVersionMismatch
	}

	recipients, err := o.resolveRecipients(ctx, draft.ProjectUID, draft.CommitteeUIDs)
	if err != nil {
		return nil, fmt.Errorf("resolve recipients: %w", err)
	}

	projectName, _ := o.project.Name(ctx, draft.ProjectUID)
	if projectName == "" {
		projectName = "Project"
	}

	chrome := render.Chrome{
		Subject:                 draft.Subject,
		BodyHTML:                draft.BodyHTML,
		DisplayName:             projectName,
		IncludeComplianceFooter: true,
		EDName:                  fallbackString(in.EDName, "Executive Director"),
		EDReplyEmail:            draft.EDReplyEmail,
	}
	htmlBody := render.EmailHTML(chrome)
	textBody := render.EmailText(chrome)

	// Atomically claim the send before any email goes out: PersistSendIntent
	// durably records the group_id and bumps the version in one update. This makes
	// the send durable (group_id survives a crash/full-failure for retry) and
	// idempotent under concurrency (a second concurrent send loses the claim race
	// rather than dispatching a duplicate batch). The row stays a draft until
	// MarkSent finalizes it below, which must use the post-claim version.
	groupID, claimedVersion, err := o.repo.PersistSendIntent(ctx, draft.ID, uuid.NewString(), draft.Version)
	if err != nil {
		return nil, fmt.Errorf("persist send intent: %w", err)
	}

	sent, failed, failures := o.fanOut(ctx, fanOutParams{
		newsletterID: draft.ID,
		projectUID:   draft.ProjectUID,
		recipients:   recipients,
		subject:      draft.Subject,
		htmlBody:     htmlBody,
		textBody:     textBody,
		groupID:      groupID,
	})

	// Only flip the draft to `sent` when at least one recipient was delivered
	// to. If every send failed (email-service unreachable, all recipients
	// rejected, etc.) the row stays a draft so the operator can retry without
	// emails ever having gone out. Without this gate, a fully-failed send is
	// permanently indistinguishable from a successful one — no retry path.
	if sent == 0 && len(recipients) > 0 {
		slog.WarnContext(ctx, "newsletter send failed: no recipients delivered, leaving as draft",
			"newsletter_id", draft.ID,
			"project_uid", draft.ProjectUID,
			"group_id", groupID,
			"total_recipients", len(recipients),
			"failed", failed,
		)
		return nil, fmt.Errorf("send failed: 0 of %d recipients delivered", len(recipients))
	}

	updated, markErr := o.repo.MarkSent(ctx, draft.ID, time.Now().UTC(), len(recipients), groupID, claimedVersion)
	if markErr != nil {
		return nil, fmt.Errorf("mark sent: %w", markErr)
	}

	slog.InfoContext(ctx, "newsletter sent",
		"newsletter_id", draft.ID,
		"project_uid", draft.ProjectUID,
		"group_id", groupID,
		"total_recipients", len(recipients),
		"sent", sent,
		"failed", failed,
	)

	return &SendResult{
		Newsletter:      updated,
		GroupID:         groupID,
		TotalRecipients: len(recipients),
		Sent:            sent,
		Failed:          failed,
		Failures:        failures,
	}, nil
}

// TestSendInput is the typed input for TestSend.
type TestSendInput struct {
	ProjectUID   string
	Subject      string
	BodyHTML     string
	ToEmail      string
	EDReplyEmail string
	EDName       string
}

// TestSend dispatches a single test email — no persistence, no analytics, no
// compliance footer.
func (o *SendOrchestrator) TestSend(ctx context.Context, in TestSendInput) error {
	if err := validateProjectUID(in.ProjectUID); err != nil {
		return err
	}
	if err := validateSubject(in.Subject); err != nil {
		return err
	}
	if err := validateBodyHTML(in.BodyHTML); err != nil {
		return err
	}
	if strings.TrimSpace(in.EDReplyEmail) != "" {
		if err := validateEDReplyEmail(in.EDReplyEmail); err != nil {
			return err
		}
	}
	if _, err := mail.ParseAddress(strings.TrimSpace(in.ToEmail)); err != nil {
		return fmt.Errorf("%w: to_email is not a valid email: %v", domain.ErrInvalidRequest, err)
	}

	projectName, _ := o.project.Name(ctx, in.ProjectUID)
	if projectName == "" {
		projectName = "Project"
	}

	chrome := render.Chrome{
		Subject:                 in.Subject,
		BodyHTML:                in.BodyHTML,
		DisplayName:             projectName,
		IncludeComplianceFooter: false,
	}
	htmlBody := render.EmailHTML(chrome)
	textBody := render.EmailText(chrome)

	if !o.fanoutEnabled {
		slog.InfoContext(ctx, "test-send: fanout disabled, accepted without dispatch",
			"to_email", redactEmail(strings.TrimSpace(in.ToEmail)),
			"project_uid", in.ProjectUID,
		)
		return nil
	}
	_, err := o.email.SendEmail(ctx, port.SendEmailInput{
		To:      strings.TrimSpace(in.ToEmail),
		Subject: in.Subject,
		HTML:    htmlBody,
		Text:    textBody,
	})
	if err != nil {
		return fmt.Errorf("dispatch test-send: %w", err)
	}
	slog.InfoContext(ctx, "test-send dispatched",
		"to_email", redactEmail(strings.TrimSpace(in.ToEmail)),
		"project_uid", in.ProjectUID,
	)
	return nil
}

// RecipientCount resolves recipients for the given project and returns the
// unique count. The committee UIDs are caller-supplied; resolveRecipients binds
// each one to projectUID before listing members.
func (o *SendOrchestrator) RecipientCount(ctx context.Context, projectUID string, committeeUIDs []string) (int, error) {
	if err := validateProjectUID(projectUID); err != nil {
		return 0, err
	}
	if err := validateCommitteeUIDs(committeeUIDs); err != nil {
		return 0, err
	}
	recipients, err := o.resolveRecipients(ctx, projectUID, committeeUIDs)
	if err != nil {
		return 0, err
	}
	return len(recipients), nil
}

// Recipients resolves recipients for the given project and returns the unique
// list. Committee UIDs are caller-supplied and bound to projectUID first.
func (o *SendOrchestrator) Recipients(ctx context.Context, projectUID string, committeeUIDs []string) ([]model.CommitteeMember, error) {
	if err := validateProjectUID(projectUID); err != nil {
		return nil, err
	}
	if err := validateCommitteeUIDs(committeeUIDs); err != nil {
		return nil, err
	}
	return o.resolveRecipients(ctx, projectUID, committeeUIDs)
}

// resolveRecipients fans out to the committee client across committees, dedupes
// by lowercased email, and filters obviously bad addresses. The errgroup cancels
// in-flight goroutines as soon as one returns an error so a transient failure
// from one committee doesn't keep the remaining lookups running.
//
// Committee UIDs are caller-supplied data and the inbound gateway only authorizes
// on `project:{projectUID}`. Before listing any members we therefore confirm each
// committee belongs to projectUID; a mismatch is rejected as ErrForbidden so a
// caller authorized for one project can't resolve or send to another project's
// committee members.
func (o *SendOrchestrator) resolveRecipients(ctx context.Context, projectUID string, committeeUIDs []string) ([]model.CommitteeMember, error) {
	results := make([][]model.CommitteeMember, len(committeeUIDs))

	g, gctx := errgroup.WithContext(ctx)
	for i, uid := range committeeUIDs {
		idx, committeeUID := i, uid
		g.Go(func() error {
			owner, err := o.committee.Project(gctx, committeeUID)
			if err != nil {
				return err
			}
			if owner != projectUID {
				return fmt.Errorf("%w: committee %s does not belong to project %s", domain.ErrForbidden, committeeUID, projectUID)
			}
			members, err := o.committee.ListMembers(gctx, committeeUID)
			if err != nil {
				return err
			}
			results[idx] = members
			return nil
		})
	}
	if err := g.Wait(); err != nil {
		return nil, err
	}

	seen := make(map[string]struct{})
	out := make([]model.CommitteeMember, 0)
	for _, members := range results {
		for _, m := range members {
			email := strings.ToLower(strings.TrimSpace(m.Email))
			if email == "" || !strings.Contains(email, "@") {
				continue
			}
			if _, ok := seen[email]; ok {
				continue
			}
			seen[email] = struct{}{}
			out = append(out, model.CommitteeMember{
				Email:     email,
				FirstName: strings.TrimSpace(m.FirstName),
			})
		}
	}
	return out, nil
}

// fanOutParams bundles the inputs to fanOut so the per-recipient send loop can
// build recipient-specific HTML (open-tracking pixel) without a long argument
// list.
type fanOutParams struct {
	newsletterID uuid.UUID
	projectUID   string
	recipients   []model.CommitteeMember
	subject      string
	htmlBody     string
	textBody     string
	groupID      string
}

// fanOut dispatches per-recipient send_email requests to email-service with
// bounded concurrency. The fan-out never returns an error — per-recipient
// failures are captured and surfaced in the result so the caller can decide
// how to react. A nil EmailDispatcher (or FanoutEnabled=false) short-circuits
// to "all sent, none failed" for dev/test environments.
//
// When a public API base URL is configured, each recipient's HTML gets a
// per-recipient open-tracking pixel appended before send so the local
// newsletter_opens table and unique-open analytics are populated.
func (o *SendOrchestrator) fanOut(ctx context.Context, p fanOutParams) (sent, failed int, failures []SendFailure) {
	if len(p.recipients) == 0 {
		return 0, 0, nil
	}
	if !o.fanoutEnabled {
		slog.InfoContext(ctx, "send fanout disabled, marking all as sent without dispatch",
			"total_recipients", len(p.recipients),
			"group_id", p.groupID,
		)
		return len(p.recipients), 0, nil
	}

	sem := make(chan struct{}, o.concurrency)
	var mu sync.Mutex
	var wg sync.WaitGroup
	for _, r := range p.recipients {
		recipient := r
		// Respect ctx cancellation when acquiring a worker slot. A naked
		// `sem <- struct{}{}` would block forever (or until a slot frees) even
		// after the caller cancelled — and then spin up a goroutine per
		// remaining recipient that immediately fails into `failures` with the
		// cancelled context. Selecting on ctx.Done() lets us bail early.
		select {
		case sem <- struct{}{}:
		case <-ctx.Done():
			mu.Lock()
			failed++
			failures = append(failures, SendFailure{Email: recipient.Email, Error: ctx.Err().Error()})
			mu.Unlock()
			continue
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			// Inject a recipient-specific open-tracking pixel so opens land in
			// the local newsletter_opens table keyed by the recipient hash.
			recipientHTML := o.injectOpenPixel(p.htmlBody, p.projectUID, p.newsletterID, recipient.Email)
			_, err := o.email.SendEmail(ctx, port.SendEmailInput{
				To:      recipient.Email,
				Subject: p.subject,
				HTML:    recipientHTML,
				Text:    p.textBody,
				GroupID: p.groupID,
			})
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				failed++
				failures = append(failures, SendFailure{Email: recipient.Email, Error: err.Error()})
				slog.WarnContext(ctx, "send fanout: recipient failed",
					"recipient", redactEmail(recipient.Email),
					"group_id", p.groupID,
					"error", err.Error(),
				)
				return
			}
			sent++
		}()
	}
	wg.Wait()
	return sent, failed, failures
}

// injectOpenPixel appends a per-recipient open-tracking pixel to the rendered
// HTML. The pixel points at
// {base}/projects/{project_uid}/newsletter-opens/{newsletter_uid}?r=<hash>,
// matching the unauthenticated OpenPixel handler route. The recipient hash is
// the same SHA-256 token the open handler validates, so no raw email is ever
// embedded in the URL.
//
// When no public base URL is configured, or the email is unhashable, the HTML is
// returned unchanged — open analytics then come solely from email-service.
func (o *SendOrchestrator) injectOpenPixel(htmlBody, projectUID string, newsletterID uuid.UUID, email string) string {
	if o.publicAPIBaseURL == "" {
		return htmlBody
	}
	hash := HashRecipient(email)
	if hash == "" {
		return htmlBody
	}
	pixelURL := fmt.Sprintf("%s/projects/%s/newsletter-opens/%s?r=%s",
		o.publicAPIBaseURL,
		url.PathEscape(projectUID),
		newsletterID.String(),
		url.QueryEscape(hash),
	)
	pixel := fmt.Sprintf(`<img src="%s" width="1" height="1" alt="" style="display:none" />`, pixelURL)
	// Inject before </body> when present so the pixel sits inside the document;
	// otherwise append. Case-insensitively match the closing tag.
	if idx := lastIndexFold(htmlBody, "</body>"); idx >= 0 {
		return htmlBody[:idx] + pixel + htmlBody[idx:]
	}
	return htmlBody + pixel
}

// lastIndexFold returns the index of the last case-insensitive occurrence of
// substr in s, or -1 if absent. Used to locate the closing </body> tag without
// allocating a fully lowercased copy on the hot path when the tag is missing.
func lastIndexFold(s, substr string) int {
	return strings.LastIndex(strings.ToLower(s), strings.ToLower(substr))
}

func fallbackString(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}

// redactEmail masks the local part of an email for safe logging.
func redactEmail(email string) string {
	at := strings.Index(email, "@")
	if at <= 0 {
		return "***"
	}
	return email[:1] + "***" + email[at:]
}
