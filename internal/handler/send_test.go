// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/linuxfoundation/lfx-v2-newsletter-service/internal/domain/model"
	"github.com/linuxfoundation/lfx-v2-newsletter-service/internal/domain/port"
	"github.com/linuxfoundation/lfx-v2-newsletter-service/internal/service"
	publicapi "github.com/linuxfoundation/lfx-v2-newsletter-service/pkg/api"
)

// stubRepo is a minimal NewsletterRepository for handler-level send tests.
type stubRepo struct {
	getResult *model.Newsletter

	markCalled  bool
	markGroupID string
}

func (s *stubRepo) Get(context.Context, uuid.UUID) (*model.Newsletter, error) {
	return s.getResult, nil
}

func (s *stubRepo) MarkSent(_ context.Context, id uuid.UUID, sentAt time.Time, totalRecipients int, groupID string, _ int64) (*model.Newsletter, error) {
	s.markCalled = true
	s.markGroupID = groupID
	gid := groupID
	return &model.Newsletter{
		ID:              id,
		Status:          model.StatusSent,
		GroupID:         &gid,
		SentAt:          &sentAt,
		TotalRecipients: totalRecipients,
		Version:         s.getResult.Version + 1,
	}, nil
}

func (s *stubRepo) Create(context.Context, *model.Newsletter) error { return nil }
func (s *stubRepo) List(context.Context, model.ContextType, string) ([]*model.Newsletter, error) {
	return nil, nil
}
func (s *stubRepo) ListAll(context.Context, port.ListFilters) (*port.ListPage, error) {
	return nil, nil
}
func (s *stubRepo) Update(context.Context, *model.Newsletter, int64) (*model.Newsletter, error) {
	return nil, nil
}
func (s *stubRepo) Delete(context.Context, uuid.UUID) error             { return nil }
func (s *stubRepo) RecordOpen(context.Context, uuid.UUID, string) error { return nil }
func (s *stubRepo) Analytics(context.Context, uuid.UUID) (*model.Analytics, error) {
	return nil, nil
}

type stubCommittee struct{ members []model.CommitteeMember }

func (s *stubCommittee) GetMembers(context.Context, string) ([]model.CommitteeMember, error) {
	return s.members, nil
}

func newSendHandler(repo port.NewsletterRepository, committee port.CommitteeClient) http.Handler {
	send := service.NewSendOrchestrator(service.SendOrchestratorConfig{Repo: repo, Committee: committee})
	return New(Config{Send: send, RequireUserAuth: false}).Routes()
}

func sendRequest(t *testing.T, h http.Handler, id, ifMatch, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/newsletters/drafts/"+id+"/send", strings.NewReader(body))
	if ifMatch != "" {
		req.Header.Set("If-Match", ifMatch)
	}
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func sendableDraft() *model.Newsletter {
	return &model.Newsletter{
		ID:            uuid.New(),
		Status:        model.StatusDraft,
		CommitteeUIDs: []string{"committee-1"},
		Version:       3,
	}
}

func TestSendDraftHandler_Success(t *testing.T) {
	draft := sendableDraft()
	repo := &stubRepo{getResult: draft}
	committee := &stubCommittee{members: []model.CommitteeMember{{Email: "a@example.com", FirstName: "A"}}}
	h := newSendHandler(repo, committee)

	gid := uuid.New().String()
	rec := sendRequest(t, h, draft.ID.String(), "\"3\"", `{"groupId":"`+gid+`"}`)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	// Fresh ETag reflects the incremented version.
	if got := rec.Header().Get("ETag"); got != "\"4\"" {
		t.Fatalf("expected fresh ETag \"4\", got %q", got)
	}
	if !repo.markCalled || repo.markGroupID != gid {
		t.Fatalf("MarkSent should persist groupId %q, got called=%v gid=%q", gid, repo.markCalled, repo.markGroupID)
	}

	var resp publicapi.Newsletter
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("response is not a Newsletter: %v", err)
	}
	if resp.GroupID == nil || *resp.GroupID != gid {
		t.Fatalf("response must carry persisted groupId %q, got %+v", gid, resp.GroupID)
	}
	if resp.Status != publicapi.Status(model.StatusSent) {
		t.Fatalf("response status should be sent, got %q", resp.Status)
	}
}

func TestSendDraftHandler_MissingIfMatch(t *testing.T) {
	draft := sendableDraft()
	h := newSendHandler(&stubRepo{getResult: draft}, &stubCommittee{})
	rec := sendRequest(t, h, draft.ID.String(), "", `{"groupId":"`+uuid.New().String()+`"}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("missing If-Match should be 400, got %d", rec.Code)
	}
}

func TestSendDraftHandler_MissingGroupID(t *testing.T) {
	draft := sendableDraft()
	repo := &stubRepo{getResult: draft}
	h := newSendHandler(repo, &stubCommittee{members: []model.CommitteeMember{{Email: "a@example.com"}}})
	rec := sendRequest(t, h, draft.ID.String(), "\"3\"", `{}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("missing groupId should be 400, got %d body=%s", rec.Code, rec.Body.String())
	}
	if repo.markCalled {
		t.Fatal("MarkSent must not be called when groupId is missing")
	}
}

func TestSendDraftHandler_InvalidGroupID(t *testing.T) {
	draft := sendableDraft()
	repo := &stubRepo{getResult: draft}
	h := newSendHandler(repo, &stubCommittee{members: []model.CommitteeMember{{Email: "a@example.com"}}})
	rec := sendRequest(t, h, draft.ID.String(), "\"3\"", `{"groupId":"not-a-uuid"}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("invalid groupId should be 400, got %d", rec.Code)
	}
	if repo.markCalled {
		t.Fatal("MarkSent must not be called when groupId is not a UUID")
	}
}

func TestSendDraftHandler_MalformedJSON(t *testing.T) {
	draft := sendableDraft()
	h := newSendHandler(&stubRepo{getResult: draft}, &stubCommittee{})
	rec := sendRequest(t, h, draft.ID.String(), "\"3\"", `{not json`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("malformed JSON body should be 400, got %d", rec.Code)
	}
}

// ensure the empty-recipient guard surfaces as a 400 through the handler too.
func TestSendDraftHandler_NoRecipients(t *testing.T) {
	draft := sendableDraft()
	repo := &stubRepo{getResult: draft}
	h := newSendHandler(repo, &stubCommittee{members: nil})
	rec := sendRequest(t, h, draft.ID.String(), "\"3\"", `{"groupId":"`+uuid.New().String()+`"}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("no recipients should be 400, got %d", rec.Code)
	}
	if repo.markCalled {
		t.Fatal("MarkSent must not be called when no recipients resolve")
	}
}
