#!/bin/zsh
set -eu
setopt pipefail

: ${RUN_DIR:?RUN_DIR is required}
: ${PACKET_DIR:?PACKET_DIR is required}
: ${ACTOR_ROOT:?ACTOR_ROOT is required}
: ${EXPECTED_PACKETS:?EXPECTED_PACKETS is required}

GRADER_MODEL=${GRADER_MODEL:-gpt-6-astra}
ACTOR_MODE=${ACTOR_MODE:-live}
FAILURE_MARKER="$ACTOR_ROOT/grader-operator-failed"
RAW_DIR="$RUN_DIR/grades-raw"
LOG_DIR="$RUN_DIR/execution-logs/grader"

test "$EXPECTED_PACKETS" -gt 0
test -d "$RUN_DIR"
test -d "$PACKET_DIR"
test ! -e "$FAILURE_MARKER"
test ! -e "$RAW_DIR"
test ! -e "$LOG_DIR"

packet_count=$(find "$PACKET_DIR" -type f -name '*.txt' -print | wc -l | tr -d ' ')
test "$packet_count" = "$EXPECTED_PACKETS"
mkdir -p "$RAW_DIR" "$LOG_DIR" "$ACTOR_ROOT/grader"

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

invoke_grader_once() {
  local item_worktree=$1
  local output_path=$2
  local event_log=$3
  local stderr_log=$4
  local packet_path=$5

  case "$ACTOR_MODE" in
    live)
      codex exec --ephemeral --ignore-user-config --ignore-rules -s read-only \
        -m "$GRADER_MODEL" -c 'model_reasoning_effort="high"' \
        -C "$item_worktree" --json -o "$output_path" - \
        < "$packet_path" > "$event_log" 2> "$stderr_log"
      ;;
    *)
      return 64
      ;;
  esac
}

typeset -i completed_count=0
typeset -i grader_rc=0
typeset packet_path item_stem output_path event_log stderr_log item_worktree

while IFS= read -r packet_path; do
  completed_count=$((completed_count + 1))
  item_stem=${packet_path:t:r}
  output_path="$RAW_DIR/${item_stem}.txt"
  event_log="$LOG_DIR/${item_stem}.jsonl"
  stderr_log="$LOG_DIR/${item_stem}.stderr"
  item_worktree="$ACTOR_ROOT/grader/${completed_count}"
  test ! -e "$output_path"
  test ! -e "$event_log"
  test ! -e "$stderr_log"
  test ! -e "$item_worktree"
  mkdir -p "$item_worktree"
  git -C "$item_worktree" init -q

  if ! invoke_grader_once "$item_worktree" "$output_path" "$event_log" "$stderr_log" "$packet_path"; then
    grader_rc=1
    : > "$FAILURE_MARKER"
    print -r -- "grader_invocation_failure_at=$completed_count"
    break
  fi
  if ! test -s "$output_path" || ! audit_event_log "$event_log"; then
    grader_rc=1
    : > "$FAILURE_MARKER"
    print -r -- "grader_audit_failure_at=$completed_count"
    break
  fi
  if (( completed_count % 16 == 0 || completed_count == EXPECTED_PACKETS )); then
    print -r -- "grader_complete=$completed_count/$EXPECTED_PACKETS"
  fi
done < <(find "$PACKET_DIR" -type f -name '*.txt' -print | LC_ALL=C sort)

test "$grader_rc" = 0
test "$completed_count" = "$EXPECTED_PACKETS"
test ! -e "$FAILURE_MARKER"
print -r -- "grading_complete=$completed_count/$EXPECTED_PACKETS errors=0 retries=0 tool_events=0"
