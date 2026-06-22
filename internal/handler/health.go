// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package handler

import (
	"context"
	"log/slog"
	"net/http"
	"time"
)

// Livez returns 200 if the process is alive. No external dependencies.
func (h *Handler) Livez(w http.ResponseWriter, r *http.Request) {
	_ = r
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ok"))
}

// Readyz returns 200 if the database AND NATS are reachable, 503 otherwise.
// NATS is a required runtime dependency (recipient resolution, project metadata,
// email dispatch, and engagement analytics all go over NATS), so the pod is not
// ready to serve traffic while it is disconnected.
func (h *Handler) Readyz(w http.ResponseWriter, r *http.Request) {
	if h.db == nil {
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte("no db"))
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	if err := h.db.PingContext(ctx); err != nil {
		slog.WarnContext(r.Context(), "readyz: db ping failed", "error", err.Error())
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte("db unavailable"))
		return
	}
	if h.natsReady != nil {
		if err := h.natsReady(); err != nil {
			slog.WarnContext(r.Context(), "readyz: nats not ready", "error", err.Error())
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte("nats unavailable"))
			return
		}
	}
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ok"))
}
