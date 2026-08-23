# lib/util.rb — the shared service definitions consumed by app/.
#
# Module-level `def` declarations at the top level are collected as
# KindFunction by core/parse/parser_ruby.go:129; the QN is
# <posix_dir>.<bare> = `lib.core`, `lib.run`
# (core/parse/parser_tswalk.go:127). The file lives at lib/util.rb,
# so its directory basename is `lib` and the byDir["lib"] index
# records two NodeIds — one per top-level function.
#
# The fixture ships ONLY TWO methods (`core` and `run`) rather than
# three because the gotreesitter Ruby grammar mis-nests the THIRD
# of three top-level `def` blocks inside the second's body_statement
# (a known grammar bug). With two top-level `def` blocks the
# grammar is well-formed and both methods land in byDir["lib"].
# The cross-file scenarios pivot on `lib.run` (the function callers
# from app/ reach); `lib.salute` would have been a third method
# but is omitted to keep the grammar happy.

def core(name)
  name ? name : ""
end

def invoke(name)
  core(name)
end