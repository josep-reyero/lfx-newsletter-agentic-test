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

type newsletterRepoStub struct {
	getResult    *model.Newsletter
	updateCalled bool
	deleteCalled bool
}

func (s *newsletterRepoStub) Create(context.Context, *model.Newsletter) error { return nil }
func (s *newsletterRepoStub) Get(context.Context, uuid.UUID) (*model.Newsletter, error) {
	return s.getResult, nil
}
func (s *newsletterRepoStub) List(context.Context, string) ([]*model.Newsletter, error) {
	return nil, nil
}
func (s *newsletterRepoStub) ListAll(context.Context, port.ListFilters) (*port.ListPage, error) {
	return nil, nil
}
func (s *newsletterRepoStub) Update(context.Context, *model.Newsletter, int64) (*model.Newsletter, error) {
	s.updateCalled = true
	return s.getResult, nil
}
func (s *newsletterRepoStub) Delete(context.Context, uuid.UUID) error {
	s.deleteCalled = true
	return nil
}
func (s *newsletterRepoStub) PersistSendIntent(context.Context, uuid.UUID, string, int64) (string, int64, error) {
	return "", 0, nil
}
func (s *newsletterRepoStub) MarkSent(context.Context, uuid.UUID, time.Time, int, string, int64) (*model.Newsletter, error) {
	return nil, nil
}
func (s *newsletterRepoStub) RecordOpen(context.Context, uuid.UUID, string) error { return nil }
func (s *newsletterRepoStub) Analytics(context.Context, uuid.UUID) (*model.Analytics, error) {
	return nil, nil
}

func TestUpdateDraftRejectsClaimedSend(t *testing.T) {
	groupID := uuid.NewString()
	repo := &newsletterRepoStub{getResult: claimedDraft(groupID)}
	svc := NewNewsletterService(repo)

	_, err := svc.UpdateDraft(context.Background(), "project-1", UpdateDraftInput{
		ID:              repo.getResult.ID,
		ExpectedVersion: repo.getResult.Version,
		Subject:         "Updated subject",
		BodyHTML:        "<p>Updated</p>",
		EDReplyEmail:    "ed@example.com",
		CommitteeUIDs:   []string{"committee-1"},
	})
	if !errors.Is(err, domain.ErrSendInProgress) {
		t.Fatalf("UpdateDraft error = %v, want ErrSendInProgress", err)
	}
	if repo.updateCalled {
		t.Fatal("Update must not be called for a claimed draft")
	}
}

func TestDeleteDraftRejectsClaimedSend(t *testing.T) {
	groupID := uuid.NewString()
	repo := &newsletterRepoStub{getResult: claimedDraft(groupID)}
	svc := NewNewsletterService(repo)

	err := svc.DeleteDraft(context.Background(), "project-1", repo.getResult.ID)
	if !errors.Is(err, domain.ErrSendInProgress) {
		t.Fatalf("DeleteDraft error = %v, want ErrSendInProgress", err)
	}
	if repo.deleteCalled {
		t.Fatal("Delete must not be called for a claimed draft")
	}
}

func claimedDraft(groupID string) *model.Newsletter {
	return &model.Newsletter{
		ID:            uuid.New(),
		ProjectUID:    "project-1",
		Subject:       "Subject",
		BodyHTML:      "<p>Hello</p>",
		EDReplyEmail:  "ed@example.com",
		CommitteeUIDs: []string{"committee-1"},
		Status:        model.StatusDraft,
		GroupID:       &groupID,
		Version:       2,
	}
}
