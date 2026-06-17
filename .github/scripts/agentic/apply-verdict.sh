#!/usr/bin/env bash
# Copyright The Linux Foundation and each contributor to LFX.
# SPDX-License-Identifier: MIT
#
# Apply the escalation judge's verdict (escalation.json: {needs-human, reason}).
#
# Two outputs:
#   - The needs-human label, which the gate reads. It is sticky and add-only:
#     this script only ever ADDS it, never removes it, so a later push that makes
#     the change benign does not un-escalate. Only a human clears it.
#   - A single escalation verdict comment, upserted each run, that records the
#     current decision for EITHER verdict (escalate or not) and why. Keeping one
#     comment and editing it (rather than posting per push) shows the live
#     verdict without spamming the thread.
#
# Inputs (env): REPO, PR_NUMBER, VERDICT (path to escalation.json).
# Honors AGENTIC_DRY_RUN / missing token via lib.sh.

set -euo pipefail

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source-path=SCRIPTDIR
# shellcheck source=lib.sh
. "$HERE/lib.sh"

: "${REPO:?REPO is required}"
: "${PR_NUMBER:?PR_NUMBER is required}"
: "${VERDICT:?VERDICT path is required}"

LABEL="needs-human"

verdict_json="$(ag_read_agent_json "$VERDICT")"
needs_human="$(jq -r '."needs-human" // false' <<<"$verdict_json")"
reason="$(jq -r '.reason // ""' <<<"$verdict_json" | ag_strip_markers)"
reason="$(printf '%s' "$reason" | sed -E 's/^[[:space:]]+//; s/[[:space:]]+$//')"
[ -z "$reason" ] && reason="<no reason given>"

# Build the verdict comment body (one comment, upserted). The hidden marker is
# appended last; the reason was stripped of markers above so it cannot forge it.
if [ "$needs_human" = "true" ]; then
  body="$(printf '**needs-human** set by the escalation judge: %s\n\nA human must review before this PR can merge. Only a human clears this flag.\n\n%s' \
    "$reason" "$ag_escalation_marker")"
else
  body="$(printf '**No human required.** The escalation judge reviewed this change and did not flag it for a human: %s\n\n%s' \
    "$reason" "$ag_escalation_marker")"
fi

# Audit trail regardless of what we post.
ag_log "needs-human=${needs_human}: ${reason}"
if [ -n "${GITHUB_STEP_SUMMARY:-}" ]; then
  printf 'Escalation judge: needs-human=%s. %s\n' "$needs_human" "$reason" >> "$GITHUB_STEP_SUMMARY"
fi

if ag_is_dry_run; then
  if [ "$needs_human" = "true" ]; then
    ag_log "[dry-run] add label '$LABEL' and upsert escalation comment: $reason"
  else
    ag_log "[dry-run] upsert escalation comment (no escalation): $reason"
  fi
  exit 0
fi

# Add the sticky label first when escalating: it is the safety-critical signal
# the gate reads, so it must land even if the comment step later fails.
if [ "$needs_human" = "true" ]; then
  gh label create "$LABEL" --repo "$REPO" \
    --color FBCA04 --description "Agentic escalation: a human must review before merge" \
    --force >/dev/null 2>&1 || true
  gh pr edit "$PR_NUMBER" --repo "$REPO" --add-label "$LABEL" >/dev/null
fi

# Upsert the single verdict comment: edit the existing one if present, else post.
cid="$(ag_escalation_comment_id "$REPO" "$PR_NUMBER" || true)"
if [ -n "$cid" ]; then
  gh api -X PATCH "repos/${REPO}/issues/comments/${cid}" -f body="$body" >/dev/null
  ag_log "updated escalation comment ${cid} (needs-human=${needs_human})"
else
  gh api "repos/${REPO}/issues/${PR_NUMBER}/comments" -f body="$body" >/dev/null
  ag_log "posted escalation comment (needs-human=${needs_human})"
fi
