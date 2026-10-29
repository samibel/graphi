#!/bin/sh
# shunt-lib.sh - shared helpers for the graphi-shunt PreToolUse gates.
# Sourced by gate-grep.sh and gate-bash.sh; never executed on its own.
#
# Contract: every unexpected condition FAILS OPEN (the gate allows the tool
# call). A gate only blocks when graphi is installed AND an index for this
# repository exists AND the search looks like a symbol lookup.
#
# The store layout below mirrors internal/state (StateDir + Fingerprint):
#   <state>/<fp>/db.sqlite   with  fp = first 16 hex of sha256(clean(repo root))
#   <state> = $XDG_STATE_HOME/graphi, else $HOME/.graphi
# extensions/claude-plugin/validate pins this copy to the Go source of truth.

SHUNT_INPUT=""
SHUNT_ROOT=""

shunt_read_input() {
	SHUNT_INPUT=$(cat) || SHUNT_INPUT=""
}

# shunt_sha256 hashes stdin and prints the hex digest.
shunt_sha256() {
	if command -v sha256sum >/dev/null 2>&1; then
		sha256sum | cut -d' ' -f1
	elif command -v shasum >/dev/null 2>&1; then
		shasum -a 256 | cut -d' ' -f1
	else
		return 1
	fi
}

# shunt_fingerprint prints the 16-hex store fingerprint of an absolute path,
# equivalent to internal/state.Fingerprint (trailing slashes cleaned).
shunt_fingerprint() {
	_root=$1
	while [ "${#_root}" -gt 1 ] && [ "${_root%/}" != "$_root" ]; do
		_root=${_root%/}
	done
	_sum=$(printf '%s' "$_root" | shunt_sha256) || return 1
	[ -n "$_sum" ] || return 1
	printf '%s' "$_sum" | cut -c1-16
}

# shunt_enabled succeeds only when the gate should be active for this call.
shunt_enabled() {
	[ -z "${GRAPHI_SHUNT_DISABLE:-}" ] || return 1
	command -v jq >/dev/null 2>&1 || return 1
	command -v graphi >/dev/null 2>&1 || return 1
	_root=${CLAUDE_PROJECT_DIR:-}
	if [ -z "$_root" ]; then
		_root=$(git rev-parse --show-toplevel 2>/dev/null) || return 1
	fi
	[ -n "$_root" ] || return 1
	_fp=$(shunt_fingerprint "$_root") || return 1
	if [ -n "${XDG_STATE_HOME:-}" ]; then
		_state="$XDG_STATE_HOME/graphi"
	elif [ -n "${HOME:-}" ]; then
		_state="$HOME/.graphi"
	else
		return 1
	fi
	[ -f "$_state/$_fp/db.sqlite" ] || return 1
	SHUNT_ROOT=$_root
	return 0
}

# shunt_jq prints one field of the hook input ("" when absent).
shunt_jq() {
	printf '%s' "$SHUNT_INPUT" | jq -r "$1 // empty" 2>/dev/null
}

# shunt_noncode_path succeeds when a path points into a directory graphi does
# not index as code (docs, fixtures, vendored trees, VCS metadata).
shunt_noncode_path() {
	case "$1" in
	docs | docs/* | */docs | */docs/* | \
		site | site/* | */site | */site/* | \
		corpus | corpus/* | */corpus | */corpus/* | \
		testdata | testdata/* | */testdata | */testdata/* | \
		node_modules | node_modules/* | */node_modules | */node_modules/* | \
		.git | .git/* | */.git | */.git/*)
		return 0
		;;
	esac
	return 1
}

# shunt_noncode_ext succeeds when one extension / type name is not source code.
shunt_noncode_ext() {
	case "$1" in
	md | markdown | txt | json | yaml | yml | toml | csv | lock | sum | mod | \
		xml | ini | cfg | conf | html | css | svg | log | rst)
		return 0
		;;
	esac
	return 1
}

# shunt_noncode_glob succeeds when a glob such as "*.md" or "*.{json,yaml}"
# only selects non-code extensions.
shunt_noncode_glob() {
	_g=$1
	case "$_g" in *.*) ;; *) return 1 ;; esac
	_exts=$(printf '%s' "${_g##*.}" | tr -d '{}' | tr ',' ' ')
	[ -n "$_exts" ] || return 1
	for _e in $_exts; do
		shunt_noncode_ext "$_e" || return 1
	done
	return 0
}

# shunt_symbol_shaped succeeds when a search pattern looks like a symbol name
# (identifier, qualified name, definition probe or call probe) rather than a
# text search. Deliberately conservative: when unsure, it says "not a symbol"
# so the search is allowed.
shunt_symbol_shaped() {
	_p=$1
	# Strip ripgrep word-boundary escapes: \bFoo\b -> Foo
	_p=${_p#\\b}
	_p=${_p%\\b}
	[ -n "$_p" ] || return 1

	# Bare identifier.
	if printf '%s\n' "$_p" | grep -Eq '^[A-Za-z_][A-Za-z0-9_]*$'; then
		[ "${#_p}" -ge 3 ] || return 1
		# TODO / FIXME / HACK style markers are text, not symbols.
		if printf '%s\n' "$_p" | grep -Eq '^[A-Z0-9]+$'; then
			return 1
		fi
		case "$_p" in *_*) return 0 ;; esac
		if printf '%s\n' "$_p" | grep -Eq '[a-z]' &&
			printf '%s\n' "$_p" | grep -Eq '[A-Z]'; then
			return 0
		fi
		[ "${#_p}" -ge 6 ] && return 0
		return 1
	fi

	# Qualified name: pkg.Func, pkg::Func, obj->field. File names are not symbols.
	if printf '%s\n' "$_p" | grep -Eq '^[A-Za-z_][A-Za-z0-9_]*((\.|::|->)[A-Za-z_][A-Za-z0-9_]*)+$'; then
		_last=${_p##*[.:>]}
		if shunt_noncode_ext "$_last"; then
			return 1
		fi
		case "$_last" in go | js | ts | tsx | jsx | py | rs | java | kt | rb | sh | c | h | cc | cpp | hpp | cs | php | swift) return 1 ;; esac
		return 0
	fi

	# Definition probe: func Foo, class Foo, func (r *T) Foo
	if printf '%s\n' "$_p" | grep -Eq '^(func|def|class|type|interface|struct|fn)[[:space:]]+(\([^)]*\)[[:space:]]*)?[A-Za-z_][A-Za-z0-9_]*'; then
		return 0
	fi

	# Call probe: Foo\(  /  pkg.Foo\(
	if printf '%s\n' "$_p" | grep -Eq '^[A-Za-z_][A-Za-z0-9_.]*\\\($'; then
		return 0
	fi
	return 1
}

# shunt_seen_before implements the retry ledger: the first time a signature is
# seen it is recorded and the gate blocks; the identical second attempt is
# allowed. If the ledger cannot be written the gate allows (fail open), so an
# agent can never be trapped by an unwritable temp directory.
shunt_seen_before() {
	_sig=$(printf '%s' "$1" | shunt_sha256) || return 0
	[ -n "$_sig" ] || return 0
	_sid=$(printf '%s' "$SHUNT_INPUT" | jq -j '.session_id // "nosession"' 2>/dev/null | tr -c 'A-Za-z0-9_-' '_')
	[ -n "$_sid" ] || _sid=nosession
	_dir=${TMPDIR:-/tmp}
	_ledger="$_dir/graphi-shunt-$_sid.log"
	_now=$(date +%s) || return 0
	(
		umask 077
		if [ -f "$_ledger" ]; then
			_tmp="$_ledger.$$"
			awk -v now="$_now" 'now - $1 <= 900' "$_ledger" 2>/dev/null | tail -n 200 >"$_tmp" &&
				mv "$_tmp" "$_ledger" 2>/dev/null
			rm -f "$_tmp" 2>/dev/null
		fi
	)
	if [ -f "$_ledger" ] && awk -v s="$_sig" '$2 == s { f = 1 } END { exit !f }' "$_ledger" 2>/dev/null; then
		return 0
	fi
	if ! (umask 077 && printf '%s %s\n' "$_now" "$_sig" >>"$_ledger") 2>/dev/null; then
		return 0
	fi
	return 1
}

# shunt_deny prints the PreToolUse deny decision and exits 0.
#   $1 = the offending pattern, $2 = "cli" to lead with shell commands.
shunt_deny() {
	_pat=$1
	_mode=${2:-tool}
	_mcp="Use the graphi MCP tools: search (find the symbol), definition, callers / callees, references (who uses it) and impact (blast radius); for a whole task start with agent_brief, then related_files."
	_cli="From the shell the same answers are 'graphi search \"$_pat\"', 'graphi references \"$_pat\"', 'graphi callers \"$_pat\"' and 'graphi impact \"$_pat\"'."
	if [ "$_mode" = "cli" ]; then
		_how="$_cli $_mcp"
	else
		_how="$_mcp $_cli"
	fi
	_reason="graphi-shunt: \"$_pat\" looks like a symbol lookup, and this repository has a graphi code-graph index that answers it with file:line citations and confidence tiers instead of raw text matches. $_how If graphi returns empty / ambiguous / unavailable, or you really need a literal-text match, repeat the exact same search once more - the second identical attempt is allowed. Set GRAPHI_SHUNT_DISABLE=1 to turn this gate off."
	jq -n --arg r "$_reason" '{hookSpecificOutput: {hookEventName: "PreToolUse", permissionDecision: "deny", permissionDecisionReason: $r}}' 2>/dev/null || exit 0
	exit 0
}
