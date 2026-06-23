#!/usr/bin/env bash
#
# SW-043 — graphi PR-review GitHub Action entrypoint.
#
# This script COMPOSES the existing graphi PR-review vertical. It contains NO
# analysis, render, or gate logic of its own — it drives the EXISTING graphi CLI
# subcommands in order against the checked-out repo:
#
#   1. graphi analyze pr-risk      (SW-039 risk score)
#   2. graphi analyze pr-signals   (SW-040 hub/bridge/surprise signals)
#   3. graphi analyze pr-questions (SW-041 reviewer questions)
#   4. graphi pr-comment --publish (SW-042 sticky comment + optional merge gate)
#
# Local-first contract (AC2): the PR diff is computed LOCALLY with `git diff`
# against the checked-out repo (NOT the GitHub get-diff API). The ONLY outbound
# network connection is step 4's sticky-comment upsert through the GitHub REST API
# (engine/review.GitHubHost). Steps 1-3 (engine analysis) perform ZERO outbound.
#
# Security (S1): the GitHub token is read from the environment (GITHUB_TOKEN) and
# is NEVER placed on the command line (argv is world-readable via /proc) and NEVER
# echoed. The graphi engine resolves the real comment host from the environment
# (review.HostFromEnv); the token never touches a graphi flag.
set -euo pipefail

# --- Inputs (forwarded as INPUT_* env by the composite action.yml) -------------
GRAPHI_BIN="${GRAPHI_BIN:?GRAPHI_BIN must point to the pinned graphi binary}"
BASE_REF="${INPUT_BASE_REF:-}"
HEAD_REF="${INPUT_HEAD_REF:-}"
PR_REF="${INPUT_PR_REF:-}"
PROVENANCE="${INPUT_PROVENANCE:-summary}"
MERGE_GATE="${INPUT_MERGE_GATE:-false}"
GATE_THRESHOLD="${INPUT_GATE_THRESHOLD:-700}"
COMMENT_MARKER="${INPUT_COMMENT_MARKER:-}"   # informational; the engine owns the sticky marker
WORKDIR="${INPUT_WORKING_DIRECTORY:-.}"

cd "$WORKDIR"

# --- Local-first diff acquisition (AC2: no GitHub get-diff API) ----------------
DIFF_FILE="$(mktemp -t graphi-pr-diff.XXXXXX)"
trap 'rm -f "$DIFF_FILE"' EXIT

if [[ -n "$BASE_REF" && -n "$HEAD_REF" ]]; then
  git diff "${BASE_REF}..${HEAD_REF}" > "$DIFF_FILE"
elif [[ -n "$BASE_REF" ]]; then
  git diff "${BASE_REF}" > "$DIFF_FILE"
else
  # Default: diff the PR head against its merge-base with the default branch.
  git diff "$(git merge-base HEAD origin/HEAD 2>/dev/null || echo HEAD~1)..HEAD" > "$DIFF_FILE"
fi

# --- Step 1-3: engine analysis in-runner, ZERO outbound (siblings in order) ----
echo "graphi: analyzing PR (pr-risk -> pr-signals -> pr-questions); engine is zero-egress"
"$GRAPHI_BIN" analyze pr-risk      -diff-path "$DIFF_FILE" -provenance "$PROVENANCE" > /tmp/graphi-pr-risk.json
"$GRAPHI_BIN" analyze pr-signals   -diff-path "$DIFF_FILE" -provenance "$PROVENANCE" > /tmp/graphi-pr-signals.json
"$GRAPHI_BIN" analyze pr-questions -diff-path "$DIFF_FILE" -provenance "$PROVENANCE" > /tmp/graphi-pr-questions.json

# --- Step 4: sticky comment upsert — the ONLY outbound connection (GitHub API) -
# The token is supplied via the environment (GITHUB_TOKEN), consumed by the engine
# host resolver; it is NEVER passed as a flag.
GATE_FLAGS=()
if [[ "$MERGE_GATE" == "true" ]]; then
  GATE_FLAGS+=(-gate -gate-threshold "$GATE_THRESHOLD")
fi

echo "graphi: publishing sticky PR comment (single GitHub API egress)"
"$GRAPHI_BIN" pr-comment \
  -diff-path "$DIFF_FILE" \
  -pr "$PR_REF" \
  -provenance "$PROVENANCE" \
  "${GATE_FLAGS[@]}" \
  -publish > /tmp/graphi-publish.json

# --- Project Action outputs from the byte-stable engine/review.PublishResult ----
# Outputs are PROJECTED from the contract JSON, never recomputed.
PUBLISH_JSON="$(cat /tmp/graphi-publish.json)"

# gate-status <= .gate.verdict (PASS | BLOCK)
GATE_STATUS="$(printf '%s' "$PUBLISH_JSON" | grep -o '"verdict":"[A-Z]*"' | head -n1 | sed 's/.*:"//; s/"//')"
# comment-url <= .upsert.comment_id (host comment identifier)
COMMENT_ID="$(printf '%s' "$PUBLISH_JSON" | grep -o '"comment_id":"[^"]*"' | head -n1 | sed 's/.*:"//; s/"//')"
# risk-score <= worst region score parsed from the pr-risk report
RISK_SCORE="$(printf '%s' "$(cat /tmp/graphi-pr-risk.json)" | grep -o '"score":"[0-9.]*"' | sed 's/.*:"//; s/"//' | sort -r | head -n1)"
RISK_SCORE="${RISK_SCORE:-0.000}"

# Emit to the GitHub Actions outputs file (or stdout when run locally).
if [[ -n "${GITHUB_OUTPUT:-}" ]]; then
  {
    echo "risk-score=${RISK_SCORE}"
    echo "gate-status=${GATE_STATUS}"
    echo "comment-url=${COMMENT_ID}"
  } >> "$GITHUB_OUTPUT"
else
  echo "risk-score=${RISK_SCORE}"
  echo "gate-status=${GATE_STATUS}"
  echo "comment-url=${COMMENT_ID}"
fi

# Fail the step when the merge gate BLOCKs so branch protection can enforce it.
if [[ "$MERGE_GATE" == "true" && "$GATE_STATUS" == "BLOCK" ]]; then
  echo "graphi: merge gate BLOCK (risk threshold exceeded)" >&2
  exit 1
fi
