// app/Other.cs — second caller in the same `app/` directory; ambiguous
// `init_c` with app/Caller.cs. Both files live in `app/`, so the byDir
// index records `app.init_c` TWICE (two distinct NodeIds, one per
// source location). dirAmbiguous["app"]["init_c"] = true makes
// resolve.Strict return AMBIGUOUS for any reference to `init_c`,
// including the search/resolve lookup the callers-ambiguous scenario
// pivots on.
//
// `other_salute` is a NEGATIVE ANCHOR: it never calls `Price.run`, so
// callers(Shop.run) MUST NOT surface `app.other_salute`.

using Shop;

namespace app
{
    public class Other
    {
        public static string init_c()
        {
            return "init-c-other";
        }

        public static string other_salute(string name)
        {
            return "hola";
        }
    }
}
