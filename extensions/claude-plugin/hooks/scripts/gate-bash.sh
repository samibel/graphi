#!/bin/sh
# gate-bash.sh - PreToolUse gate for Claude Code's Bash tool.
# Blocks repo-wide symbol-shaped searches (grep -r, rg, ag, ack, git grep) when
# a graphi index exists and points the agent at graphi instead. This hook runs
# on EVERY Bash call, so the common case (no search tool in the command) exits
# after a pure-shell substring test, before anything is spawned.
#
# Conservative by design: anything it cannot parse confidently is allowed.

dir=$(dirname "$0")
# shellcheck source=shunt-lib.sh
. "$dir/shunt-lib.sh" 2>/dev/null || exit 0

shunt_read_input
[ -n "$SHUNT_INPUT" ] || exit 0

# Cheap prefilter on the raw payload: no candidate search tool, nothing to do.
case "$SHUNT_INPUT" in
*grep* | *rg* | *ag* | *ack*) ;;
*) exit 0 ;;
esac

shunt_enabled || exit 0

command=$(shunt_jq '.tool_input.command')
[ -n "$command" ] || exit 0
cwd=$(shunt_jq '.cwd')
if [ -n "$cwd" ] && [ -d "$cwd" ]; then
	cd "$cwd" 2>/dev/null || true
elif [ -d "$SHUNT_ROOT" ]; then
	cd "$SHUNT_ROOT" 2>/dev/null || true
fi

shunt_unquote() {
	case "$1" in
	\"*\") _u=${1#\"}; printf '%s' "${_u%\"}" ;;
	\'*\') _u=${1#\'}; printf '%s' "${_u%\'}" ;;
	*) printf '%s' "$1" ;;
	esac
}

# analyze_segment inspects ONE simple command. It exits the script with a deny
# decision for a repo-wide symbol search and returns normally otherwise.
analyze_segment() {
	_seg=$1
	# Only the first command of a pipeline can search the repo; a search that
	# reads from a pipe is just filtering. Substitutions / heredocs: give up.
	case "$_seg" in
	*'$('* | *'`'* | *'<<'*) return 0 ;;
	esac
	_seg=${_seg%%|*}

	set -f
	# shellcheck disable=SC2086
	set -- $_seg
	set +f
	[ "$#" -gt 0 ] || return 0

	# Skip leading VAR=value assignments.
	while [ "$#" -gt 0 ]; do
		case "$1" in
		[A-Za-z_]*=*) shift ;;
		*) break ;;
		esac
	done
	[ "$#" -gt 0 ] || return 0

	_tool=$(basename -- "$1" 2>/dev/null) || return 0
	shift
	_always_recursive=1
	case "$_tool" in
	rg | ag | ack | ack-grep) ;;
	grep | egrep | fgrep) _always_recursive=0 ;;
	git)
		[ "${1:-}" = "grep" ] || return 0
		shift
		;;
	*) return 0 ;;
	esac

	_recursive=$_always_recursive
	_pattern=""
	_have_pattern=0
	_operands=""
	_skip=0
	_next_is_pattern=0
	for _t in "$@"; do
		if [ "$_skip" -eq 1 ]; then
			_skip=0
			continue
		fi
		if [ "$_next_is_pattern" -eq 1 ]; then
			_pattern=$_t
			_have_pattern=1
			_next_is_pattern=0
			continue
		fi
		case "$_t" in
		*'>' | *'<') _skip=1; continue ;;
		*'>'* | *'<'*) continue ;;
		-e | --regexp) _next_is_pattern=1; continue ;;
		-g | -t | -T | -A | -B | -C | -m | -j | -d | -f | --glob | --type | --type-not | --max-count | --max-depth | --include | --exclude | --exclude-dir | --context | --after-context | --before-context)
			_skip=1
			[ "$_t" = "-d" ] && _recursive=1
			continue
			;;
		--recursive | --dereference-recursive) _recursive=1; continue ;;
		--*) continue ;;
		-*[rR]*) _recursive=1; continue ;;
		-*) continue ;;
		esac
		if [ "$_have_pattern" -eq 0 ]; then
			_pattern=$_t
			_have_pattern=1
		else
			_operands="$_operands
$_t"
		fi
	done
	[ "$_have_pattern" -eq 1 ] || return 0
	[ "$_recursive" -eq 1 ] || return 0

	# A multi-word quoted pattern is a text search.
	case "$_pattern" in
	\"*[!\"] | \'*[!\']) return 0 ;;
	esac
	_pattern=$(shunt_unquote "$_pattern")

	# Explicit operands: all existing files, or all in non-code trees => allow.
	if [ -n "$_operands" ]; then
		_old=$IFS
		IFS='
'
		_all_safe=1
		set -f
		for _o in $_operands; do
			[ -n "$_o" ] || continue
			_o=$(shunt_unquote "$_o")
			if [ -f "$_o" ] || shunt_noncode_path "$_o"; then
				continue
			fi
			_all_safe=0
			break
		done
		set +f
		IFS=$_old
		[ "$_all_safe" -eq 0 ] || return 0
	fi

	shunt_symbol_shaped "$_pattern" || return 0

	if shunt_seen_before "bash|$command"; then
		return 0
	fi
	shunt_deny "$_pattern" cli
}

# Split the command line on '&&', ';' and newlines and analyze each segment.
_old_ifs=$IFS
IFS='
'
set -f
_segments=$(printf '%s\n' "$command" | sed 's/&&/\
/g; s/;/\
/g')
for _s in $_segments; do
	IFS=$_old_ifs
	analyze_segment "$_s"
	IFS='
'
done
IFS=$_old_ifs
set +f
exit 0
