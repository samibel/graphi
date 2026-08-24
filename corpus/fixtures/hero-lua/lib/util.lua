-- lib/util.lua — shared service definitions, top-level functions only.
--
-- The Lua parser's luaCollectDefs (core/parse/parser_lua.go:121) walks the
-- chunk's direct children and records every `function_declaration` whose
-- `name` field is an identifier. Methods declared as `function obj:method()`
-- inside a table are NOT indexed at this tier (the parser records the
-- outer table assignment as a KindVariable and ignores nested methods).
-- Keeping the module's surface at top-level lets the parser lift every
-- callee the cross-file scenarios pivot on (core, salute, run).
--
-- langPackage derives the QN prefix from the parent directory's BASENAME
-- (core/parse/parser_tswalk.go:240), so functions in this file are named
-- lib.core, lib.salute, lib.run — the cross-file target every caller
-- file resolves to through the luaResolver's ambient-dir fallback.

function core(name)
  return salute(name)
end

function salute(name)
  return name or ""
end

function run(name)
  return core(name)
end
