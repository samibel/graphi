/**
 * org package — a second Speaker implementation that does NOT call core.
 *
 * Negative anchor for the callers-of-core scenario: org.salute honours
 * the Speaker contract but never calls core, so it MUST NOT appear in
 * the callers-of-core result.
 */

export const GERMAN_PREFIX = "Guten Tag ";

/**
 * German honours the Speaker contract without delegating to core.
 *
 * QN is `org.salute`. Like impl.salute the function is callable from
 * the outside, but unlike impl.salute it does NOT call core — that is
 * the negative anchor the hts-06 callers scenario relies on.
 */
export function salute(name: string): string {
    return GERMAN_PREFIX + name;
}
