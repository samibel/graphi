# app/main.rb — the cross-file caller of the shared service.
#
# `require_relative '../lib/util'` is the cross-file construct: the
# rubyResolver's requireBinder (engine/link/resolve_ruby.go,
# resolve_common.go:332) joins `'../lib/util'` against this file's
# directory (`app/`) to produce `lib/util.rb`, recording it as a
# file→file `imports` edge AND adding `lib` (the posixDir of the
# joined path) to the ambient lookup dirs. Bare `core(...)` /
# `run(...)` references below then resolve through the ambient-dir
# lookup to lib.core / lib.run at the heuristic tier.
#
# `app.entry` is the module-level QN for `def entry` here, distinct
# from the `app.init` collision below (defined twice in app/ — once
# in main.rb and once in other.rb — which makes init ambiguous by
# directory, mirroring the ccpp twin's init_c / init_cpp shape).
#
# `other_salute` is a NEGATIVE ANCHOR: it never calls core, so
# callers(lib.core) MUST NOT surface app.other_salute.

require_relative '../lib/util'

def entry(name)
  run(name)
end

def twice(name)
  core(name) + core(name + 1)
end

def init
  'init-main'
end

def other_salute(name)
  # never calls core() — negative anchor for callers(lib.core)
  'hola'
end