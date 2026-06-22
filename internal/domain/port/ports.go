// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

// Package port defines the inbound and outbound interfaces the service depends
// on. Concrete implementations live in internal/infrastructure or
// internal/repository.
package port

import (
	"context"
	"time"

	"github.com/google/uuid"

	"github.com/linuxfoundation/lfx-v2-newsletter-service/internal/domain/model"
)

// ListFilters narrows a newsletter listing query.
//
// Status is optional: if empty, both drafts and sent newsletters are returned.
// PageToken is the opaque cursor returned in the previous page's response.
type ListFilters struct {
	ProjectUID string
	Status     model.Status
	PageToken  string
	Limit      int
}

// ListPage is one page of newsletters plus an optional NextPageToken for
// continuation.
type ListPage struct {
	Newsletters   []*model.Newsletter
	NextPageToken string
}

// NewsletterRepository persists Newsletter aggregates.
//
// Implementations must surface optimistic-locking conflicts as
// domain.ErrVersionMismatch and missing records as domain.ErrNotFound.
type NewsletterRepository interface {
	Create(ctx context.Context, n *model.Newsletter) error
	Get(ctx context.Context, id uuid.UUID) (*model.Newsletter, error)
	List(ctx context.Context, projectUID string) ([]*model.Newsletter, error)
	ListAll(ctx context.Context, filters ListFilters) (*ListPage, error)
	Update(ctx context.Context, n *model.Newsletter, expectedVersion int64) (*model.Newsletter, error)
	Delete(ctx context.Context, id uuid.UUID) error

	// PersistSendIntent atomically claims a draft for sending: it durably records
	// the group_id and bumps the version in one conditional update *before*
	// fan-out begins, while keeping status=draft. This makes the send durable and
	// idempotent under concurrency — a crash or fully-failed attempt leaves the
	// group_id to retry under, and a concurrent send holding the pre-claim version
	// loses the optimistic-lock race instead of dispatching a duplicate batch. It
	// returns the durable group_id and the post-claim version to pass to MarkSent.
	// A draft already carrying a group_id (a prior unfinished attempt) returns that
	// group and its current version for reuse.
	PersistSendIntent(ctx context.Context, id uuid.UUID, groupID string, expectedVersion int64) (groupID2 string, claimedVersion int64, err error)

	MarkSent(ctx context.Context, id uuid.UUID, sentAt time.Time, totalRecipients int, groupID string, expectedVersion int64) (*model.Newsletter, error)

	// Open tracking
	RecordOpen(ctx context.Context, newsletterID uuid.UUID, recipientHash string) error
	Analytics(ctx context.Context, newsletterID uuid.UUID) (*model.Analytics, error)
}

// CommitteeClient resolves committee members for newsletter recipient calculation.
//
// The concrete implementation talks to lfx-v2-committee-service via the
// `lfx.committee-api.list_members` NATS subject. The inbound gateway authorizes
// the request on `project:{project_uid}`, but a committee UID is caller-supplied
// data — so before listing members the orchestrator must confirm the committee
// belongs to the authorized project. Project resolves a committee's owning
// project UID via `lfx.committee-api.get_project` for exactly that check.
type CommitteeClient interface {
	ListMembers(ctx context.Context, committeeUID string) ([]model.CommitteeMember, error)
	// Project returns the project UID that owns the committee. Used to verify a
	// caller-supplied committee UID is in-scope for the authorized project
	// before any member PII is resolved.
	Project(ctx context.Context, committeeUID string) (string, error)
}

// ProjectMetadataClient resolves the project name and slug used for email
// chrome (subject line variables, header branding, compliance footer).
//
// Backed by NATS subjects `lfx.projects-api.get_name` and `lfx.projects-api.get_slug`
// exposed by lfx-v2-projects-service. The pattern mirrors committee-service's
// project_retriever client.
type ProjectMetadataClient interface {
	Name(ctx context.Context, projectUID string) (string, error)
	Slug(ctx context.Context, projectUID string) (string, error)
}

// SendEmailInput is one per-recipient send envelope dispatched to email-service.
type SendEmailInput struct {
	To      string
	Subject string
	HTML    string
	Text    string
	GroupID string
}

// EmailRecipientRecord mirrors lfx-v2-email-service's per-recipient state, used
// when aggregating analytics. Fields are kept loose because newsletter-service
// only reads a subset.
type EmailRecipientRecord struct {
	EmailID    string
	GroupID    string
	To         string
	SentAt     *time.Time
	Delivered  bool
	Opened     bool
	OpenCount  int
	LastOpened *time.Time
	Failed     bool
}

// EmailEngagement is the per-group rollup returned by email-service.
type EmailEngagement struct {
	GroupID     string
	TotalSent   int
	Delivered   int
	Opened      int
	UniqueOpens int
	Failed      int
}

// EmailDispatcher fans out individual emails to lfx-v2-email-service and
// fetches engagement data needed for analytics aggregation.
//
// All calls are NATS request/reply against the email-service subjects:
//   - lfx.email-service.send_email
//   - lfx.email-service.get_email_status
//   - lfx.email-service.get_email_engagement_analytics
//
// No auth context is propagated — see comments on ProjectMetadataClient.
type EmailDispatcher interface {
	SendEmail(ctx context.Context, in SendEmailInput) (emailID string, err error)
	GetEngagement(ctx context.Context, groupID string) (*EmailEngagement, error)
	GetStatusByEmailID(ctx context.Context, emailID string) (*EmailRecipientRecord, error)
	// ListGroupRecipients returns every per-recipient record email-service holds
	// for a group_id. Used to make a retried send idempotent: recipients already
	// accepted under the same group are skipped instead of being re-sent. An
	// empty/unknown group returns an empty slice, not an error.
	ListGroupRecipients(ctx context.Context, groupID string) ([]EmailRecipientRecord, error)
}
