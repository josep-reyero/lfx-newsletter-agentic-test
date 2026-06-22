// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package handler

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/linuxfoundation/lfx-v2-newsletter-service/internal/domain"
	pkgerrors "github.com/linuxfoundation/lfx-v2-newsletter-service/pkg/errors"
)

func TestClassifyErrorMapsTypedWrappersAndDomainPrecedence(t *testing.T) {
	tests := []struct {
		name       string
		err        error
		wantStatus int
		wantCode   string
	}{
		{
			name:       "wrapped not found",
			err:        fmt.Errorf("committee lookup: %w", pkgerrors.NewNotFound("committee not found")),
			wantStatus: http.StatusNotFound,
			wantCode:   "not_found",
		},
		{
			name:       "wrapped validation",
			err:        fmt.Errorf("committee lookup: %w", pkgerrors.NewValidation("committee_uid is required")),
			wantStatus: http.StatusBadRequest,
			wantCode:   "invalid_request",
		},
		{
			name:       "wrapped conflict",
			err:        fmt.Errorf("send: %w", pkgerrors.NewConflict("duplicate send")),
			wantStatus: http.StatusConflict,
			wantCode:   "conflict",
		},
		{
			name:       "wrapped service unavailable",
			err:        fmt.Errorf("nats request: %w", pkgerrors.NewServiceUnavailable("nats unavailable")),
			wantStatus: http.StatusServiceUnavailable,
			wantCode:   "service_unavailable",
		},
		{
			name:       "already sent domain sentinel wins over conflict wrapper",
			err:        pkgerrors.NewConflict("send conflict", domain.ErrAlreadySent),
			wantStatus: http.StatusConflict,
			wantCode:   "already_sent",
		},
		{
			name:       "version mismatch domain sentinel wins over conflict wrapper",
			err:        pkgerrors.NewConflict("update conflict", domain.ErrVersionMismatch),
			wantStatus: http.StatusPreconditionFailed,
			wantCode:   "version_mismatch",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotStatus, gotCode := classifyError(tt.err)
			if gotStatus != tt.wantStatus || gotCode != tt.wantCode {
				t.Fatalf("classifyError() = (%d, %q), want (%d, %q)", gotStatus, gotCode, tt.wantStatus, tt.wantCode)
			}
		})
	}
}
