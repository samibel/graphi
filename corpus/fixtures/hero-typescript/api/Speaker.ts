/**
 * api package — the contract that impl/ and org/ both honour.
 *
 * The QN for module-level functions is `<last_dir>.<funcname>`
 * (`core/parse/parser_tswalk.go:240` — the langPackage helper), so the
 * `export function salute(name)` here gives the resolved QN `api.salute`
 * because the langPackage of `api/Speaker.ts` is `api` (the parent
 * directory's base name). This is the fixture's stable call-site
 * identifier for the search/callers/callees/neighborhood/impact
 * scenarios.
 */

// SPEAKER_PREFIX is the read-only contract value every impl honours.
// The QN is `api.SPEAKER_PREFIX` and `references(SPEAKER_PREFIX)` would
// resolve through impl.English.core, which reads it via the module-level
// name. (The TS parser tracks `type_identifier` references; module-level
// constants like this one are not registered as types — see
// core/parse/parser_tswalk.go:88 — so they have no inbound references
// edges at the heuristic tier.)
export const SPEAKER_PREFIX = "Hello ";

/**
 * The contract method Speaker declares — every impl re-implements it.
 *
 * The QN is `api.salute` (KindFunction; the parser treats module-level
 * function declarations as functions, not methods — see
 * core/parse/parser_tsx.go:132). Note the absence of a `return` shape on
 * the contract: the impls choose how to construct the greeting.
 */
export function salute(name: string): string {
    return SPEAKER_PREFIX + name;
}
