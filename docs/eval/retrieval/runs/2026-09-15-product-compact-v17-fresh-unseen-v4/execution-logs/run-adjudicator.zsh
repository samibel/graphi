#!/bin/zsh
set -eu
setopt pipefail

: ${RUN_DIR:?RUN_DIR is required}
: ${PROMPT_DIR:?PROMPT_DIR is required}
: ${DISAGREEMENT_LIST:?DISAGREEMENT_LIST is required}
: ${ACTOR_ROOT:?ACTOR_ROOT is required}
: ${EXPECTED_ITEMS:?EXPECTED_ITEMS is required}

ADJUDICATOR_MODEL=${ADJUDICATOR_MODEL:-gpt-5.6-sol}
FAILURE_MARKER="$ACTOR_ROOT/adjudicator-operator-failed"
RAW_DIR="$RUN_DIR/adjudications-raw"
LOG_DIR="$RUN_DIR/execution-logs/adjudicator"

test "$EXPECTED_ITEMS" -gt 0
test -d "$RUN_DIR"
test -d "$PROMPT_DIR"
test -f "$DISAGREEMENT_LIST"
test ! -e "$FAILURE_MARKER"
test ! -e "$RAW_DIR"
test ! -e "$LOG_DIR"
test "$(wc -l < "$DISAGREEMENT_LIST" | tr -d ' ')" = "$EXPECTED_ITEMS"
mkdir -p "$RAW_DIR" "$LOG_DIR" "$ACTOR_ROOT/adjudicator"

audit_event_log() {
  local event_log=$1
  jq -s -e '
    length == 4 and
    .[0].type == "thread.started" and
    .[1].type == "turn.started" and
    .[2].type == "item.completed" and
    .[2].item.type == "agent_message" and
    .[3].type == "turn.completed"
  ' "$event_log" >/dev/null
}

typeset -i completed_count=0
typeset -i adjudicator_rc=0
typeset query_id prompt_path output_path event_log stderr_log item_worktree

while IFS= read -r query_id; do
  completed_count=$((completed_count + 1))
  [[ "$query_id" =~ '^[A-Za-z0-9._-]+$' ]]
  prompt_path="$PROMPT_DIR/${query_id}.txt"
  output_path="$RAW_DIR/${query_id}.txt"
  event_log="$LOG_DIR/${query_id}.jsonl"
  stderr_log="$LOG_DIR/${query_id}.stderr"
  item_worktree="$ACTOR_ROOT/adjudicator/${completed_count}"
  test -f "$prompt_path"
  test ! -e "$output_path"
  test ! -e "$event_log"
  test ! -e "$stderr_log"
  test ! -e "$item_worktree"
  mkdir -p "$item_worktree"
  git -C "$item_worktree" init -q

  if ! codex exec --ephemeral --ignore-user-config --ignore-rules -s read-only \
      -m "$ADJUDICATOR_MODEL" -c 'model_reasoning_effort="high"' \
      -C "$item_worktree" --json -o "$output_path" - \
      < "$prompt_path" > "$event_log" 2> "$stderr_log"; then
    adjudicator_rc=1
    : > "$FAILURE_MARKER"
    print -r -- "adjudicator_invocation_failure_at=$completed_count"
    break
  fi
  if ! test -s "$output_path" || ! audit_event_log "$event_log"; then
    adjudicator_rc=1
    : > "$FAILURE_MARKER"
    print -r -- "adjudicator_audit_failure_at=$completed_count"
    break
  fi
  print -r -- "adjudicator_complete=$completed_count/$EXPECTED_ITEMS"
done < "$DISAGREEMENT_LIST"

test "$adjudicator_rc" = 0
test "$completed_count" = "$EXPECTED_ITEMS"
test ! -e "$FAILURE_MARKER"
print -r -- "adjudication_complete=$completed_count/$EXPECTED_ITEMS errors=0 retries=0 tool_events=0"
