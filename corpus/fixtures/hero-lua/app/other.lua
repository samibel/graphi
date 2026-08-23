-- app/other.lua — second file in app/, defines the duplicate `init` that
-- drives the AMBIGUOUS scenario (callers(app.init) cannot pick a single
-- definition because byDir["app"]["init"] has two NodeIds — one per file —
-- and dirAmbiguous["app"]["init"] is set at engine/link/index.go:266).
--
-- greet() calls salute() through the SAME require's ambient-dir lookup
-- (lib/util.lua's dir key is <fixture>/lib), so callers(lib.salute)
-- surfaces app.greet as a positive anchor and impact(lib.salute) walks
-- back to it. greet never calls core() — a negative anchor for
-- callers(lib.core).

require('../lib/util')

function init()
  return "init-other"
end

function greet(name)
  return salute(name)
end
