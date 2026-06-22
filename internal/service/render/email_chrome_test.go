// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package render

import (
	"strings"
	"testing"
)

func TestEmailHTMLKeepsMSOWidthFallbackForFluidCard(t *testing.T) {
	html := EmailHTML(Chrome{
		Subject:     "Test Newsletter",
		DisplayName: "LFX",
		BodyHTML:    "<p>Hello</p>",
	})

	for _, want := range []string{
		"<!--[if mso]>",
		`width="680" align="center"`,
		`width="100%" style="width:100%;max-width:680px`,
	} {
		if !strings.Contains(html, want) {
			t.Fatalf("EmailHTML() missing %q", want)
		}
	}
}
