// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package handler

import (
	"testing"

	"github.com/google/uuid"

	"github.com/linuxfoundation/lfx-v2-newsletter-service/internal/domain/model"
)

func TestToAPINewsletterMapsGroupID(t *testing.T) {
	groupID := uuid.NewString()
	n := &model.Newsletter{
		ID:          uuid.New(),
		ContextType: model.ContextProject,
		ContextUID:  uuid.NewString(),
		GroupID:     &groupID,
	}

	got := toAPINewsletter(n)
	if got.GroupID == nil || *got.GroupID != groupID {
		t.Fatalf("GroupID = %v, want %q", got.GroupID, groupID)
	}
}
