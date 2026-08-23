// app/Caller.cs — the cross-file C# caller of the shared core service.
//
// The top-level `using Shop;` is what the csharp parser records as an
// ImportSpec (core/parse/parser_csharp.go:281). The resolver turns it
// into ambient clause `Shop`. The selector call `Price.run(who)` then
// resolves to `Shop.run` via `crossModule("Shop", run)` at the heuristic
// tier (resolve_common.go:444).
//
// The directory basename `app` is the langPackage prefix; functions in
// this file get QNs `app.entry`, `app.twice`, `app.init_c` — distinct
// from any C/C++ callers under `c/` or `cpp/` (the ccpp twin).

using Shop;

namespace app
{
    public class Caller
    {
        public static string entry(string who)
        {
            return Price.run(who);
        }

        public static string twice(string who)
        {
            return Price.run(who) + Price.run(who + 1);
        }

        public static string init_c()
        {
            return "init-c";
        }
    }
}
