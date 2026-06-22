// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/linuxfoundation/lfx-v2-newsletter-service/internal/service"
)

type stubUnsubRepo struct {
	created []string
}

func (s *stubUnsubRepo) CreateUnsubscribe(_ context.Context, projectUID, recipientHash string) error {
	s.created = append(s.created, projectUID+"|"+recipientHash)
	return nil
}
func (s *stubUnsubRepo) ListUnsubscribedHashes(_ context.Context, _ string) (map[string]struct{}, error) {
	return map[string]struct{}{}, nil
}

type stubProjectClient struct{}

func (stubProjectClient) Name(_ context.Context, _ string) (string, error) { return "CNCF", nil }
func (stubProjectClient) Slug(_ context.Context, _ string) (string, error) { return "cncf", nil }

func tokenFromURL(url string) string {
	return url[strings.Index(url, "?t=")+3:]
}

// TestUnsubscribeConfirmGETIsNonMutating asserts that GET renders the
// confirmation form WITHOUT recording an opt-out, so mail-client previews and
// scanners that fetch the URL cannot unsubscribe a recipient. It must also not
// echo the raw email address.
func TestUnsubscribeConfirmGETIsNonMutating(t *testing.T) {
	repo := &stubUnsubRepo{}
	unsub := service.NewUnsubscribeService(repo, []byte("k"), "http://localhost")
	h := &Handler{unsub: unsub, project: stubProjectClient{}}

	token := tokenFromURL(unsub.BuildURL("proj-1", "alice@example.com"))

	req := httptest.NewRequest(http.MethodGet, "/newsletters/unsubscribe?t="+token, nil)
	w := httptest.NewRecorder()
	h.UnsubscribeConfirm(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200. body=%s", w.Code, w.Body.String())
	}
	body := w.Body.String()
	if !strings.Contains(body, "<form") || !strings.Contains(strings.ToLower(body), "post") {
		t.Errorf("GET should render a POST form: %s", body)
	}
	if strings.Contains(body, "alice@example.com") {
		t.Errorf("confirmation page must not echo the raw email: %s", body)
	}
	if len(repo.created) != 0 {
		t.Errorf("GET must not record an unsubscribe; repo.created = %v", repo.created)
	}
}

// TestUnsubscribePOSTRecordsHash asserts the opt-out is recorded only on POST,
// keyed by the recipient hash (never the raw email), with a generic
// confirmation page.
func TestUnsubscribePOSTRecordsHash(t *testing.T) {
	repo := &stubUnsubRepo{}
	unsub := service.NewUnsubscribeService(repo, []byte("k"), "http://localhost")
	h := &Handler{unsub: unsub, project: stubProjectClient{}}

	token := tokenFromURL(unsub.BuildURL("proj-1", "alice@example.com"))

	req := httptest.NewRequest(http.MethodPost, "/newsletters/unsubscribe?t="+token, nil)
	w := httptest.NewRecorder()
	h.Unsubscribe(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200. body=%s", w.Code, w.Body.String())
	}
	if ct := w.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
		t.Errorf("content-type = %q, want text/html", ct)
	}
	body := w.Body.String()
	if !strings.Contains(body, "CNCF") {
		t.Errorf("body missing project name: %s", body)
	}
	if strings.Contains(body, "alice@example.com") {
		t.Errorf("confirmation page must not echo the raw email: %s", body)
	}
	wantHash := service.HashRecipient("alice@example.com")
	if len(repo.created) != 1 || repo.created[0] != "proj-1|"+wantHash {
		t.Errorf("repo.created = %v, want [proj-1|%s]", repo.created, wantHash)
	}
}

func TestUnsubscribeHandlerInvalidToken(t *testing.T) {
	unsub := service.NewUnsubscribeService(&stubUnsubRepo{}, []byte("k"), "http://localhost")
	h := &Handler{unsub: unsub, project: stubProjectClient{}}

	req := httptest.NewRequest(http.MethodPost, "/newsletters/unsubscribe?t=garbage", nil)
	w := httptest.NewRecorder()
	h.Unsubscribe(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}
	if !strings.Contains(w.Body.String(), "invalid") && !strings.Contains(w.Body.String(), "Invalid") {
		t.Errorf("body should mention invalid link: %s", w.Body.String())
	}
}
