#!/bin/sh
# gate-grep.sh - PreToolUse gate for Claude Code's Grep tool.
# Blocks repo-wide symbol-shaped searches inside an indexed repository and
# points the agent at graphi's tools instead. Everything else is allowed
# (fail open).

dir=$(dirname "$0")
# shellcheck source=shunt-lib.sh
. "$dir/shunt-lib.sh" 2>/dev/null || exit 0

shunt_read_input
[ -n "$SHUNT_INPUT" ] || exit 0
shunt_available || exit 0

pattern=$(shunt_jq '.tool_input.pattern')
[ -n "$pattern" ] || exit 0
shunt_symbol_shaped "$pattern" || exit 0
path=$(shunt_jq '.tool_input.path')
glob=$(shunt_jq '.tool_input.glob')
type=$(shunt_jq '.tool_input.type')

if [ -n "$type" ] && shunt_noncode_ext "$type"; then
	exit 0
fi
if [ -n "$glob" ] && shunt_noncode_glob "$glob"; then
	exit 0
fi

# Relative paths in the payload are relative to the session's cwd, not to the
# directory the hook happens to run in.
cwd=$(shunt_jq '.cwd')
if [ -z "$cwd" ] || [ ! -d "$cwd" ]; then
	cwd=${CLAUDE_PROJECT_DIR:-}
fi
if [ -n "$cwd" ] && [ -d "$cwd" ]; then
	cd "$cwd" 2>/dev/null || true
fi

if [ -n "$path" ]; then
	# A targeted read of one file is not exploration.
	[ -f "$path" ] && exit 0
	shunt_noncode_path "$path" && exit 0
	# The searched tree must belong to an indexed repository (which may be a
	# clone nested inside the session's own repository).
	shunt_root_for "$path" || exit 0
else
	shunt_root_for "$PWD" || exit 0
fi

if shunt_seen_before "grep|$pattern|$path|$glob|$type"; then
	exit 0
fi

shunt_deny "$pattern" tool
