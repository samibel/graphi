# HTTP/SSE Surface (`surfaces/http`)

> A read-only HTTP REST + SSE surface over the shared engine, intended for
> browser and IDE clients that cannot use stdio or Unix-socket transports.
> Later extended with a one-shot SSE analysis frame and a set of PR-tool
> endpoints (`/prs/*`, `/branches/compare`, `/reviews/critique`).

## Before / after

| | Before | After initial HTTP surface | After later extensions |
|---|---|---|---|
| **Transports** | CLI, Unix-socket daemon, MCP stdio | + **HTTP REST + SSE** over loopback | + package-level MCP HTTP adapter for embedders + **per-class SSE** + **PR-tool endpoints** |
| **TS/web/IDE backend** | none (no transport for the web client or VS Code extension) | stable, versioned HTTP contract these surfaces consume | unchanged; consumers can pin to the same envelope |
| **Freshness/events** | none (no observer) | `engine/observe` broker; ingest publishes lifecycle events; SSE streams them | + per-class subscriptions (`class=ingest`, `class=analyze`, `class=overlay`, `class=watcher`, `class=community`) and a one-shot `?analyzer=<name>` analysis frame on `/events` |
| **Code reuse** | each surface delegates to `client.Client` | HTTP delegates to the **same** `client.Client` seam → byte-identical answers (parity) | unchanged; every new endpoint rides the same shared client |

## Why

The downstream TS/React web client and VS Code extension need a **transport
that runs in a browser/extension runtime** — stdio (MCP) and a Unix-socket RPC
(daemon) cannot provide that. HTTP is the universal consumer boundary.

To avoid surface-forked logic, the HTTP layer delegates to the **same**
`client.Client` interface the CLI/MCP/daemon use, so every answer — including
per-edge provenance (`confidence`, `confidence_tier`, `reason`, `evidence`) — is
byte-identical across surfaces. SSE gives those clients a **streamed freshness**
channel (no polling) over a generic `engine/observe` broker.

## Contract

### Envelope
Every data response is wrapped in a versioned envelope so consumers can detect
drift. `payload` carries the engine's canonical serialized bytes **verbatim** —
the same bytes MCP/CLI return:

```json
{ "schema_version": 1, "payload": { /* engine result */ } }
```

### REST routes (all read-only; non-GET → 405)
| Method | Route | Delegates to |
|---|---|---|
| GET | `/healthz` | — (liveness) |
| GET | `/contract` | capability negotiation document |
| GET | `/query/{op}?symbol=&depth=` | `client.Query` (`{op}` ∈ callers/callees/references/definition/neighborhood/implementers/implements/overrides/subtypes/supertypes) |
| POST | `/compound` | `client.Compound` (Cypher-style) |
| POST | `/query-ast` | `client.SearchAST` |
| POST | `/find-clones` | `client.FindClones` |
| GET | `/search?q=&limit=` | `client.Search` |
| GET | `/search/semantic?q=&limit=` | `client.SearchSemantic` (graceful-skip when no embedder) |
| GET | `/analyze/{analyzer}?symbol=&direction=&max-nodes=` | `client.Analyze` (incl. `impact`, `call-chain`, `concept`, `metrics`, `batched`, `taint`, `pdg`, `interproc`, `contracts`, `git-history`, `pr-risk`, `pr-signals`, `pr-questions`, `communities`, `notebook-ingest`, `taint-query`, `watcher-status`, `triage-prs`, `conflicts-prs`, `suggest-reviewers`, `compare-branches`, `critique-review`) |
| GET | `/prs` | `client.ListPRs` (read-only forge enumeration) |
| GET | `/prs/triage` | `client.TriagePRs` (single-pass graph-derived ranking) |
| GET | `/prs/conflicts` | `client.ConflictsPRs` (textual + graph-semantic + asymmetric contract-dependency) |
| GET | `/prs/suggest-reviewers` | `client.SuggestReviewers` (ownership/churn + affected-subgraph proximity) |
| GET | `/branches/compare?base=&head=` | `client.CompareBranches` (graph-level diff, keyed by canonical NodeId) |
| GET | `/reviews/critique?diff=&pr=` | `client.CritiqueReview` (graph-evidence critique of an existing PR review) |
| POST | `/memory` | `client.Memory` (store / recall / forget) |
| POST | `/distill` | `client.Distill` (session distillation) |
| POST | `/skillgen` | `client.SkillGen` (deterministic skill generation) |
| GET | `/events` (SSE) | `engine/observe` broker; per-class subscriptions |
| GET | `/wiki` | `client.WikiIndex` (community-driven) |
| GET | `/wiki/c/{id}` | `client.WikiPage` |
| GET | `/` | SPA (bundled `webui_embed` build) or notice page |

### Schema-version drift gate
A request header `X-Graphi-Schema-Version: N` where `N != 1` → **412 Precondition
Failed** (envelope still echoes the current version). Absent header = no
negotiation (pass-through).

### SSE — `GET /events`
- `Content-Type: text/event-stream`; `:keep-alive` comment every 15s.
- Each event: `data: {"type":"ingest-completed","ts":"...","payload":{...}}\n\n`.
- **Per-class subscriptions.** The optional `?class=<name>` query
  parameter filters the stream to a single class: `ingest`, `analyze`,
  `overlay`, `watcher`, `community`. Absent or empty `class` = firehose.
  Classes are defined in `engine/observe/class.go`; the broker drops the
  request silently if the class is unknown (defense-in-depth — never errors
  on a misconfigured subscriber).
- **One-shot analysis frame.** With `?analyzer=<name>` the connection
  terminates after the next matching analysis frame is delivered (e.g.
  `?analyzer=watcher-status` returns the current watcher health and closes).
  This suits an editor polling at low frequency without holding a
  long-lived SSE connection open.
- **Backpressure:** subscriber buffer = 16; a slow subscriber's events are
  dropped (never blocked) — loss-tolerant by design (read-only freshness stream).
- **Lifecycle:** on client disconnect the request context cancels, the broker
  drops the subscriber, and the handler goroutine exits — no leak.

## Local-first / zero-outbound contract
- The listen address **must** be loopback (`127.0.0.1` / `localhost` / `::1`).
  Both `http.ListenAndServe` **and** `cmd/graphi runHTTP` validate this **before**
  binding and refuse non-loopback addresses.
- The surface makes **zero outbound** connections; it only accepts inbound
  loopback connections and calls the in-process engine. No telemetry, no fetch.
- **Zero-egress enforcement guard.** The central loopback/egress
  chokepoint (`surfaces/guard`) rejects any non-loopback `Dial` at the
  surface boundary — fail-closed, with a live netns canary
  (`internal/canary/gate.go`) as a defense-in-depth layer. This is the same
  guard every other surface rides, so HTTP cannot be a back door.

## Data flow

```mermaid
sequenceDiagram
    participant C as Client (loopback)
    participant H as surfaces/http
    participant Cl as client.Client
    participant E as engine (query/observe)
    C->>H: GET /query/callers?symbol=pkg.Foo
    H->>Cl: Query(ctx, "callers", "pkg.Foo", 0)
    Cl->>E: query.Service.Callers(...)
    E-->>Cl: Result{nodes,edges + provenance}
    Cl-->>H: []byte (canonical)
    H-->>C: 200 {schema_version:1, payload:<bytes>}
```

```mermaid
sequenceDiagram
    participant C as Client (loopback)
    participant H as surfaces/http
    participant B as engine/observe.Broker
    participant I as engine/ingest
    C->>H: GET /events (SSE)
    H->>B: Subscribe(ctx)
    Note over H,C: text/event-stream, :keep-alive every 15s
    I->>B: Publish("ingest-completed")
    B-->>H: Event (ordered, per-subscriber)
    H-->>C: data: {...}\n\n
    C-->>H: disconnect (ctx cancel)
    H->>B: subscriber dropped, goroutine exits
```

```mermaid
sequenceDiagram
    participant ED as Editor
    participant H as GET /events?class=watcher
    participant B as engine/observe.Broker
    participant W as engine/watch.Service
    ED->>H: GET /events?class=watcher
    H->>B: Subscribe(ctx, class=watcher)
    Note over H,ED: per-class filter (SW-097)
    W->>B: Publish(class=watcher, payload={health,errors})
    B-->>H: event (typed)
    H-->>ED: data: {"class":"watcher",...}\n\n
    ED->>H: GET /events?analyzer=watcher-status
    H->>W: analyze(watcher-status)
    W-->>H: snapshot
    H-->>ED: data: {"class":"analyze","analyzer":"watcher-status",...}\n\n (then close)
```

## Run

```bash
# loopback HTTP + SSE; in-memory store; ingest a repo so SSE has a producer
graphi http -addr 127.0.0.1:8080 -db "" -root ./myrepo

# against a durable store, custom meta sidecar
graphi http -addr 127.0.0.1:8080 -db ./graph.db -root ./myrepo -meta ./meta
```

## Parity proof (tests)
`surfaces/http/server_test.go` asserts `envelope.payload == client.Query(...)`
**byte-for-byte** for every op + representative symbols — the strongest possible
proof that the HTTP surface returns the same answers (and provenance) as MCP/CLI.
Additional tests cover the 412 schema gate, 405 read-only enforcement, SSE
ordering, goroutine-leak-free disconnect, cold-start P95 < 100ms, and the
loopback-only bind refusal.
