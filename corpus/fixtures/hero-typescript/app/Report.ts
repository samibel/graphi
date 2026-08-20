/**
 * app package — the second cross-module caller.
 *
 * Like Service.ts, this wires the same named-import shape. The
 * two-callers-of-core shape is what gives the callers and impact
 * scenarios their cross-file reach.
 */

import { core } from "../impl";

/**
 * A second cross-module caller of core.
 *
 * The QN is `app.run`. The callers-of-core scenario asserts this as a
 * positive anchor (alongside app.serve, app.twice, and impl.salute).
 */
export function run(name: string): string {
    return core(name);
}
