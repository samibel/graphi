# lib/util.sh — the canonical helper module.
#
# QN keys on `<dir>.<funcname>` (parser_bashwalk.go's langPackage key), so
# `lib/util.sh` -> package `lib`. Functions here are `lib.hello`, `lib.shout`.
# `summarize` is intentionally NOT defined here — it lives only in serve.sh,
# where main() calls it four times. That keeps `summarize` a single-candidate
# symbol so `explain_symbol(summarize, max_items=1)` marks the result partial
# via Limits.Truncated (the bash parallel of the JVM twin's
# hjvm-15-explain-partial.yaml).
#
# Per docs/plan/2026-08-per-language-ga-template-v1.md §5.5, bash has NO
# cross-file construct in its language specification (the `source` builtin
# is a shell execution mechanism, not a script-level language construct;
# bash defines no file-inclusion construct at the language level). Graphi's
# bashResolver at engine/link/resolve_bash.go DOES model `source ./util.sh`
# as a file->file imports edge, but the language-spec test in §5.5 says:
# bash has no askable cross-file relation. The hero fixture intentionally
# AVOIDS `source` so the bashResolver's cross-file machinery is never
# invoked — cross-file operations genuinely return empty.

hello() {
  local name="$1"
  echo "hello $name"
}

shout() {
  local name="$1"
  echo "HI $name"
}
