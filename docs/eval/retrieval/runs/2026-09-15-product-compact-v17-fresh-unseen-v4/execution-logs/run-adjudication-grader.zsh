#!/bin/zsh
set -eu
setopt pipefail

: ${RUN_DIR:?RUN_DIR is required}
: ${PACKET_DIR:?PACKET_DIR is required}
: ${PACKET_LIST:?PACKET_LIST is required}
: ${ACTOR_ROOT:?ACTOR_ROOT is required}
: ${EXPECTED_PACKETS:?EXPECTED_PACKETS is required}

GRADER_MODEL=${GRADER_MODEL:-gpt-6-astra}
FAILURE_MARKER="$ACTOR_ROOT/adjudication-grader-operator-failed"
RAW_DIR="$RUN_DIR/grades-raw"
LOG_DIR="$RUN_DIR/execution-logs/adjudication-grader"

test "$EXPECTED_PACKETS" -gt 0
test -d "$RUN_DIR"
test -d "$PACKET_DIR"
test -d "$RAW_DIR"
test -f "$PACKET_LIST"
test ! -e "$FAILURE_MARKER"
test ! -e "$LOG_DIR"
test "$(wc -l < "$PACKET_LIST" | tr -d ' ')" = "$EXPECTED_PACKETS"
mkdir -p "$LOG_DIR" "$ACTOR_ROOT/grader"

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
typeset -i grader_rc=0
typeset packet_name packet_path item_stem output_path event_log stderr_log item_worktree

while IFS= read -r packet_name; do
  completed_count=$((completed_count + 1))
  [[ "$packet_name" =~ '^[A-Za-z0-9._-]+\.txt$' ]]
  packet_path="$PACKET_DIR/$packet_name"
  item_stem=${packet_name:r}
  output_path="$RAW_DIR/$packet_name"
  event_log="$LOG_DIR/${item_stem}.jsonl"
  stderr_log="$LOG_DIR/${item_stem}.stderr"
  item_worktree="$ACTOR_ROOT/grader/${completed_count}"
  test -f "$packet_path"
  test ! -e "$output_path"
  test ! -e "$event_log"
  test ! -e "$stderr_log"
  test ! -e "$item_worktree"
  mkdir -p "$item_worktree"
  git -C "$item_worktree" init -q

  if ! codex exec --ephemeral --ignore-user-config --ignore-rules -s read-only \
      -m "$GRADER_MODEL" -c 'model_reasoning_effort="high"' \
      -C "$item_worktree" --json -o "$output_path" - \
      < "$packet_path" > "$event_log" 2> "$stderr_log"; then
    grader_rc=1
    : > "$FAILURE_MARKER"
    print -r -- "adjudication_grader_invocation_failure_at=$completed_count"
    break
  fi
  if ! test -s "$output_path" || ! audit_event_log "$event_log"; then
    grader_rc=1
    : > "$FAILURE_MARKER"
    print -r -- "adjudication_grader_audit_failure_at=$completed_count"
    break
  fi
  print -r -- "adjudication_grader_complete=$completed_count/$EXPECTED_PACKETS"
done < "$PACKET_LIST"

test "$grader_rc" = 0
test "$completed_count" = "$EXPECTED_PACKETS"
test ! -e "$FAILURE_MARKER"
print -r -- "adjudication_grading_complete=$completed_count/$EXPECTED_PACKETS errors=0 retries=0 tool_events=0"
