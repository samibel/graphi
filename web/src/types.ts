// Single type barrel for the web client. The ENVELOPE/error/contract/SSE-frame
// shapes are GENERATED from surfaces/http/contract.schema.json (the SW-044
// single source of truth) into contract.gen.ts — see package.json `gen:types`
// and the `gen:types:check` drift gate. The curated EP-002 PAYLOAD shapes live
// in payload.ts. Importers depend only on this barrel.

import type { Envelope as GenEnvelope } from "./contract.gen";

// Re-export the generated contract shapes verbatim.
export type {
  Envelope as RawEnvelope,
  ErrorEnvelope,
  Contract,
  SseFrame,
} from "./contract.gen";

// Re-export curated payload shapes.
export type {
  ResultNode,
  ResultEdge,
  QueryResult,
  ImpactResult,
  StreamEvent,
} from "./payload";

/**
 * Typed success envelope. The generated `Envelope` (contract.gen.ts) types
 * `payload` as an opaque object since the EP-002 payload shape is versioned
 * separately; this generic narrows the payload at decode sites while keeping
 * the schema-derived `schema_version` literal from the generated type.
 */
export interface Envelope<T> extends Omit<GenEnvelope, "payload"> {
  payload: T;
}

/**
 * The client's built-against ENVELOPE schema version. Sourced from the GENERATED
 * literal type (`Envelope["schema_version"]` is `1` const in contract.gen.ts),
 * so a contract bump that regenerates the file forces this constant to be
 * reconciled — the runtime guard and the static type stay in lockstep.
 */
export const SCHEMA_VERSION: GenEnvelope["schema_version"] = 1;
