// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/linuxfoundation/lfx-v2-newsletter-service/internal/domain/model"
	"github.com/linuxfoundation/lfx-v2-newsletter-service/internal/domain/port"
	pkgerrors "github.com/linuxfoundation/lfx-v2-newsletter-service/pkg/errors"
)

type sendRepoFake struct {
	newsletter    *model.Newsletter
	markSentCalls int
}

func (f *sendRepoFake) Create(context.Context, *model.Newsletter) error { return nil }
func (f *sendRepoFake) Get(context.Context, uuid.UUID) (*model.Newsletter, error) {
	return f.newsletter, nil
}
func (f *sendRepoFake) List(context.Context, string) ([]*model.Newsletter, error) { return nil, nil }
func (f *sendRepoFake) ListAll(context.Context, port.ListFilters) (*port.ListPage, error) {
	return nil, nil
}
func (f *sendRepoFake) Update(context.Context, *model.Newsletter, int64) (*model.Newsletter, error) {
	return nil, nil
}
func (f *sendRepoFake) Delete(context.Context, uuid.UUID) error { return nil }
func (f *sendRepoFake) MarkSent(context.Context, uuid.UUID, time.Time, int, string, int64) (*model.Newsletter, error) {
	f.markSentCalls++
	return f.newsletter, nil
}
func (f *sendRepoFake) RecordOpen(context.Context, uuid.UUID, string) error { return nil }
func (f *sendRepoFake) Analytics(context.Context, uuid.UUID) (*model.Analytics, error) {
	return nil, nil
}

type sendCommitteeFake struct {
	members []model.CommitteeMember
}

func (f sendCommitteeFake) ListMembers(context.Context, string) ([]model.CommitteeMember, error) {
	return f.members, nil
}

type sendProjectFake struct{}

func (sendProjectFake) Name(context.Context, string) (string, error) { return "Test Project", nil }
func (sendProjectFake) Slug(context.Context, string) (string, error) { return "test-project", nil }

type sendEmailFake struct {
	err   error
	sends int
}

func (f *sendEmailFake) SendEmail(context.Context, port.SendEmailInput) (string, error) {
	f.sends++
	if f.err != nil {
		return "", f.err
	}
	return uuid.NewString(), nil
}
func (f *sendEmailFake) GetEngagement(context.Context, string) (*port.EmailEngagement, error) {
	return nil, nil
}
func (f *sendEmailFake) GetStatusByEmailID(context.Context, string) (*port.EmailRecipientRecord, error) {
	return nil, nil
}

func TestSendNewsletter_AllFanoutFailuresDoesNotMarkSent(t *testing.T) {
	projectUID := "63f32fa9-b1be-4b1a-9a1f-98fb2dd34870"
	newsletterID := uuid.New()
	repo := &sendRepoFake{
		newsletter: &model.Newsletter{
			ID:            newsletterID,
			ProjectUID:    projectUID,
			Subject:       "Quarterly update",
			BodyHTML:      "<p>Hello</p>",
			EDReplyEmail:  "ed@example.org",
			CommitteeUIDs: []string{"committee-1"},
			Status:        model.StatusDraft,
			Version:       7,
		},
	}
	email := &sendEmailFake{err: errors.New("nats timeout")}
	orchestrator := NewSendOrchestrator(SendOrchestratorConfig{
		Repo:          repo,
		Committee:     sendCommitteeFake{members: []model.CommitteeMember{{Email: "a@example.org"}, {Email: "b@example.org"}}},
		Project:       sendProjectFake{},
		Email:         email,
		Concurrency:   2,
		FanoutEnabled: true,
	})

	got, err := orchestrator.SendNewsletter(context.Background(), SendNewsletterInput{
		ProjectUID:      projectUID,
		NewsletterID:    newsletterID,
		ExpectedVersion: 7,
		EDName:          "Executive Director",
	})
	if err == nil {
		t.Fatal("SendNewsletter returned nil error, want service unavailable")
	}
	if got != nil {
		t.Fatalf("SendNewsletter result: got %#v, want nil", got)
	}
	var svcUnavailable pkgerrors.ServiceUnavailable
	if !errors.As(err, &svcUnavailable) {
		t.Fatalf("SendNewsletter error = %T %[1]v, want ServiceUnavailable", err)
	}
	if repo.markSentCalls != 0 {
		t.Fatalf("MarkSent calls: got %d, want 0", repo.markSentCalls)
	}
	if email.sends != 2 {
		t.Fatalf("email sends: got %d, want 2", email.sends)
	}
}
