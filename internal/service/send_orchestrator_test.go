// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/linuxfoundation/lfx-v2-newsletter-service/internal/domain"
	"github.com/linuxfoundation/lfx-v2-newsletter-service/internal/domain/model"
	"github.com/linuxfoundation/lfx-v2-newsletter-service/internal/domain/port"
)

// fakeRepo is a minimal NewsletterRepository for exercising SendDraft. Only the
// methods SendDraft touches (Get, MarkSent) carry behavior; the rest are unused.
type fakeRepo struct {
	getResult *model.Newsletter
	getErr    error

	markCalled    bool
	markGroupID   string
	markRecipient int
	markResult    *model.Newsletter
	markErr       error
}

func (f *fakeRepo) Get(_ context.Context, _ uuid.UUID) (*model.Newsletter, error) {
	return f.getResult, f.getErr
}

func (f *fakeRepo) MarkSent(_ context.Context, _ uuid.UUID, _ time.Time, totalRecipients int, groupID string, _ int64) (*model.Newsletter, error) {
	f.markCalled = true
	f.markGroupID = groupID
	f.markRecipient = totalRecipients
	if f.markErr != nil {
		return nil, f.markErr
	}
	if f.markResult != nil {
		return f.markResult, nil
	}
	gid := groupID
	return &model.Newsletter{
		ID:              f.getResult.ID,
		Status:          model.StatusSent,
		GroupID:         &gid,
		TotalRecipients: totalRecipients,
		Version:         f.getResult.Version + 1,
	}, nil
}

func (f *fakeRepo) Create(context.Context, *model.Newsletter) error { return nil }
func (f *fakeRepo) List(context.Context, model.ContextType, string) ([]*model.Newsletter, error) {
	return nil, nil
}
func (f *fakeRepo) ListAll(context.Context, port.ListFilters) (*port.ListPage, error) {
	return nil, nil
}
func (f *fakeRepo) Update(context.Context, *model.Newsletter, int64) (*model.Newsletter, error) {
	return nil, nil
}
func (f *fakeRepo) Delete(context.Context, uuid.UUID) error { return nil }
func (f *fakeRepo) RecordOpen(context.Context, uuid.UUID, string) error {
	return nil
}
func (f *fakeRepo) Analytics(context.Context, uuid.UUID) (*model.Analytics, error) {
	return nil, nil
}

// fakeCommittee resolves a fixed member set per committee UID.
type fakeCommittee struct {
	members []model.CommitteeMember
	err     error
}

func (f *fakeCommittee) GetMembers(context.Context, string) ([]model.CommitteeMember, error) {
	return f.members, f.err
}

func draft() *model.Newsletter {
	return &model.Newsletter{
		ID:            uuid.New(),
		Status:        model.StatusDraft,
		CommitteeUIDs: []string{"committee-1"},
		Version:       1,
	}
}

func TestSendDraft_RejectsMissingGroupID(t *testing.T) {
	repo := &fakeRepo{getResult: draft()}
	o := NewSendOrchestrator(SendOrchestratorConfig{Repo: repo, Committee: &fakeCommittee{}})

	_, err := o.SendDraft(context.Background(), SendDraftInput{DraftID: uuid.New(), GroupID: "   "})
	if !errors.Is(err, domain.ErrInvalidRequest) {
		t.Fatalf("expected ErrInvalidRequest for blank groupId, got %v", err)
	}
	if repo.markCalled {
		t.Fatal("MarkSent must not be called when groupId is missing")
	}
}

func TestSendDraft_RejectsNonUUIDGroupID(t *testing.T) {
	repo := &fakeRepo{getResult: draft()}
	o := NewSendOrchestrator(SendOrchestratorConfig{Repo: repo, Committee: &fakeCommittee{}})

	_, err := o.SendDraft(context.Background(), SendDraftInput{DraftID: uuid.New(), GroupID: "not-a-uuid"})
	if !errors.Is(err, domain.ErrInvalidRequest) {
		t.Fatalf("expected ErrInvalidRequest for non-UUID groupId, got %v", err)
	}
	if repo.markCalled {
		t.Fatal("MarkSent must not be called when groupId is not a UUID")
	}
}

func TestSendDraft_RejectsEmptyRecipients(t *testing.T) {
	repo := &fakeRepo{getResult: draft()}
	// committee resolves no usable recipients (all filtered out)
	committee := &fakeCommittee{members: []model.CommitteeMember{{Email: ""}}}
	o := NewSendOrchestrator(SendOrchestratorConfig{Repo: repo, Committee: committee})

	_, err := o.SendDraft(context.Background(), SendDraftInput{DraftID: uuid.New(), GroupID: uuid.NewString()})
	if !errors.Is(err, domain.ErrInvalidRequest) {
		t.Fatalf("expected ErrInvalidRequest when no recipients resolve, got %v", err)
	}
	if repo.markCalled {
		t.Fatal("MarkSent must not be called when zero recipients resolve (no phantom send)")
	}
}

func TestSendDraft_RejectsAlreadySent(t *testing.T) {
	d := draft()
	d.Status = model.StatusSent
	repo := &fakeRepo{getResult: d}
	o := NewSendOrchestrator(SendOrchestratorConfig{Repo: repo, Committee: &fakeCommittee{}})

	_, err := o.SendDraft(context.Background(), SendDraftInput{DraftID: uuid.New(), GroupID: uuid.NewString()})
	if !errors.Is(err, domain.ErrAlreadySent) {
		t.Fatalf("expected ErrAlreadySent, got %v", err)
	}
	if repo.markCalled {
		t.Fatal("MarkSent must not be called for an already-sent draft")
	}
}

func TestSendDraft_RejectsVersionMismatch(t *testing.T) {
	d := draft()
	d.Version = 5
	repo := &fakeRepo{getResult: d}
	o := NewSendOrchestrator(SendOrchestratorConfig{Repo: repo, Committee: &fakeCommittee{}})

	_, err := o.SendDraft(context.Background(), SendDraftInput{DraftID: uuid.New(), GroupID: uuid.NewString(), ExpectedVersion: 2})
	if !errors.Is(err, domain.ErrVersionMismatch) {
		t.Fatalf("expected ErrVersionMismatch, got %v", err)
	}
	if repo.markCalled {
		t.Fatal("MarkSent must not be called on a version mismatch")
	}
}

func TestSendDraft_PersistsNormalizedGroupIDOnSuccess(t *testing.T) {
	repo := &fakeRepo{getResult: draft()}
	committee := &fakeCommittee{members: []model.CommitteeMember{
		{Email: "Alice@Example.com", FirstName: "Alice"},
		{Email: "bob@example.com", FirstName: "Bob"},
	}}
	o := NewSendOrchestrator(SendOrchestratorConfig{Repo: repo, Committee: committee})

	// Upper-cased, padded UUID — must be normalized to canonical lower form.
	canonical := uuid.New()
	raw := "  " + canonical.String() + "  "

	updated, err := o.SendDraft(context.Background(), SendDraftInput{DraftID: uuid.New(), GroupID: raw})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !repo.markCalled {
		t.Fatal("MarkSent should be called on a successful send")
	}
	if repo.markGroupID != canonical.String() {
		t.Fatalf("group_id should be persisted in canonical form: got %q want %q", repo.markGroupID, canonical.String())
	}
	if repo.markRecipient != 2 {
		t.Fatalf("expected 2 resolved recipients, got %d", repo.markRecipient)
	}
	if updated == nil || updated.GroupID == nil || *updated.GroupID != canonical.String() {
		t.Fatalf("response must carry the persisted group_id, got %+v", updated)
	}
	if updated.Status != model.StatusSent {
		t.Fatalf("response status should be sent, got %q", updated.Status)
	}
}
