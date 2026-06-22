-- Copyright The Linux Foundation and each contributor to LFX.
-- SPDX-License-Identifier: MIT

CREATE EXTENSION IF NOT EXISTS "pgcrypto";

CREATE TABLE IF NOT EXISTS newsletters (
    id                UUID         PRIMARY KEY DEFAULT gen_random_uuid(),
    context_type      TEXT         NOT NULL CHECK (context_type IN ('foundation','project')),
    context_uid       TEXT         NOT NULL,
    subject           TEXT         NOT NULL,
    body_html         TEXT         NOT NULL,
    ed_reply_email    TEXT         NOT NULL,
    committee_uids    TEXT[]       NOT NULL DEFAULT '{}',
    status            TEXT         NOT NULL DEFAULT 'draft' CHECK (status IN ('draft','sent')),
    sent_at           TIMESTAMPTZ,
    total_recipients  INT          NOT NULL DEFAULT 0,
    created_by        TEXT         NOT NULL,
    version           BIGINT       NOT NULL DEFAULT 1,
    created_at        TIMESTAMPTZ  NOT NULL DEFAULT now(),
    updated_at        TIMESTAMPTZ  NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_newsletters_context ON newsletters (context_type, context_uid);
CREATE INDEX IF NOT EXISTS idx_newsletters_status  ON newsletters (status);

-- Composite index supporting the (context_type, context_uid, updated_at DESC, id DESC)
-- keyset pagination used by ListAll. Without this, every list call falls back
-- to a sort over the context partition.
CREATE INDEX IF NOT EXISTS idx_newsletters_list
    ON newsletters (context_type, context_uid, updated_at DESC, id DESC);

-- newsletter_opens captures one row per open event. recipient_hash is a SHA-256
-- of the lowercased recipient email so we can compute unique opens without
-- persisting PII in this table beyond what the newsletters table already holds.
-- The CHECK constraint enforces the same shape as the handler / service layer
-- regex (64-char lowercase hex), so a bug in any caller can't grow this table
-- with arbitrary text.
CREATE TABLE IF NOT EXISTS newsletter_opens (
    id              UUID         PRIMARY KEY DEFAULT gen_random_uuid(),
    newsletter_id   UUID         NOT NULL REFERENCES newsletters(id) ON DELETE CASCADE,
    recipient_hash  TEXT         NOT NULL,
    opened_at       TIMESTAMPTZ  NOT NULL DEFAULT now()
);

-- PG has no native IF NOT EXISTS on ADD CONSTRAINT; check pg_constraint
-- before adding so re-running schema.sql against an existing DB is a no-op.
DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint WHERE conname = 'newsletter_opens_recipient_hash_format'
    ) THEN
        ALTER TABLE newsletter_opens
            ADD CONSTRAINT newsletter_opens_recipient_hash_format
            CHECK (recipient_hash ~ '^[a-f0-9]{64}$');
    END IF;
END$$;

CREATE INDEX IF NOT EXISTS idx_opens_newsletter            ON newsletter_opens (newsletter_id);
CREATE INDEX IF NOT EXISTS idx_opens_newsletter_recipient  ON newsletter_opens (newsletter_id, recipient_hash);
CREATE INDEX IF NOT EXISTS idx_opens_opened_at             ON newsletter_opens (newsletter_id, opened_at);

-- Bound runaway growth on the unauthenticated open-tracking pixel: collapse
-- repeat hits from the same recipient within the same hour into a single row.
-- opened_at_hour stores the UTC hour bucket as epoch-hours so the conflict
-- target can use a plain immutable column. Avoid date_trunc/AT TIME ZONE here:
-- generated columns reject STABLE timezone functions.
ALTER TABLE newsletter_opens
    ADD COLUMN IF NOT EXISTS opened_at_hour BIGINT
        GENERATED ALWAYS AS (floor(EXTRACT(EPOCH FROM opened_at) / 3600)::bigint) STORED;

-- The application is expected to use ON CONFLICT DO NOTHING when inserting.
CREATE UNIQUE INDEX IF NOT EXISTS uq_opens_newsletter_recipient_hour
    ON newsletter_opens (newsletter_id, recipient_hash, opened_at_hour);

-- PG has no native IF NOT EXISTS on ADD CONSTRAINT; attach the named unique
-- index as a constraint so RecordOpen's ON CONFLICT ON CONSTRAINT target is
-- valid.
DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint WHERE conname = 'uq_opens_newsletter_recipient_hour'
    ) THEN
        ALTER TABLE newsletter_opens
            ADD CONSTRAINT uq_opens_newsletter_recipient_hour
            UNIQUE USING INDEX uq_opens_newsletter_recipient_hour;
    END IF;
END$$;
