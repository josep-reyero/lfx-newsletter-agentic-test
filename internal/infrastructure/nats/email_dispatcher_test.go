// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package nats

import (
	"testing"
	"time"
)

// TestDecodeGroupStatusReply_OpenedAtList covers the newer email-service shape
// where each recipient carries a per-event opened_at_list plus last_opened_at.
// This is the contract-bearing parse path that feeds analytics DailyOpens, so
// the decode and mapping are exercised against representative reply bytes
// rather than pre-normalized port records.
func TestDecodeGroupStatusReply_OpenedAtList(t *testing.T) {
	reply := []byte(`[
		{
			"group_id": "g1",
			"email_id": "e1",
			"to": "a@x",
			"sent_at": "2026-06-01T22:00:00Z",
			"delivered": true,
			"opened": true,
			"opened_at_list": [
				{"event_id": "ev1", "opened_at": "2026-06-01T22:30:00Z"},
				{"event_id": "ev2", "opened_at": "2026-06-02T09:00:00Z"}
			],
			"last_opened_at": "2026-06-02T09:00:00Z",
			"failed": false
		}
	]`)

	records, err := decodeGroupStatusReply(reply)
	if err != nil {
		t.Fatalf("decodeGroupStatusReply: unexpected error: %v", err)
	}
	if len(records) != 1 {
		t.Fatalf("records: got %d, want 1", len(records))
	}
	r := records[0]
	if r.EmailID != "e1" || r.To != "a@x" || r.GroupID != "g1" {
		t.Errorf("identity fields: got %+v", r)
	}
	if !r.Delivered || !r.Opened {
		t.Errorf("delivered/opened: got delivered=%v opened=%v, want true/true", r.Delivered, r.Opened)
	}
	if len(r.OpenedAtList) != 2 {
		t.Fatalf("OpenedAtList: got %d, want 2", len(r.OpenedAtList))
	}
	if r.OpenCount != 2 {
		t.Errorf("OpenCount: got %d, want 2", r.OpenCount)
	}
	wantFirst := time.Date(2026, 6, 1, 22, 30, 0, 0, time.UTC)
	wantLast := time.Date(2026, 6, 2, 9, 0, 0, 0, time.UTC)
	if !r.OpenedAtList[0].Equal(wantFirst) || !r.OpenedAtList[1].Equal(wantLast) {
		t.Errorf("OpenedAtList timestamps: got %v", r.OpenedAtList)
	}
	if r.LastOpened == nil || !r.LastOpened.Equal(wantLast) {
		t.Errorf("LastOpened: got %v, want %v", r.LastOpened, wantLast)
	}
}

// TestDecodeGroupStatusReply_FlatOpenedAt covers the older email-service shape
// (v0.1.3) that only emits a single flat opened_at and no list. The decoder
// must synthesize a one-element OpenedAtList so the analytics code path is
// uniform regardless of the deployed email-service version.
func TestDecodeGroupStatusReply_FlatOpenedAt(t *testing.T) {
	reply := []byte(`[
		{
			"group_id": "g1",
			"email_id": "e1",
			"to": "a@x",
			"sent_at": "2026-06-01T22:00:00Z",
			"delivered": true,
			"opened": true,
			"opened_at": "2026-06-01T22:30:00Z",
			"failed": false
		},
		{
			"group_id": "g1",
			"email_id": "e2",
			"to": "b@x",
			"sent_at": "2026-06-01T22:00:00Z",
			"delivered": true,
			"opened": false,
			"failed": false
		}
	]`)

	records, err := decodeGroupStatusReply(reply)
	if err != nil {
		t.Fatalf("decodeGroupStatusReply: unexpected error: %v", err)
	}
	if len(records) != 2 {
		t.Fatalf("records: got %d, want 2", len(records))
	}

	opened := records[0]
	wantOpen := time.Date(2026, 6, 1, 22, 30, 0, 0, time.UTC)
	if len(opened.OpenedAtList) != 1 || !opened.OpenedAtList[0].Equal(wantOpen) {
		t.Errorf("flat opened_at not promoted to one-element list: got %v", opened.OpenedAtList)
	}
	if opened.OpenCount != 1 {
		t.Errorf("OpenCount: got %d, want 1", opened.OpenCount)
	}
	if opened.LastOpened == nil || !opened.LastOpened.Equal(wantOpen) {
		t.Errorf("LastOpened: got %v, want %v", opened.LastOpened, wantOpen)
	}

	unopened := records[1]
	if len(unopened.OpenedAtList) != 0 {
		t.Errorf("unopened recipient should have empty OpenedAtList, got %v", unopened.OpenedAtList)
	}
	if unopened.LastOpened != nil {
		t.Errorf("unopened recipient should have nil LastOpened, got %v", unopened.LastOpened)
	}
}

// TestDecodeGroupStatusReply_ErrorEnvelope verifies a JSON error envelope from
// email-service surfaces as an error instead of being silently treated as an
// empty record list.
func TestDecodeGroupStatusReply_ErrorEnvelope(t *testing.T) {
	reply := []byte(`{"error": "group not found"}`)

	records, err := decodeGroupStatusReply(reply)
	if err == nil {
		t.Fatalf("expected error for error envelope, got records=%v", records)
	}
	if records != nil {
		t.Errorf("records should be nil on error, got %v", records)
	}
}

// TestDecodeGroupStatusReply_Empty verifies an empty reply degrades to an empty
// (nil, nil) result so analytics can fall back to the engagement-only rollup.
func TestDecodeGroupStatusReply_Empty(t *testing.T) {
	records, err := decodeGroupStatusReply(nil)
	if err != nil {
		t.Fatalf("empty reply should not error, got %v", err)
	}
	if records != nil {
		t.Errorf("empty reply should yield nil records, got %v", records)
	}

	records, err = decodeGroupStatusReply([]byte{})
	if err != nil {
		t.Fatalf("zero-length reply should not error, got %v", err)
	}
	if records != nil {
		t.Errorf("zero-length reply should yield nil records, got %v", records)
	}
}

// TestDecodeGroupStatusReply_Malformed verifies a non-array, non-envelope reply
// surfaces as a decode error rather than panicking or returning partial data.
func TestDecodeGroupStatusReply_Malformed(t *testing.T) {
	reply := []byte(`{"unexpected": "object-not-array"}`)

	records, err := decodeGroupStatusReply(reply)
	if err == nil {
		t.Fatalf("expected error for malformed reply, got records=%v", records)
	}
	if records != nil {
		t.Errorf("records should be nil on malformed reply, got %v", records)
	}
}
