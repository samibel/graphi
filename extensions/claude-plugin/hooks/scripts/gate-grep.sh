#!/bin/sh
# gate-grep.sh - PreToolUse gate for Claude Code's Grep tool.
# Blocks repo-wide symbol-shaped searches when a graphi index exists and points
# the agent at graphi's tools instead. Everything else is allowed (fail open).

dir=$(dirname "$0")
# shellcheck source=shunt-lib.sh
. "$dir/shunt-lib.sh" 2>/dev/null || exit 0

shunt_read_input
[ -n "$SHUNT_INPUT" ] || exit 0
shunt_enabled || exit 0

pattern=$(shunt_jq '.tool_input.pattern')
[ -n "$pattern" ] || exit 0
path=$(shunt_jq '.tool_input.path')
glob=$(shunt_jq '.tool_input.glob')
type=$(shunt_jq '.tool_input.type')

# Relative paths in the payload are relative to the session's cwd, not to the
# directory the hook happens to run in.
cwd=$(shunt_jq '.cwd')
if [ -n "$cwd" ] && [ -d "$cwd" ]; then
	cd "$cwd" 2>/dev/null || true
elif [ -d "$SHUNT_ROOT" ]; then
	cd "$SHUNT_ROOT" 2>/dev/null || true
fi

if [ -n "$path" ]; then
	# A targeted read of one file is not exploration.
	[ -f "$path" ] && exit 0
	shunt_noncode_path "$path" && exit 0
	# Only the indexed repository is graphi's domain.
	shunt_in_root "$path" || exit 0
fi
if [ -n "$type" ] && shunt_noncode_ext "$type"; then
	exit 0
fi
if [ -n "$glob" ] && shunt_noncode_glob "$glob"; then
	exit 0
fi

shunt_symbol_shaped "$pattern" || exit 0

if shunt_seen_before "grep|$pattern|$path|$glob|$type"; then
	exit 0
fi

shunt_deny "$pattern" tool
