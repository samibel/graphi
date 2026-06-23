// Zero-outbound / loopback-only guard (S1). The web client must only ever talk
// to the locally-running graphi daemon. This module validates the configured
// base URL and is unit-tested to reject any non-loopback origin.

const LOOPBACK_HOSTS = new Set(["127.0.0.1", "localhost", "::1", "[::1]"]);

/**
 * True when `base` is a loopback target. An empty string is allowed: it means
 * "same-origin relative request", which under the Vite dev proxy and a
 * loopback-served production bundle is also loopback-only.
 */
export function isLoopbackBase(base: string): boolean {
  if (base === "") return true;
  let url: URL;
  try {
    url = new URL(base);
  } catch {
    return false;
  }
  const host = url.hostname;
  return LOOPBACK_HOSTS.has(host) || LOOPBACK_HOSTS.has(`[${host}]`);
}

/** Throw if `base` is not loopback. Fail-closed: callers must not proceed. */
export function assertLoopbackBase(base: string): void {
  if (!isLoopbackBase(base)) {
    throw new Error(
      `refusing non-loopback base URL "${base}": the graphi web client is loopback-only (zero-outbound).`,
    );
  }
}
