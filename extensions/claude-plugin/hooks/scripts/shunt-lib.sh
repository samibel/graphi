#!/bin/sh
# shunt-lib.sh - shared helpers for the graphi-shunt PreToolUse gates.
# Sourced by gate-grep.sh and gate-bash.sh; never executed on its own.
#
# Contract: every unexpected condition FAILS OPEN (the gate allows the tool
# call). A gate only blocks when graphi is installed AND an index for this
# repository exists AND the search looks like a symbol lookup of this repo.
#
# The store layout below mirrors internal/state (RepoRoot + StateDir +
# Fingerprint):
#   root    = nearest ancestor of the project dir holding .git, else go.work,
#             else go.mod, else the project dir itself
#   <state>/<fp>/db.sqlite   with  fp = first 16 hex of sha256(clean(root))
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

# shunt_clean_path strips trailing slashes (the part of filepath.Clean that
# matters for a path produced by a shell or an editor).
shunt_clean_path() {
	_c=$1
	while [ "${#_c}" -gt 1 ] && [ "${_c%/}" != "$_c" ]; do
		_c=${_c%/}
	done
	printf '%s' "$_c"
}

# shunt_repo_root prints the repository root for an absolute directory, with
# the walk order of internal/state.RepoRoot: the nearest ancestor holding a
# .git entry (dir or file); failing that go.work; failing that go.mod;
# otherwise the directory itself.
shunt_repo_root() {
	_start=$(shunt_clean_path "$1")
	for _marker in .git go.work go.mod; do
		_d=$_start
		while :; do
			if [ -e "$_d/$_marker" ]; then
				printf '%s' "$_d"
				return 0
			fi
			[ "$_d" != "/" ] || break
			_parent=${_d%/*}
			[ -n "$_parent" ] || _parent=/
			[ "$_parent" != "$_d" ] || break
			_d=$_parent
		done
	done
	printf '%s' "$_start"
}

# shunt_fingerprint prints the 16-hex store fingerprint of an absolute path,
# equivalent to internal/state.Fingerprint.
shunt_fingerprint() {
	_sum=$(printf '%s' "$(shunt_clean_path "$1")" | shunt_sha256) || return 1
	[ -n "$_sum" ] || return 1
	printf '%s' "$_sum" | cut -c1-16
}

# shunt_available succeeds when the gate may run at all: not disabled, and jq
# and graphi on PATH. Whether a search touches an indexed repository is decided
# per path by shunt_root_for.
shunt_available() {
	[ -z "${GRAPHI_SHUNT_DISABLE:-}" ] || return 1
	command -v jq >/dev/null 2>&1 || return 1
	command -v graphi >/dev/null 2>&1 || return 1
	return 0
}

shunt_state_dir() {
	if [ -n "${XDG_STATE_HOME:-}" ]; then
		printf '%s' "$XDG_STATE_HOME/graphi"
	elif [ -n "${HOME:-}" ]; then
		printf '%s' "$HOME/.graphi"
	else
		return 1
	fi
}

# shunt_index_at succeeds when graphi has an index for exactly the repository
# root $1, and exports that root as SHUNT_ROOT.
shunt_index_at() {
	_fp=$(shunt_fingerprint "$1") || return 1
	_state=$(shunt_state_dir) || return 1
	[ -f "$_state/$_fp/db.sqlite" ] || return 1
	SHUNT_ROOT=$(shunt_clean_path "$1")
	return 0
}

# shunt_root_for finds the indexed repository a path belongs to. The path is
# resolved against the current directory (a file counts as its directory, a
# path that does not exist as its nearest existing ancestor), the repo-root
# walk runs from there, and when that root has no index the walk continues
# from the root's parent - so a product clone that sits inside a portfolio
# repository (portfolio/.git, portfolio/workspace/<name>/.git) is found from
# either side. Succeeds with SHUNT_ROOT set, or fails when no enclosing
# repository has an index.
shunt_root_for() {
	_p=$1
	[ -n "$_p" ] || return 1
	case "$_p" in /*) ;; *) _p="$PWD/$_p" ;; esac
	_p=$(shunt_clean_path "$_p")
	while [ ! -d "$_p" ]; do
		[ "$_p" != "/" ] || return 1
		_p=${_p%/*}
		[ -n "$_p" ] || _p=/
	done
	_p=$(cd -- "$_p" 2>/dev/null && pwd) || return 1
	while :; do
		_root=$(shunt_repo_root "$_p")
		if shunt_index_at "$_root"; then
			return 0
		fi
		[ "$_root" != "/" ] || return 1
		_p=${_root%/*}
		[ -n "$_p" ] || _p=/
	done
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
		resources | resources/* | */resources | */resources/* | \
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

# shunt_stopword succeeds for lowercase words an agent greps as TEXT far more
# often than as a symbol: language keywords and common English words that
# happen to be six letters or longer. Anything here is allowed through.
shunt_stopword() {
	case "$1" in
	abstract | arguments | assert | author | boolean | breaking | callback | \
		catch | changed | changelog | changes | client | command | comment | comments | \
		compile | config | configs | console | constant | content | context | continue | \
		copyright | create | created | current | custom | database | debug | declare | \
		default | defer | define | delete | deleted | deprecated | describe | \
		description | disable | disabled | docker | document | double | elseif | \
		enable | enabled | endpoint | environment | errors | example | examples | \
		except | exception | expected | export | exports | extends | extern | failed | \
		failure | fatal | feature | finally | fixture | fixtures | footer | foreach | \
		format | function | global | handle | handler | header | headers | import | \
		imports | include | includes | inline | input | insert | install | instance | \
		integer | interface | internal | invalid | lambda | length | license | \
		listen | loading | logger | logging | message | messages | method | missing | \
		module | modules | namespace | native | nothing | notice | number | object | \
		offset | option | options | output | override | package | packages | params | \
		parent | partial | password | pending | private | process | protected | public | \
		readme | readonly | reason | record | remove | removed | render | replace | \
		request | require | required | resolve | response | result | results | return | \
		returns | runtime | schema | script | search | secret | select | server | \
		service | session | setting | settings | should | signal | signed | single | \
		skipped | source | sources | static | status | storage | stream | string | \
		struct | submit | success | summary | support | switch | symbol | syntax | \
		target | template | testing | thread | throws | timeout | typedef | \
		unknown | unsafe | update | updated | upgrade | upload | usage | username | \
		utility | values | verbose | version | virtual | volatile | warning | warnings | \
		window | without | yield)
		return 0
		;;
	esac
	return 1
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
		[ "${#_p}" -ge 6 ] || return 1
		shunt_stopword "$_p" && return 1
		return 0
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
# seen in a session it is recorded and the gate blocks; the identical later
# attempt is allowed, with no time limit. The ledger keeps the last 200
# signatures per session. If it cannot be written the gate allows (fail open),
# so an agent can never be trapped by an unwritable temp directory.
shunt_seen_before() {
	_sig=$(printf '%s' "$1" | shunt_sha256) || return 0
	[ -n "$_sig" ] || return 0
	_sid=$(printf '%s' "$SHUNT_INPUT" | jq -j '.session_id // "nosession"' 2>/dev/null | tr -c 'A-Za-z0-9_-' '_')
	[ -n "$_sid" ] || _sid=nosession
	_ledger="${TMPDIR:-/tmp}/graphi-shunt-$_sid.log"
	if [ -f "$_ledger" ] && awk -v s="$_sig" '$2 == s { f = 1 } END { exit !f }' "$_ledger" 2>/dev/null; then
		return 0
	fi
	_now=$(date +%s) || return 0
	if ! (umask 077 && printf '%s %s\n' "$_now" "$_sig" >>"$_ledger") 2>/dev/null; then
		return 0
	fi
	(
		umask 077
		_tmp="$_ledger.$$"
		tail -n 200 "$_ledger" >"$_tmp" 2>/dev/null && mv "$_tmp" "$_ledger" 2>/dev/null
		rm -f "$_tmp" 2>/dev/null
	)
	return 1
}

# shunt_deny prints the PreToolUse deny decision and exits 0.
#   $1 = the offending pattern, $2 = "cli" to lead with shell commands.
shunt_deny() {
	_pat=$1
	_mode=${2:-tool}
	_mcp="Use the graphi MCP tools (the server registered for that checkout, if several are): search (find the symbol), definition, callers / callees, references (who uses it) and impact (blast radius); for a whole task start with agent_brief, then related_files."
	_cli="From the shell, run inside that checkout: cd \"$SHUNT_ROOT\" && graphi search \"$_pat\" (likewise graphi references / callers / impact \"$_pat\")."
	if [ "$_mode" = "cli" ]; then
		_how="$_cli $_mcp"
	else
		_how="$_mcp $_cli"
	fi
	_reason="graphi-shunt: \"$_pat\" looks like a symbol lookup, and the repository at $SHUNT_ROOT has a graphi code-graph index that answers it with file:line citations and confidence tiers instead of raw text matches. $_how If graphi returns empty / ambiguous / unavailable, or you really need a literal-text match, repeat the exact same search once more - the second identical attempt is allowed. Set GRAPHI_SHUNT_DISABLE=1 to turn this gate off."
	jq -n --arg r "$_reason" '{hookSpecificOutput: {hookEventName: "PreToolUse", permissionDecision: "deny", permissionDecisionReason: $r}}' 2>/dev/null || exit 0
	exit 0
}
