// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package handler

import (
	"errors"
	"html"
	"log/slog"
	"net/http"

	"github.com/linuxfoundation/lfx-v2-newsletter-service/internal/domain"
)

// UnsubscribeConfirm handles GET /newsletters/unsubscribe?t=<token>.
//
// This endpoint is *intentionally unauthenticated* — it is requested by a
// newsletter recipient clicking the footer link in their mail client, which
// has no session. Authorization comes from the HMAC-signed token: only
// someone who received the email (or this service) can produce a valid token
// for a given (project_uid, recipientHash) pair.
//
// GET is deliberately NON-mutating. The footer URL is embedded in outgoing
// mail, so mail-client link previews and security scanners fetch it before a
// human ever clicks; recording the opt-out on GET would silently unsubscribe
// recipients. Instead GET validates the token and renders a confirmation page
// with a one-click POST form. The opt-out is recorded only by the POST handler
// (Unsubscribe). Always returns text/html so the browser renders a page rather
// than offering a JSON download.
func (h *Handler) UnsubscribeConfirm(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	// Go's net/http ServeMux routes HEAD to a GET-only pattern. Link checkers,
	// security scanners, and mail-client preview engines commonly probe URLs
	// with HEAD. Rendering the confirmation page is already non-mutating, but
	// treat HEAD as an explicit no-op so we never spend a project lookup or
	// emit a body for an automated probe.
	if r.Method == http.MethodHead {
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(http.StatusOK)
		return
	}

	token := r.URL.Query().Get("t")

	if h.unsub == nil {
		slog.ErrorContext(ctx, "unsubscribe: service not configured")
		writeUnsubscribeHTML(w, http.StatusInternalServerError, "Unsubscribe unavailable", "Unsubscribe is not configured on this server.")
		return
	}

	// Validate the token before showing the form so a tampered or expired link
	// surfaces an error rather than a form that would fail on submit.
	projectUID, _, err := h.unsub.VerifyToken(token)
	if err != nil {
		slog.WarnContext(ctx, "unsubscribe: invalid token", "error", err.Error())
		writeUnsubscribeHTML(w, http.StatusBadRequest, "Invalid link", "This unsubscribe link is invalid.")
		return
	}

	displayName := h.projectDisplayName(ctx, projectUID)
	writeUnsubscribeForm(w, html.EscapeString(displayName), html.EscapeString(token))
}

// Unsubscribe handles POST /newsletters/unsubscribe with the token in the
// query string (so the same signed link works as the POST target). This is the
// only path that mutates state: it records the project-scoped opt-out keyed by
// the recipient hash carried in the token. The raw email is never recovered or
// echoed, so the confirmation page is generic.
func (h *Handler) Unsubscribe(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	token := r.URL.Query().Get("t")

	if h.unsub == nil {
		slog.ErrorContext(ctx, "unsubscribe: service not configured")
		writeUnsubscribeHTML(w, http.StatusInternalServerError, "Unsubscribe unavailable", "Unsubscribe is not configured on this server.")
		return
	}

	projectUID, err := h.unsub.Unsubscribe(ctx, token)
	if err != nil {
		if errors.Is(err, domain.ErrInvalidRequest) {
			slog.WarnContext(ctx, "unsubscribe: invalid token", "error", err.Error())
			writeUnsubscribeHTML(w, http.StatusBadRequest, "Invalid link", "This unsubscribe link is invalid.")
			return
		}
		slog.ErrorContext(ctx, "unsubscribe: failed", "error", err.Error())
		writeUnsubscribeHTML(w, http.StatusInternalServerError, "Something went wrong", "We couldn't process your request. Please try again later.")
		return
	}

	displayName := h.projectDisplayName(ctx, projectUID)
	writeUnsubscribeHTML(w, http.StatusOK, "You're unsubscribed",
		"You will no longer receive "+html.EscapeString(displayName)+" newsletters at this address.")
}

// writeUnsubscribeForm renders the GET confirmation page: a one-click POST
// form back to the same signed link. displayName and token must already be
// HTML-safe.
func writeUnsubscribeForm(w http.ResponseWriter, displayName, token string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(`<!DOCTYPE html>
<html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>Confirm unsubscribe</title></head>
<body style="font-family:-apple-system,BlinkMacSystemFont,'Segoe UI',Roboto,sans-serif;max-width:560px;margin:48px auto;padding:0 16px;color:#1F2937;">
<h1 style="font-size:22px;">Unsubscribe from ` + displayName + ` newsletters?</h1>
<p style="font-size:15px;line-height:1.6;color:#4B5563;">Confirm to stop receiving ` + displayName + ` newsletters at this address.</p>
<form method="POST" action="/newsletters/unsubscribe?t=` + token + `">
<button type="submit" style="font-size:15px;padding:10px 20px;background:#3B82F6;color:#fff;border:none;border-radius:6px;cursor:pointer;">Unsubscribe</button>
</form>
<p style="font-size:12px;color:#9CA3AF;margin-top:32px;">Delivered by <strong style="color:#3B82F6;">LFX</strong></p>
</body></html>`))
}

// writeUnsubscribeHTML writes a minimal self-contained confirmation page.
// Both heading and body must already be HTML-safe.
func writeUnsubscribeHTML(w http.ResponseWriter, status int, heading, body string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_, _ = w.Write([]byte(`<!DOCTYPE html>
<html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>` + heading + `</title></head>
<body style="font-family:-apple-system,BlinkMacSystemFont,'Segoe UI',Roboto,sans-serif;max-width:560px;margin:48px auto;padding:0 16px;color:#1F2937;">
<h1 style="font-size:22px;">` + heading + `</h1>
<p style="font-size:15px;line-height:1.6;color:#4B5563;">` + body + `</p>
<p style="font-size:12px;color:#9CA3AF;margin-top:32px;">Delivered by <strong style="color:#3B82F6;">LFX</strong></p>
</body></html>`))
}
