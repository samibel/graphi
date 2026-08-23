// app/other.rs — second `init` definition in the same `app/`
// directory; ambiguous `init` with caller.rs.
//
// Both caller.rs and other.rs live in `app/`, so both mint
// `app.init` — two distinct NodeIds in `byDir["app"]["init"]`. The
// resolver drops and counts same-clause collisions instead of
// guessing, so `callers(init)` returns `ambiguous`.

pub fn init() -> &'static str {
    "init from other"
}
