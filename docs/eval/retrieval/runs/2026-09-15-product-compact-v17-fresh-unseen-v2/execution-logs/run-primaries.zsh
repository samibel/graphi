#!/bin/zsh
set -eu
setopt pipefail

run_dir=/private/tmp/graphi-compact17-unseen-operator/docs/eval/retrieval/runs/2026-09-15-product-compact-v17-fresh-unseen-v2
workspace_root=/private/tmp/graphi-c17u2-primary-workspaces
failure_marker=/private/tmp/graphi-c17u2-primary-failed

test ! -e "$failure_marker"
test ! -e "$run_dir/responses-raw"
test ! -e "$run_dir/execution-logs/primary-a"
test ! -e "$run_dir/execution-logs/primary-b"
mkdir -p "$run_dir/responses-raw" "$run_dir/execution-logs/primary-a" "$run_dir/execution-logs/primary-b" "$workspace_root/primary-a" "$workspace_root/primary-b"

audit_log() {
  local log=$1
  jq -s -e '
    length == 4 and
    .[0].type == "thread.started" and
    .[1].type == "turn.started" and
    .[2].type == "item.completed" and
    .[2].item.type == "agent_message" and
    .[3].type == "turn.completed"
  ' "$log" >/dev/null
}

run_role() {
  local slot=$1
  local model=$2
  local rater_id=$3
  local count=0
  local prompt stem raw log stderr_file item_work

  while IFS= read -r prompt; do
    if test -e "$failure_marker"; then
      return 1
    fi
    count=$((count + 1))
    stem=${prompt:t:r}
    raw="$run_dir/responses-raw/${stem}--${rater_id}.txt"
    log="$run_dir/execution-logs/${slot}/${stem}.jsonl"
    stderr_file="$run_dir/execution-logs/${slot}/${stem}.stderr"
    item_work="$workspace_root/${slot}/${count}"
    test ! -e "$raw"
    test ! -e "$log"
    test ! -e "$stderr_file"
    test ! -e "$item_work"
    mkdir -p "$item_work"
    git -C "$item_work" init -q
    if ! codex exec --ephemeral --ignore-user-config --ignore-rules -s read-only -m "$model" -c 'model_reasoning_effort="high"' -C "$item_work" --json -o "$raw" - < "$prompt" > "$log" 2> "$stderr_file"; then
      : > "$failure_marker"
      print -r -- "primary_${slot}_error_at=$count"
      return 1
    fi
    if ! test -s "$raw" || ! audit_log "$log"; then
      : > "$failure_marker"
      print -r -- "primary_${slot}_event_or_output_error_at=$count"
      return 1
    fi
    if (( count % 8 == 0 )); then
      print -r -- "primary_${slot}_complete=$count/64"
    fi
  done < <(find "$run_dir/prompts" -type f -name '*.txt' -print | LC_ALL=C sort)
  test "$count" = 64
}

run_role primary-a gpt-6-astra unseen-v2-primary-a--gpt-6-astra--high--codex-cli-0.153.4 &
pid_a=$!
run_role primary-b gpt-5.6-sol unseen-v2-primary-b--gpt-5.6-sol--high--codex-cli-0.153.4 &
pid_b=$!

status=0
wait "$pid_a" || status=1
wait "$pid_b" || status=1
test "$status" = 0
test ! -e "$failure_marker"
print -r -- "primaries_complete=128/128 errors=0 retries=0 tool_events=0"
