// app/caller.rs — the cross-module caller of the shared shop service.
//
// `use crate::shop::run;` is the rustResolver's clause-keyed bind:
// clause = SECOND-to-last `::` segment of the import path → "shop",
// bare = "run". The bare `run(who)` call inside `entry` resolves via
// `crossModule("shop", "run")` (engine/link/resolve_common.go:488) to
// `shop.run` at the heuristic tier (Rust has no typed binder; the
// G2SUB substitution is the language's honest level).
//
// `entry` calls `run(...)` THREE times — populating the items list
// for `explain_symbol(shop.run, max_items=1)` to trigger
// `Limits.Truncated`, marking the result `partial` (the Rust parallel
// of the ccpp twin's hccpp-15).
//
// `init` is defined here AND in app/other.rs — that duplication is
// what gives `callers(init)` its `ambiguous` outcome (both QNs match
// the same `app.init` key with two distinct NodeIds).

use crate::shop::run;

pub fn entry(who: &str) -> String {
    // Three call sites of shop.run — the explain-partial scenario
    // counts them so max_items=1 truncates.
    let first = run(who);
    let second = run(&(first.clone() + " again"));
    run(&second)
}

pub fn twice(who: &str) -> String {
    // A second caller of shop.run — gives shop.run at least two
    // distinct cross-module callers, anchoring the
    // callers-cross-module scenario with positive evidence.
    let a = run(who);
    run(&(a + " twice"))
}

pub fn other_salute(_name: &str) -> &'static str {
    // NEGATIVE ANCHOR: never calls shop.core — must NOT appear in
    // callers(shop.core) or impact(shop.core).
    "hola"
}

pub fn init() -> &'static str {
    "init from caller"
}
