// Zero-outbound / loopback-only guard (S1). The extension must only ever talk
// to the locally-running graphi daemon. This module validates the configured
// base URL and is unit-tested to reject any non-loopback origin. Mirrors
// web/src/loopback.ts and SW-039's http.AssertLoopback.

const LOOPBACK_HOSTS = new Set(["127.0.0.1", "localhost", "::1", "[::1]"]);

/** True when `base` is a loopback target. */
export function isLoopbackBase(base: string): boolean {
  let url: URL;
  try {
    url = new URL(base);
  } catch {
    return false;
  }
  const host = url.hostname.toLowerCase();
  return LOOPBACK_HOSTS.has(host) || LOOPBACK_HOSTS.has(`[${host}]`);
}

/** Throw if `base` is not loopback. Fail-closed: callers must not proceed. */
export function assertLoopback(base: string): void {
  let parsed: URL;
  try {
    parsed = new URL(base);
  } catch {
    throw new Error(`graphi: invalid daemon URL "${base}"`);
  }
  const host = parsed.hostname.toLowerCase();
  if (!(LOOPBACK_HOSTS.has(host) || LOOPBACK_HOSTS.has(`[${host}]`))) {
    throw new Error(
      `graphi: refusing non-loopback daemon URL "${base}" (local-first, loopback-only)`,
    );
  }
}
