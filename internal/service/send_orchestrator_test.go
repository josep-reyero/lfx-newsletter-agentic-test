// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package service

import (
	"context"
	"errors"
	"testing"

	"github.com/linuxfoundation/lfx-v2-newsletter-service/internal/domain"
	"github.com/linuxfoundation/lfx-v2-newsletter-service/internal/domain/model"
)

// stubCommittee is a minimal port.CommitteeClient that maps committee UID to its
// owning project and member list. It records which committees had ListMembers
// called so a test can assert resolution is skipped for out-of-scope committees.
type stubCommittee struct {
	owner   map[string]string
	members map[string][]model.CommitteeMember
	listed  map[string]bool
	projErr error
}

func (s *stubCommittee) Project(_ context.Context, committeeUID string) (string, error) {
	if s.projErr != nil {
		return "", s.projErr
	}
	owner, ok := s.owner[committeeUID]
	if !ok {
		return "", domain.ErrNotFound
	}
	return owner, nil
}

func (s *stubCommittee) ListMembers(_ context.Context, committeeUID string) ([]model.CommitteeMember, error) {
	if s.listed == nil {
		s.listed = map[string]bool{}
	}
	s.listed[committeeUID] = true
	return s.members[committeeUID], nil
}

func newOrchestratorWithCommittee(c *stubCommittee) *SendOrchestrator {
	return NewSendOrchestrator(SendOrchestratorConfig{Committee: c})
}

func TestRecipients_RejectsCommitteeFromAnotherProject(t *testing.T) {
	c := &stubCommittee{
		owner:   map[string]string{"committee-a": "project:other"},
		members: map[string][]model.CommitteeMember{"committee-a": {{Email: "a@example.com"}}},
	}
	o := newOrchestratorWithCommittee(c)

	_, err := o.Recipients(context.Background(), "project:mine", []string{"committee-a"})
	if !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("expected ErrForbidden for cross-project committee, got %v", err)
	}
	if c.listed["committee-a"] {
		t.Fatalf("ListMembers must not be called for an out-of-scope committee")
	}
}

func TestRecipients_AllowsCommitteeInProject(t *testing.T) {
	c := &stubCommittee{
		owner: map[string]string{"committee-a": "project:mine"},
		members: map[string][]model.CommitteeMember{
			"committee-a": {{Email: "A@Example.com", FirstName: "Ann"}},
		},
	}
	o := newOrchestratorWithCommittee(c)

	got, err := o.Recipients(context.Background(), "project:mine", []string{"committee-a"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 1 || got[0].Email != "a@example.com" {
		t.Fatalf("expected normalized recipient a@example.com, got %+v", got)
	}
}

func TestRecipientCount_RejectsMixedProjectCommittees(t *testing.T) {
	c := &stubCommittee{
		owner: map[string]string{
			"committee-a": "project:mine",
			"committee-b": "project:other",
		},
		members: map[string][]model.CommitteeMember{
			"committee-a": {{Email: "a@example.com"}},
			"committee-b": {{Email: "b@example.com"}},
		},
	}
	o := newOrchestratorWithCommittee(c)

	_, err := o.RecipientCount(context.Background(), "project:mine", []string{"committee-a", "committee-b"})
	if !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("expected ErrForbidden when any committee is out of scope, got %v", err)
	}
}
