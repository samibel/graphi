package link

// rubyResolver / phpResolver / luaResolver are the FU-5 registrations for the
// require/include script family. Each pulls in another file by a relative specifier
// resolved against the including file's directory; cross-file calls/references then
// resolve same-directory (derived) or in a required file's directory (heuristic),
// and the require itself yields a file→file `imports` edge. A search-path / gem /
// package require that resolves to no committed node skip+counts (no phantom).
type rubyResolver struct{}

// Language implements Resolver.
func (rubyResolver) Language() string { return "ruby" }

// Resolve implements Resolver for Ruby (require_relative 'x' → x.rb).
func (rubyResolver) Resolve(in FileRefs, idx *SymbolIndex, st *Stats) []intent {
	return resolveRefs(in, idx, st, requireBinder(in, []string{".rb"}))
}

// phpResolver is the FU-5 registration for PHP (require/include 'x.php').
type phpResolver struct{}

// Language implements Resolver.
func (phpResolver) Language() string { return "php" }

// Resolve implements Resolver for PHP.
func (phpResolver) Resolve(in FileRefs, idx *SymbolIndex, st *Stats) []intent {
	return resolveRefs(in, idx, st, requireBinder(in, []string{".php"}))
}

// luaResolver is the FU-5 registration for Lua (local m = require('x') → x.lua;
// m.fn() resolves in x.lua's directory).
type luaResolver struct{}

// Language implements Resolver.
func (luaResolver) Language() string { return "lua" }

// Resolve implements Resolver for Lua.
func (luaResolver) Resolve(in FileRefs, idx *SymbolIndex, st *Stats) []intent {
	return resolveRefs(in, idx, st, requireBinder(in, []string{".lua"}))
}
