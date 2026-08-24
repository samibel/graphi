-- app/main.lua — the cross-file caller of the shared service.
--
-- This file lives under app/; langPackage prefixes every def's QN with
-- `app.` (app.entry, app.twice, app.init). The `require('../lib/util')`
-- statement is captured by the Lua parser's luaImports walker
-- (core/parse/parser_lua.go:218) and turned into an ImportSpec. The
-- luaResolver's requireBinder (engine/link/resolve_ruby.go:38 +
-- engine/link/resolve_common.go:332) joins that spec against
-- `<fixture>/app` to produce `<fixture>/lib/util`, registers the file
-- target `lib/util.lua` (yielding the file→file `imports` edge that
-- related_files relies on) and the ambient dir `lib` (through which
-- the bare `core()` / `run()` calls below resolve to `lib.core` /
-- `lib.run`).
--
-- entry and twice both call run; twice calls run twice — the
-- multi-call-sites shape that drives the
-- explain_symbol(..., max_items=1) PARTIAL scenario. init is
-- duplicated with app/other.lua, which makes callers(app.init)
-- AMBIGUOUS.

require('../lib/util')

function entry(name)
  return run(name)
end

function twice(name)
  return run(name) .. run(name)
end

function init()
  return "init-main"
end
