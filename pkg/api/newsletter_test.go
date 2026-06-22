// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package api

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestNewsletterJSONPreservesCamelCaseContract(t *testing.T) {
	body, err := json.Marshal(Newsletter{
		ID:            "newsletter-1",
		ProjectUID:    "project-1",
		Subject:       "Subject",
		BodyHTML:      "<p>Hello</p>",
		EDReplyEmail:  "ed@example.com",
		CommitteeUIDs: []string{"committee-1"},
	})
	if err != nil {
		t.Fatalf("marshal Newsletter: %v", err)
	}

	got := string(body)
	for _, want := range []string{`"projectUid"`, `"bodyHtml"`, `"edReplyEmail"`, `"committeeUids"`} {
		if !strings.Contains(got, want) {
			t.Fatalf("Newsletter JSON missing %s: %s", want, got)
		}
	}
	for _, notWant := range []string{`"project_uid"`, `"body_html"`, `"ed_reply_email"`, `"committee_uids"`} {
		if strings.Contains(got, notWant) {
			t.Fatalf("Newsletter JSON unexpectedly contains %s: %s", notWant, got)
		}
	}
}

func TestCreateNewsletterRequestAcceptsCamelCaseContract(t *testing.T) {
	var req CreateNewsletterRequest
	if err := json.Unmarshal([]byte(`{
		"subject":"Subject",
		"bodyHtml":"<p>Hello</p>",
		"edReplyEmail":"ed@example.com",
		"committeeUids":["committee-1"]
	}`), &req); err != nil {
		t.Fatalf("unmarshal CreateNewsletterRequest: %v", err)
	}

	if req.BodyHTML != "<p>Hello</p>" || req.EDReplyEmail != "ed@example.com" ||
		len(req.CommitteeUIDs) != 1 || req.CommitteeUIDs[0] != "committee-1" {
		t.Fatalf("request did not decode camelCase fields: %+v", req)
	}
}
