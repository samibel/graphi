#!/usr/bin/env bash
# serve.sh — the entry-point script.
#
# bash has no cross-file construct per docs/plan/2026-08-per-language-ga-
# template-v1.md §5.5 (the language-spec test). The cross-file operations
# (callers/callees/references/impact/related_files across files) all
# return well-formed empty outcomes — that's the parse-determinism
# honest-empty discipline AC-4 binds.
#
# `summarize` is defined here and called FOUR TIMES by `main()`. That
# call-graph populates the items list for `explain_symbol(summarize,
# max_items=1)` to trigger `Limits.Truncated`, marking the result partial
# (the bash parallel of the JVM twin's hjvm-15-explain-partial.yaml).
#
# `init` is defined here AND in report.sh — that duplication is what gives
# `callers(init)` its ambiguous outcome.

summarize() {
  local label="$1"
  local who="$2"
  echo "summary ($label) for $who"
}

main() {
  local who="$1"
  echo "main says hello to $who"
  summarize "first"  "$who"
  summarize "second" "$who"
  summarize "third"  "$who"
  summarize "fourth" "$who"
}

run() {
  main "$@"
}

init() {
  echo "init from serve.sh"
}

if [[ "${BASH_SOURCE[0]}" == "${0}" ]]; then
  run "$@"
fi
