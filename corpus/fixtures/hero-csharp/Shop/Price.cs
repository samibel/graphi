// Shop/Price.cs — the shared C# service. The directory basename
// `Shop` becomes the langPackage (core/parse/parser_tswalk.go:240),
// so the methods inside this file get QNs `Shop.core`, `Shop.salute`,
// `Shop.run`. The C# parser records the class `Price` as `Shop.Price`
// (KindType) at core/parse/parser_csharp.go:147; each method_declaration
// inside the declaration_list is recorded by its bare name at :156.
//
// The csharp resolver (engine/link/resolve_csharp.go:28) maps
// `using Shop;` to ambient clause `Shop` (last `.` segment of the
// using path). A selector call `Price.Of()` in `app/Caller.cs` then
// resolves to `Shop.run` via `crossModule("Shop", run)` at the
// heuristic tier (resolve_common.go:444).

namespace Shop
{
    public class Price
    {
        public static string core(string name)
        {
            return name;
        }

        public static string salute(string name)
        {
            return core(name);
        }

        public static string run(string name)
        {
            return core(name);
        }
    }
}
