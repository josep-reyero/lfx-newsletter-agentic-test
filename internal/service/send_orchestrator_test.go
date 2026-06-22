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

type sendDraftRepoFake struct {
	newsletter    *model.Newsletter
	markedGroupID string
}

func (f *sendDraftRepoFake) Create(context.Context, *model.Newsletter) error { return nil }
func (f *sendDraftRepoFake) Get(context.Context, uuid.UUID) (*model.Newsletter, error) {
	return f.newsletter, nil
}
func (f *sendDraftRepoFake) List(context.Context, model.ContextType, string) ([]*model.Newsletter, error) {
	return nil, nil
}
func (f *sendDraftRepoFake) ListAll(context.Context, port.ListFilters) (*port.ListPage, error) {
	return nil, nil
}
func (f *sendDraftRepoFake) Update(context.Context, *model.Newsletter, int64) (*model.Newsletter, error) {
	return nil, nil
}
func (f *sendDraftRepoFake) Delete(context.Context, uuid.UUID) error { return nil }
func (f *sendDraftRepoFake) MarkSent(_ context.Context, _ uuid.UUID, sentAt time.Time, totalRecipients int, groupID string, _ int64) (*model.Newsletter, error) {
	f.markedGroupID = groupID
	f.newsletter.Status = model.StatusSent
	f.newsletter.SentAt = &sentAt
	f.newsletter.TotalRecipients = totalRecipients
	f.newsletter.GroupID = &groupID
	return f.newsletter, nil
}
func (f *sendDraftRepoFake) RecordOpen(context.Context, uuid.UUID, string) error { return nil }
func (f *sendDraftRepoFake) Analytics(context.Context, uuid.UUID) (*model.Analytics, error) {
	return nil, nil
}

type sendDraftCommitteeFake struct{}

func (sendDraftCommitteeFake) GetMembers(context.Context, string) ([]model.CommitteeMember, error) {
	return []model.CommitteeMember{{Email: "a@example.org"}, {Email: "b@example.org"}}, nil
}

func TestSendDraftRejectsInvalidGroupID(t *testing.T) {
	orchestrator := NewSendOrchestrator(SendOrchestratorConfig{})

	_, err := orchestrator.SendDraft(context.Background(), SendDraftInput{
		DraftID: uuid.New(),
		GroupID: "not-a-uuid",
	})
	if !errors.Is(err, domain.ErrInvalidRequest) {
		t.Fatalf("SendDraft error = %v, want ErrInvalidRequest", err)
	}
}

func TestSendDraftPersistsTrimmedUUIDGroupID(t *testing.T) {
	draftID := uuid.New()
	groupID := uuid.NewString()
	repo := &sendDraftRepoFake{
		newsletter: &model.Newsletter{
			ID:            draftID,
			ContextType:   model.ContextProject,
			ContextUID:    uuid.NewString(),
			Subject:       "Subject",
			BodyHTML:      "<p>Body</p>",
			EDReplyEmail:  "ed@example.org",
			CommitteeUIDs: []string{"committee-1"},
			Status:        model.StatusDraft,
			Version:       3,
		},
	}
	orchestrator := NewSendOrchestrator(SendOrchestratorConfig{
		Repo:      repo,
		Committee: sendDraftCommitteeFake{},
	})

	got, err := orchestrator.SendDraft(context.Background(), SendDraftInput{
		DraftID:         draftID,
		ExpectedVersion: 3,
		GroupID:         " " + groupID + " ",
	})
	if err != nil {
		t.Fatalf("SendDraft: %v", err)
	}
	if repo.markedGroupID != groupID {
		t.Fatalf("MarkSent groupID = %q, want %q", repo.markedGroupID, groupID)
	}
	if got.GroupID == nil || *got.GroupID != groupID {
		t.Fatalf("returned GroupID = %v, want %q", got.GroupID, groupID)
	}
}
