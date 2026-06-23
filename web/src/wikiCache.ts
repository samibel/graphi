// wikiCache is a tiny session-lifetime cache of fetched wiki bodies (SW-046).
// The daemon already generates+caches the wiki once per server lifetime (a
// stable snapshot), so this is purely a UX nicety: it avoids a refetch when the
// user navigates back/forward between the index and community pages.
//
// Cached bytes are stored and returned UNCHANGED (preservation contract, AC-2).
// Keys: "index" for the index, the verbatim id for community pages.

const cache = new Map<string, string>();

const INDEX_KEY = "index";

/** Memoize a fetch by key; the body is stored and returned verbatim. */
async function memo(key: string, fetcher: () => Promise<string>): Promise<string> {
  const hit = cache.get(key);
  if (hit !== undefined) return hit;
  const body = await fetcher();
  cache.set(key, body);
  return body;
}

export function cachedIndex(fetcher: () => Promise<string>): Promise<string> {
  return memo(INDEX_KEY, fetcher);
}

export function cachedPage(
  id: string,
  fetcher: () => Promise<string>,
): Promise<string> {
  return memo(`c/${id}`, fetcher);
}

/** Test-only: clear the session cache. */
export function _clearWikiCache(): void {
  cache.clear();
}
