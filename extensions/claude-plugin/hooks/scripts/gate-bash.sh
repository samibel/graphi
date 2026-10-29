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

# Cheap prefilter without spawning anything: slice the command text out of the
# raw JSON (from the "command" key up to the "description" key that follows it
# in a Bash tool payload) and look for a search tool in it. The slice is
# deliberately rough - a false positive only costs one jq run below, and a
# false negative needs the literal text `,"description":` inside the command.
cmdslice=${SHUNT_INPUT#*\"command\":}
cmdslice=${cmdslice%%,\"description\":*}
case "$cmdslice" in
*grep* | *rg\ * | *ag\ * | *ack\ *) ;;
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

# shunt_join_quotes re-joins whitespace-split tokens that belong to one quoted
# word ("func HandleRequest" -> one token) and prints them one per line.
# Fails (returns 1) on an unbalanced quote, which callers treat as allow.
shunt_join_quotes() {
	_open=""
	_acc=""
	for _t in "$@"; do
		if [ -n "$_open" ]; then
			_acc="$_acc $_t"
			case "$_t" in
			*"$_open") printf '%s\n' "$_acc"; _open=""; _acc="" ;;
			esac
			continue
		fi
		case "$_t" in
		\"?*\" | \'?*\') printf '%s\n' "$_t"; continue ;;
		\"*) _open='"'; _acc=$_t; continue ;;
		\'*) _open="'"; _acc=$_t; continue ;;
		esac
		printf '%s\n' "$_t"
	done
	[ -z "$_open" ]
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
	_family=""
	case "$_tool" in
	rg) _family=rg ;;
	ag) _family=ag ;;
	ack | ack-grep) _family=ack ;;
	grep | egrep | fgrep) _family=grep ;;
	git)
		[ "${1:-}" = "grep" ] || return 0
		shift
		_family=gitgrep
		;;
	*) return 0 ;;
	esac

	_tokens=$(shunt_join_quotes "$@") || return 0
	_old_ifs=$IFS
	IFS='
'
	set -f
	# shellcheck disable=SC2086
	set -- $_tokens
	set +f
	IFS=$_old_ifs

	# grep needs -r to leave a single file; the others always walk a tree.
	_recursive=1
	[ "$_family" != "grep" ] || _recursive=0
	_pattern=""
	_have_pattern=0
	_operands=""
	_skip=0
	_next_is_pattern=0
	_end_opts=0
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
		if [ "$_end_opts" -eq 0 ]; then
			case "$_t" in
			--) _end_opts=1; continue ;;
			*'>' | *'<') _skip=1; continue ;;
			*'>'* | *'<'*) continue ;;
			-e | --regexp | --match) _next_is_pattern=1; continue ;;
			# Options that take a separate value, across grep / rg / ag / ack.
			-A | -B | -C | -m | -f | -g | -t | -T | -j | -d | -D | -G | -p | \
				--after-context | --before-context | --context | --max-count | \
				--file | --glob | --iglob | --type | --type-not | --type-add | \
				--type-clear | --max-depth | --threads | --include | --exclude | \
				--exclude-dir | --exclude-from | --label | --devices | --directories | \
				--binary-files | --group-separator | --sort | --sortr | --color | \
				--colors | --colour | --engine | --encoding | --max-columns | \
				--max-filesize | --pre | --pre-glob | --ignore-file | \
				--context-separator | --field-context-separator | \
				--field-match-separator | --path-separator | --regex-size-limit | \
				--dfa-size-limit | --ignore | --ignore-dir | --path-to-ignore | \
				--pager | --color-match | --color-filename | --color-lineno)
				_skip=1
				continue
				;;
			--recursive | --dereference-recursive) _recursive=1; continue ;;
			--*) continue ;;
			esac
			case "$_family" in
			rg)
				# rg: -r/--replace, -E/--encoding and -M/--max-columns take values.
				case "$_t" in
				-r | -E | -M | --replace) _skip=1; continue ;;
				-*) continue ;;
				esac
				;;
			grep)
				case "$_t" in
				-*[rR]*) _recursive=1; continue ;;
				-*) continue ;;
				esac
				;;
			*)
				case "$_t" in -*) continue ;; esac
				;;
			esac
		fi
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
	_pattern=$(shunt_unquote "$_pattern")

	# Operands: the search is repo-wide only if it names at least one existing
	# directory inside the indexed repository and nothing else. Files, non-code
	# trees and paths outside the repo are not graphi's domain; globs and paths
	# that do not exist (from this hook's view) cannot be judged. Any of those
	# allows the command.
	if [ -n "$_operands" ]; then
		_repo_dir=0
		_old_ifs=$IFS
		IFS='
'
		set -f
		for _o in $_operands; do
			[ -n "$_o" ] || continue
			_o=$(shunt_unquote "$_o")
			case "$_o" in *[\*\?\[]*)
				_repo_dir=0
				break
				;;
			esac
			if [ -f "$_o" ] || shunt_noncode_path "$_o"; then
				continue
			fi
			if [ -d "$_o" ] && shunt_in_root "$_o"; then
				_repo_dir=1
				continue
			fi
			_repo_dir=0
			break
		done
		set +f
		IFS=$_old_ifs
		[ "$_repo_dir" -eq 1 ] || return 0
	fi

	shunt_symbol_shaped "$_pattern" || return 0

	if shunt_seen_before "bash|$command"; then
		return 0
	fi
	shunt_deny "$_pattern" cli
}

# Split the command line on '&&', '||', ';' and newlines and analyze each
# segment in turn. A `cd` segment moves this process too, so later relative
# operands resolve the way they will for the agent.
_old_ifs=$IFS
IFS='
'
set -f
_segments=$(printf '%s\n' "$command" | sed 's/&&/\
/g; s/||/\
/g; s/;/\
/g')
for _s in $_segments; do
	IFS=$_old_ifs
	_lead=${_s#"${_s%%[![:space:]]*}"}
	case "$_lead" in
	cd)
		cd "${HOME:-/}" 2>/dev/null || true
		;;
	cd\ *)
		_target=${_lead#cd }
		_target=${_target#"${_target%%[![:space:]]*}"}
		_target=$(shunt_unquote "${_target%%[[:space:]]*}")
		cd -- "$_target" 2>/dev/null || true
		;;
	*)
		analyze_segment "$_s"
		;;
	esac
	IFS='
'
done
IFS=$_old_ifs
set +f
exit 0
