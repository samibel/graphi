/**
 * app package — the cross-module caller the heuristic resolver wires.
 *
 * The named-import bare binding here is the G2SUB heuristic resolver's
 * primary synthesis path for the TypeScript family. The import path keys
 * on the LAST directory (matching langPackage: `impl/English.ts` →
 * `impl`), so `import { core } from "../impl"` resolves the bare
 * binding to the heuristic edge `app.serve -> impl.core` (and the same
 * for `app.twice`).
 *
 * The QN keys on the LAST package directory of the file path
 * (`core/parse/parser_tswalk.go:240` — `langPackage`), so the import
 * target is `impl.core` and the resolver emits a `calls` edge
 * `app.serve -> impl.core` (and `app.twice -> impl.core`) at the
 * heuristic tier — never at confirmed, because TS has no typed binder
 * that graphi exposes as a default product surface.
 */

import { core } from "../impl";

/**
 * The cross-module caller of core: emits a heuristic edge to impl.core.
 *
 * The QN is `app.serve`. The callers-of-core scenario asserts this as
 * a positive anchor; the callees-of-serve scenario asserts `impl.core`
 * as a positive.
 */
export function serve(name: string): string {
    return core(name);
}

/**
 * The richer callee shape: twice calls core twice, so the popped
 * Witnesses for callees (callers-of-twice) and for callees-of-twice
 * pivot on this name.
 *
 * The QN is `app.twice`. The callees-of-twice scenario asserts
 * `impl.core` as a positive anchor.
 */
export function twice(name: string): string {
    return core(name) + core("b");
}
