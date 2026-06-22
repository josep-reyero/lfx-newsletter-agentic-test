// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package model

import (
	"time"

	"github.com/google/uuid"
	"github.com/uptrace/bun"
)

// NewsletterUnsubscribe records a project-scoped opt-out. One row means the
// recipient identified by RecipientHash must be excluded from all future sends
// for that project_uid only. The raw email address is intentionally not
// persisted: RecipientHash is a SHA-256 of the lowercased address (the same
// shape as newsletter_opens.recipient_hash) so this table holds no PII.
type NewsletterUnsubscribe struct {
	bun.BaseModel `bun:"table:newsletter_unsubscribes,alias:u"`

	ID            uuid.UUID `bun:"id,pk,type:uuid,default:gen_random_uuid()" json:"id"`
	ProjectUID    string    `bun:"project_uid,notnull" json:"projectUid"`
	RecipientHash string    `bun:"recipient_hash,notnull" json:"recipientHash"`
	CreatedAt     time.Time `bun:"created_at,notnull,default:current_timestamp" json:"createdAt"`
	UpdatedAt     time.Time `bun:"updated_at,notnull,default:current_timestamp" json:"updatedAt"`
}
