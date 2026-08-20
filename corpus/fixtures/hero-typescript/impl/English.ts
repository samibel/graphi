/**
 * impl package — the canonical implementation of the Speaker contract.
 *
 * QN summary (langPackage-derived, core/parse/parser_tswalk.go:240):
 *   - impl._format    (a TypeScript interface — the references target)
 *   - impl.core       (the helper every caller in app/ uses)
 *   - impl.salute     (the contract method, calls core)
 *
 * The named-import bare binding in app/Service.ts and app/Report.ts
 * wires the heuristic cross-module edge: `import { core } from "../impl"`
 * — the G2SUB heuristic resolver's primary synthesis path (see
 * engine/link/resolve_typescript.go). The same QN is reused across both
 * forms, so the heuristic tier is asserted by the witness at this
 * level, never re-affirmed at confirmed.
 *
 * NOTE on the shape of `_format`: in the Python hero fixture
 * (`corpus/fixtures/hero-python/impl/English.py`) `_format` is a helper
 * function called by `core`. The TS family has the SAME `callers`
 * story (EdgeCalls), but the `references` story is different: the TS
 * parser only emits EdgeReferences for type identifiers that resolve to
 * in-file type definitions (core/parse/parser_tswalk.go:88 — `w.types`
 * is populated only when kind == KindType). So `references(impl._format)`
 * in TS finds `impl.core` only because `_format` is declared as an
 * `interface` (KindType) and `core`'s return type references it via
 * `tsxHandleTypeRef` -> `typeRef` -> `addEdge(..., EdgeReferences, ...)`.
 * This is the deliberate TS-specific shape that lets the hts-09
 * references scenario be FOUND at the heuristic tier — the Python
 * fixture, lacking a typed-binding story for variables, marks the same
 * shape empty (hpy-09).
 */

/**
 * The internal type `core` returns — the references target.
 *
 * The QN is `impl._format` (KindType: an `interface_declaration` registers
 * the bare name in `w.types`, parser_tsx.go:143). The references
 * operation finds `impl.core` as a referrer (the only function whose
 * return type uses this interface), so the references scenario anchors
 * on this surface WITHOUT crossing modules.
 */
export interface _format {
    prefix: string;
    name: string;
}

/**
 * The shared callee every caller pivots on.
 *
 * The witness scenarios (callers, callees, impact, neighborhood,
 * related_files) all anchor on `impl.core`. The return type
 * `: _format` emits the `EdgeReferences` edge from `impl.core` to
 * `impl._format` that the hts-09 references scenario asserts.
 */
export function core(name: string): _format {
    return { prefix: "Hi ", name };
}

/**
 * English honours the Speaker contract by delegating to core().
 *
 * QN is `impl.salute`. The callers-of-core scenario asserts this as a
 * positive (salute calls core); the callers-of-salute scenario asserts
 * it as ambiguous (api.salute, impl.salute, org.salute all share the
 * name).
 */
export function salute(name: string): string {
    const f = core(name);
    return f.prefix + f.name;
}
