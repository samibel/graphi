// wikiClient is the DEDICATED backend boundary for the self-generated wiki
// (SW-046). It is deliberately SEPARATE from graphiClient.decodeEnvelope: the
// `/wiki` and `/wiki/c/{id}` routes return raw `text/markdown` — NOT the
// `{schema_version, payload}` JSON envelope — so the fail-closed schema guard
// does not (and must not) apply here.
//
// Contract (from surfaces/http/server.go, engine/wiki/wiki.go — SW-041/SW-044):
//   GET /wiki          → 200 text/markdown (index) | 404 (wiki disabled)
//   GET /wiki/c/{id}   → 200 text/markdown (page)  | 404 (disabled OR unknown id)
// 404 bodies are JSON error envelopes {schema_version, error:{code,message}}.
//
// Posture: GET-only (read-only, AC-6), loopback-only (S1, reuses the guard),
// and the returned text is NEVER mutated (preservation contract, AC-2).
import { assertLoopbackBase } from "./loopback";

const BASE = (import.meta.env.VITE_GRAPHI_URL as string | undefined) ?? "";
// Fail-closed at module load if someone points us off-loopback (S1).
assertLoopbackBase(BASE);

/**
 * Thrown when `GET /wiki` returns 404 — the daemon was started without
 * `WithWiki(store)`, so the wiki surface is disabled. This is a CONFIG state,
 * deliberately distinct from an empty-but-enabled wiki (a DATA state where the
 * index renders "0 communities"). The UI must not conflate the two (U1).
 */
export class WikiUnavailableError extends Error {
  readonly code: string;
  constructor(message = "wiki unavailable", code = "not_found") {
    super(message); // server-sanitized message only (S4)
    this.name = "WikiUnavailableError";
    this.code = code;
  }
}

/**
 * Thrown when `GET /wiki/c/{id}` returns 404 for a known-enabled wiki — the
 * community id does not exist. Distinct from WikiUnavailableError so the
 * community page can render a "community not found" affordance.
 */
export class WikiPageNotFoundError extends Error {
  readonly code: string;
  readonly id: string;
  constructor(id: string, message = "unknown community", code = "not_found") {
    super(message); // server-sanitized message only (S4)
    this.name = "WikiPageNotFoundError";
    this.code = code;
    this.id = id;
  }
}

/** Typed error for any other non-2xx wiki response (500/503/…). */
export class WikiError extends Error {
  readonly status: number;
  constructor(status: number, message: string) {
    super(message);
    this.name = "WikiError";
    this.status = status;
  }
}

/**
 * Best-effort: pull the sanitized `error.message` out of a JSON error-envelope
 * body. The wiki fetch path does NOT enforce the schema_version guard (this is
 * an un-enveloped surface), so a missing/garbled body is tolerated — we fall
 * back to a generic message keyed off the route + status.
 */
async function envelopeMessage(res: Response): Promise<string | undefined> {
  try {
    const body: unknown = await res.clone().json();
    if (
      typeof body === "object" &&
      body !== null &&
      "error" in body &&
      typeof (body as { error: unknown }).error === "object" &&
      (body as { error: { message?: unknown } }).error !== null
    ) {
      const m = (body as { error: { message?: unknown } }).error.message;
      if (typeof m === "string") return m;
    }
  } catch {
    /* body may be absent or non-JSON — ignore */
  }
  return undefined;
}

/**
 * Fetch the raw Markdown index from `GET /wiki`.
 * - 200 → the body text returned VERBATIM (no mutation, AC-2).
 * - 404 → WikiUnavailableError (wiki disabled).
 * - other non-2xx → WikiError.
 */
export async function fetchWikiIndex(): Promise<string> {
  const res = await fetch(`${BASE}/wiki`, {
    method: "GET",
    // Marks this as a DATA request so the dev proxy forwards it to the daemon
    // rather than serving the SPA shell (which it does for document navigations
    // that accept text/html — the deep-link / history fallback).
    headers: { Accept: "text/markdown" },
  });
  if (res.ok) return res.text();
  if (res.status === 404) {
    throw new WikiUnavailableError(
      (await envelopeMessage(res)) ?? "wiki unavailable",
    );
  }
  throw new WikiError(res.status, `HTTP ${res.status}`);
}

/**
 * Fetch the raw Markdown community page from `GET /wiki/c/{id}`. `id` is taken
 * verbatim (only URL-encoded for transport) — never re-derived or re-slugged.
 * - 200 → the body text returned VERBATIM (no mutation, AC-2).
 * - 404 → WikiPageNotFoundError (unknown community OR wiki disabled; the index
 *         load already distinguishes the disabled case as unavailable).
 * - other non-2xx → WikiError.
 */
export async function fetchWikiPage(id: string): Promise<string> {
  const res = await fetch(`${BASE}/wiki/c/${encodeURIComponent(id)}`, {
    method: "GET",
    headers: { Accept: "text/markdown" },
  });
  if (res.ok) return res.text();
  if (res.status === 404) {
    throw new WikiPageNotFoundError(
      id,
      (await envelopeMessage(res)) ?? "unknown community",
    );
  }
  throw new WikiError(res.status, `HTTP ${res.status}`);
}
