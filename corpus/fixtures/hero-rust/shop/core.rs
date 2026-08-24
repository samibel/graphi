// shop/core.rs — the canonical shared service for the SW-197 hero-rust
// tasks. The pub functions are at the top level of this file (NOT
// wrapped in `mod shop { ... }`) so the cstWalk's langPackage key
// (`shop/core.rs` → package `shop`) mints QNs `shop.core`, `shop.salute`,
// `shop.run` — matching the `<dir>.<bare>` convention every
// tree-sitter language parser shares (core/parse/parser_tswalk.go:240).
//
// `core` is the cross-module callee every caller in app/ pivots on;
// `salute` is the impl-internal caller (calls `core`); `run` is the
// cross-module target the bare binding `use crate::shop::run;`
// resolves to (clause-keyed: clause = "shop", bare = "run").

pub fn core(name: &str) -> String {
    format!("Hi {}", name)
}

pub fn salute(name: &str) -> String {
    // salute calls core (intra-file EdgeCalls: app.salute → shop.core
    // would not be the path; this stays inside shop.core.rs).
    core(name)
}

pub fn run(name: &str) -> String {
    // run calls core (intra-file EdgeCalls edge: shop.run → shop.core).
    core(name)
}
