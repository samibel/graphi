#!/bin/zsh
set -eu
setopt pipefail

# Required, resolved by the operator from the next sealed handoff.
: ${RUN_DIR:?RUN_DIR is required}
: ${PROMPT_DIR:?PROMPT_DIR is required}
: ${ACTOR_ROOT:?ACTOR_ROOT is required}
: ${EXPECTED_ITEMS:?EXPECTED_ITEMS is required}

# Frozen logical identities. Override only before freeze, never during a run.
PRIMARY_A_MODEL=${PRIMARY_A_MODEL:-gpt-6-astra}
PRIMARY_A_ID=${PRIMARY_A_ID:-next-primary-a--gpt-6-astra--high--codex-cli-0.153.4}
PRIMARY_B_MODEL=${PRIMARY_B_MODEL:-gpt-5.6-sol}
PRIMARY_B_ID=${PRIMARY_B_ID:-next-primary-b--gpt-5.6-sol--high--codex-cli-0.153.4}
ACTOR_MODE=${ACTOR_MODE:-live}
SMOKE_FAIL_SLOT=${SMOKE_FAIL_SLOT:-none}

FAILURE_MARKER="$ACTOR_ROOT/operator-failed"
RAW_DIR="$RUN_DIR/responses-raw"
LOG_ROOT="$RUN_DIR/execution-logs"

test "$EXPECTED_ITEMS" -gt 0
test -d "$RUN_DIR"
test -d "$PROMPT_DIR"
test ! -e "$FAILURE_MARKER"
test ! -e "$RAW_DIR"
test ! -e "$LOG_ROOT/primary-a"
test ! -e "$LOG_ROOT/primary-b"

prompt_count=$(find "$PROMPT_DIR" -type f -name '*.txt' -print | wc -l | tr -d ' ')
test "$prompt_count" = "$EXPECTED_ITEMS"
mkdir -p "$RAW_DIR" "$LOG_ROOT/primary-a" "$LOG_ROOT/primary-b" "$ACTOR_ROOT/primary-a" "$ACTOR_ROOT/primary-b"

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

invoke_actor_once() {
  local slot_name=$1
  local model_name=$2
  local item_worktree=$3
  local output_path=$4
  local event_log=$5
  local stderr_log=$6
  local input_path=$7

  case "$ACTOR_MODE" in
    live)
      codex exec --ephemeral --ignore-user-config --ignore-rules -s read-only \
        -m "$model_name" -c 'model_reasoning_effort="high"' \
        -C "$item_worktree" --json -o "$output_path" - \
        < "$input_path" > "$event_log" 2> "$stderr_log"
      ;;
    smoke)
      if test "$SMOKE_FAIL_SLOT" = "$slot_name"; then
        return 73
      fi
      print -r -- 'synthetic response' > "$output_path"
      {
        print -r -- '{"type":"thread.started"}'
        print -r -- '{"type":"turn.started"}'
        print -r -- '{"type":"item.completed","item":{"type":"agent_message","text":"synthetic response"}}'
        print -r -- '{"type":"turn.completed"}'
      } > "$event_log"
      : > "$stderr_log"
      ;;
    *)
      return 64
      ;;
  esac
}

run_role_serially() {
  local slot_name=$1
  local model_name=$2
  local rater_identity=$3
  local completed_count=0
  local input_path item_stem output_path event_log stderr_log item_worktree

  while IFS= read -r input_path; do
    test ! -e "$FAILURE_MARKER" || return 1
    completed_count=$((completed_count + 1))
    item_stem=${input_path:t:r}
    output_path="$RAW_DIR/${item_stem}--${rater_identity}.txt"
    event_log="$LOG_ROOT/${slot_name}/${item_stem}.jsonl"
    stderr_log="$LOG_ROOT/${slot_name}/${item_stem}.stderr"
    item_worktree="$ACTOR_ROOT/${slot_name}/${completed_count}"
    test ! -e "$output_path"
    test ! -e "$event_log"
    test ! -e "$stderr_log"
    test ! -e "$item_worktree"
    mkdir -p "$item_worktree"
    git -C "$item_worktree" init -q

    if ! invoke_actor_once "$slot_name" "$model_name" "$item_worktree" "$output_path" "$event_log" "$stderr_log" "$input_path"; then
      : > "$FAILURE_MARKER"
      print -r -- "primary_${slot_name}_invocation_failure_at=$completed_count"
      return 1
    fi
    if ! test -s "$output_path" || ! audit_event_log "$event_log"; then
      : > "$FAILURE_MARKER"
      print -r -- "primary_${slot_name}_audit_failure_at=$completed_count"
      return 1
    fi
    if (( completed_count % 8 == 0 || completed_count == EXPECTED_ITEMS )); then
      print -r -- "primary_${slot_name}_complete=$completed_count/$EXPECTED_ITEMS"
    fi
  done < <(find "$PROMPT_DIR" -type f -name '*.txt' -print | LC_ALL=C sort)
  test "$completed_count" = "$EXPECTED_ITEMS"
}

typeset -i primary_a_rc=0
typeset -i primary_b_rc=0
typeset -i operator_rc=0
typeset primary_a_pid=''
typeset primary_b_pid=''

stop_children() {
  : > "$FAILURE_MARKER"
  test -z "${primary_a_pid:-}" || kill "$primary_a_pid" 2>/dev/null || true
  test -z "${primary_b_pid:-}" || kill "$primary_b_pid" 2>/dev/null || true
}
trap stop_children HUP INT TERM

run_role_serially primary-a "$PRIMARY_A_MODEL" "$PRIMARY_A_ID" &
primary_a_pid=$!
run_role_serially primary-b "$PRIMARY_B_MODEL" "$PRIMARY_B_ID" &
primary_b_pid=$!

wait "$primary_a_pid" || primary_a_rc=$?
wait "$primary_b_pid" || primary_b_rc=$?
if (( primary_a_rc != 0 || primary_b_rc != 0 )); then
  operator_rc=1
fi
test "$operator_rc" = 0
test ! -e "$FAILURE_MARKER"
print -r -- "primaries_complete=$((EXPECTED_ITEMS * 2))/$((EXPECTED_ITEMS * 2)) errors=0 retries=0 tool_events=0"
