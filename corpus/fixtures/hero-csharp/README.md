# hero-csharp fixture — W5.k SW-197

The C# fixture for the SW-197 hero-csharp tasks. The shared service lives
in `Shop/Price.cs` (`namespace Shop { class Price { ... } }`); two callers
in `app/Caller.cs` and `app/Other.cs` bring `Shop` into scope via the
top-level `using Shop;` directive.

The C# parser is registered as `c_sharp` (core/parse/parser_csharp.go:29)
and the QN convention is `<dir>.<bare>` (langPackage: parent dir basename,
core/parse/parser_tswalk.go:240). `Shop/Price.cs` therefore yields QNs
`Shop.core`, `Shop.salute`, `Shop.run` (NOT `shop.*`, NOT `Price.*`,
NOT `Shop.Price.*`) — the parser records the class `Price` as `Shop.Price`
(KindType) and each method inside as a bare method, qualified by the
file's directory basename. The csharp resolver
(engine/link/resolve_csharp.go:28) consumes the `using` directives and
turns each into an ambient clause (`using Shop;` → clause `Shop`,
the last `.` segment of the using path), through which a selector call
`Price.Of()` resolves to `Shop.run` via `crossModule("Shop", run)` at
the heuristic tier.

`app/init_c` is the negative (absent) anchor for callers-of-Shop: it
is defined in both `app/Caller.cs` and `app/Other.cs`, making
`byDir["app"]["init_c"]` carry two distinct NodeIds (dirAmbiguous =
true). `callers(init_c)` returns AMBIGUOUS, mirroring the ccpp shape.

The fixture deliberately avoids `#region`, `#ifndef`, `extern alias`,
and `partial` declarations — the parser does not descend into them, so
definitions inside such blocks would never index.

No network, no .NET runtime. The class members are `public static`
methods so the parser walks `method_declaration` children inside
`declaration_list` bodies (`core/parse/parser_csharp.go:150`).
