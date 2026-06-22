// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

// Package api is the public HTTP contract for the newsletter service. Field
// names preserve the existing camelCase contract used by the UI/BFF.
package api

import "time"

// Status enumerates newsletter lifecycle states.
type Status string

// Status values persisted by the service.
const (
	StatusDraft Status = "draft"
	StatusSent  Status = "sent"
)

// Newsletter is the response shape returned by single-resource endpoints.
type Newsletter struct {
	ID            string     `json:"id"`
	ProjectUID    string     `json:"projectUid"`
	Subject       string     `json:"subject"`
	BodyHTML      string     `json:"bodyHtml"`
	EDReplyEmail  string     `json:"edReplyEmail"`
	CommitteeUIDs []string   `json:"committeeUids"`
	Status        Status     `json:"status"`
	SentAt        *time.Time `json:"sentAt,omitempty"`
	// GroupID is the lfx-v2-email-service correlation identifier, set when
	// the newsletter is sent. Null on drafts.
	GroupID         *string   `json:"groupId,omitempty"`
	TotalRecipients int       `json:"totalRecipients"`
	CreatedBy       string    `json:"createdBy"`
	Version         int64     `json:"version"`
	CreatedAt       time.Time `json:"createdAt"`
	UpdatedAt       time.Time `json:"updatedAt"`
}

// CreateNewsletterRequest is the body of POST /projects/{project_uid}/newsletters.
type CreateNewsletterRequest struct {
	Subject       string   `json:"subject"`
	BodyHTML      string   `json:"bodyHtml"`
	EDReplyEmail  string   `json:"edReplyEmail"`
	CommitteeUIDs []string `json:"committeeUids"`
}

// UpdateNewsletterRequest is the body of PUT /projects/{project_uid}/newsletters/{newsletter_uid}.
type UpdateNewsletterRequest struct {
	Subject       string   `json:"subject"`
	BodyHTML      string   `json:"bodyHtml"`
	EDReplyEmail  string   `json:"edReplyEmail"`
	CommitteeUIDs []string `json:"committeeUids"`
}

// RecipientCountRequest is the body of POST /projects/{project_uid}/newsletters/recipient-count.
type RecipientCountRequest struct {
	CommitteeUIDs []string `json:"committeeUids"`
}

// RecipientCountResponse is the body of POST /projects/{project_uid}/newsletters/recipient-count.
type RecipientCountResponse struct {
	Count int `json:"count"`
}

// Recipient is a single entry in the preview recipients list.
type Recipient struct {
	Email     string `json:"email"`
	FirstName string `json:"firstName,omitempty"`
}

// RecipientsRequest is the body of POST /projects/{project_uid}/newsletters/recipients.
type RecipientsRequest struct {
	CommitteeUIDs []string `json:"committeeUids"`
}

// RecipientsResponse is the body of POST /projects/{project_uid}/newsletters/recipients.
type RecipientsResponse struct {
	Recipients []Recipient `json:"recipients"`
}

// TestSendRequest is the body of POST /projects/{project_uid}/newsletters/test-send.
type TestSendRequest struct {
	Subject      string `json:"subject"`
	BodyHTML     string `json:"bodyHtml"`
	ToEmail      string `json:"toEmail"`
	EDReplyEmail string `json:"edReplyEmail,omitempty"`
}

// TestSendResponse is the body of POST /projects/{project_uid}/newsletters/test-send.
type TestSendResponse struct {
	OK bool `json:"ok"`
}

// SendFailure describes a single per-recipient failure surfaced from the send fan-out.
type SendFailure struct {
	Email string `json:"email"`
	Error string `json:"error"`
}

// SendNewsletterResponse is the body of POST /projects/{project_uid}/newsletters/{newsletter_uid}/send.
//
// The newsletter-service owns the email dispatch: it mints group_id, resolves
// recipients via NATS to committee-service, and fans out per-recipient sends
// via NATS to email-service. Per-recipient failures are returned so the caller
// can surface them; the newsletter is marked sent when at least one recipient
// was delivered to.
type SendNewsletterResponse struct {
	Newsletter      Newsletter    `json:"newsletter"`
	GroupID         string        `json:"groupId"`
	TotalRecipients int           `json:"totalRecipients"`
	Sent            int           `json:"sent"`
	Failed          int           `json:"failed"`
	Failures        []SendFailure `json:"failures,omitempty"`
}

// NewsletterListItem is one row in the unified list response. Inherits the
// Newsletter shape and adds engagement fields populated only when status='sent'.
type NewsletterListItem struct {
	Newsletter
	UniqueOpens *int     `json:"uniqueOpens,omitempty"`
	OpenRate    *float64 `json:"openRate,omitempty"`
}

// NewsletterListResponse is the body of GET /projects/{project_uid}/newsletters.
type NewsletterListResponse struct {
	Newsletters   []NewsletterListItem `json:"newsletters"`
	NextPageToken string               `json:"nextPageToken,omitempty"`
}

// NewsletterDailyOpens is one bucket of the daily-opens time series.
type NewsletterDailyOpens struct {
	Date        string `json:"date"`
	Opens       int    `json:"opens"`
	UniqueOpens int    `json:"uniqueOpens"`
}

// NewsletterAnalytics is the body of GET /projects/{project_uid}/newsletters/{newsletter_uid}/analytics.
type NewsletterAnalytics struct {
	NewsletterID    string                 `json:"newsletterId"`
	Subject         string                 `json:"subject"`
	Status          Status                 `json:"status"`
	SentAt          *time.Time             `json:"sentAt,omitempty"`
	TotalRecipients int                    `json:"totalRecipients"`
	Delivered       int                    `json:"delivered"`
	Failed          int                    `json:"failed"`
	TotalOpens      int                    `json:"totalOpens"`
	UniqueOpens     int                    `json:"uniqueOpens"`
	OpenRate        float64                `json:"openRate"`
	DailyOpens      []NewsletterDailyOpens `json:"dailyOpens"`
	LastEventAt     *time.Time             `json:"lastEventAt,omitempty"`
}
