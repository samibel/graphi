# Semantic search — S0 baseline (what exists today)

**Story:** SW-257 · **Spec:** semantic-search-wow-and-token-savings · **Candidate:** `c302d9c`
**Decision record:** `projects/graphi/memory/decisions/2026-08-30-semantic-search-labs-track-s0.md`
(delivery portfolio, outside this repository) — the S0 scope-lock this page is the evidence for.

Before SW-258..SW-266 change anything about how graphi embeds, indexes, ranks or renders
semantic search, "unchanged" has to be **checkable**. This page freezes the facts every later
story of the track proves byte-identity or improvement against: which commit, which Go, which
corpus, what text is embedded, how vectors are scoped and reloaded, which operations already
dispatch through the executor seam, what the current default-build bytes are, and what the
rollback is. Every claim below carries the `file:line` it was read from at the candidate.

Nothing on this page changes shipped bytes. SW-257 is docs plus one Go comment
(`surfaces/client/canary.go`) plus one test assertion (`surfaces/client/migration_test.go`).

---

## 1. Candidate and toolchain

| Item | Value |
|---|---|
| Candidate (`origin/main`) | `c302d9c5b68e99e98eb517ee75b59b6f629dd22c` — "Merge pull request #188 from samibel/codex/compound-engine-handler" |
| Content-identical commit | `efd293cef54e1d332048ac313e7a12759fd765f8` — "Add compound engine handler module". `git diff --stat c302d9c efd293c` is empty: the merge commit's tree **is** `efd293c`'s tree, so either SHA identifies the same bytes. The spec's `reviewed_against` names `efd293c`; the decision record names `c302d9c`. |
| `go version` | `go version go1.26.6 darwin/arm64` |
| `go.work` | `go 1.26.6` / `use .` — one module, toolchain pinned to the same version the measurement ran on |
| Track branch base | every SW-25x/26x branch starts from `main` at the candidate |

Reproducibility rule for later stories: a "byte-identical" claim is a claim against the
candidate's bytes on **this** Go version. A Go upgrade in the middle of the track is a
separate change with its own re-measurement, not a silent part of a story.

---

## 2. Eval corpus — the pinned repositories

`corpus/manifest.json` (version 3, sha256
`c0b822de9f48d780c7ac5610c80ceeaa0c706fa268815d42d9f7b70315c7ad10`). Refs pin release tags and
`sha` pins the checkout HEAD; the pin step fails closed if a tag is ever re-pointed. Go is the
release-gate language of this track (spec decision 2); the non-Go pins exist for cross-language
regression detection and report `unvalidated` for semantic purposes.

| Repository | Ref | Pinned sha | Language | Tier |
|---|---|---|---|---|
| cobra | `v1.8.0` | `a0a6ae020bb3899ff0276067863e50523f897370` | go | (see manifest) |
| uuid | `v1.6.0` | `0f11ee6918f41a04c201eceeadf612a377bc7fbc` | go | 3 |
| lo | `v1.39.0` | `5777c5a3ee09f852d55a5bb5f585fcaeb5a0aedb` | go | 3 |
| gin | `v1.9.1` | `4ea0e648e38a63d6caff14100f5eab5c50912bcd` | go | 3 |
| grpc-go | `v1.60.1` | `dbbcf59957fec0bd58063224cbf105b3b3698d4e` | go | 3 |
| kubernetes | `v1.29.0` | `3f7a50f38688eb332e2a1b013678c6435d539ae6` | go | 4 (manual-only stress) |
| flask | `3.0.0` | `735a4701d6d5e848241e7d7535db898efb62d400` | python | 3 |
| sinatra | `v4.0.0` | `b626e2d82c23b4fde0b51782fd32ca27ccde1d1a` | ruby | 3 |
| ky | `v1.2.0` | `38ac18bc1ac3268130de766891ce9b718eb8145a` | typescript | (see manifest) |
| express | `4.18.2` | `8368dc178af16b91b576c4c1d135f701a0007e5d` | javascript | (see manifest) |
| guava | `v33.0.0` | `2214c63670fc161da170ac6e1a2d6d07e1531a55` | java | 3 |
| okio | `3.9.1` | `8b870e8eaacecb1c1ceffbbb47246112604a1f92` | kotlin | 3 |
| kotlinx.serialization | `v1.6.3` | `3efe324be422ead21ca44f2f6318e1791c166556` | kotlin | 3 |
| antlr4 | `4.13.1` | `7ed420ff2c78d62883875c442d75f32e73bc86c8` | java | 3 |
| retrofit | `2.11.0` | `cc76c22a68e090f3dd898cbcb0bac30414f59c31` | java | 3 |
| cjson | `v1.7.19` | `c859b25da02955fef659d658b8f324b5cde87be3` | c | 3 |
| Newtonsoft.Json | `13.0.3` | `0a2e291c0d9c0c7675d445703e51750363a549ef` | csharp | 3 |
| nlohmann_json | `v3.11.3` | `9cca280a4d0ccf0c08f47a99aa71d1b0e52f8d03` | cpp | 3 |
| lua-resty-core | `v0.1.26` | `407000a9856d3a5aab34e8c73f6ab0f049f8b8d7` | lua | 3 |
| composer | `2.9.8` | `39ee8baff8e97a1b657bbfcd6a236ff93a5efbb2` | php | 3 |
| serde | `v1.0.219` | `49d098debdf8b5c38bfb6868f455c6ce542c422c` | rust | 3 |

Tier-1 entries (`tier1-fixture-go`, `tier1-fixture-hero-*`) are the committed local fixtures
under `corpus/fixtures/`, not remote pins; `bash-abstention` and `sql-abstention` are
abstention records with no repository. The Go rows carry the stratification the manifest's
`stratification` block names (small library → monorepo); SW-258 picks its eval repositories
from these rows and nowhere else.

---

## 3. What is embedded today — the `NodeText` contract

`engine/embed/generate.go:17-24`:

```go
func NodeText(n model.Node) string {
	qn := strings.TrimSpace(n.QualifiedName())
	kind := strings.TrimSpace(n.Kind())
	if kind == "" {
		return qn
	}
	return kind + " " + qn
}
```

The embedded document is **`Kind + " " + QualifiedName`** and nothing else — no doc comment,
no signature, no body, no path. The comment at `generate.go:11-16` records why:
`core/model.Node` is a pure identity leaf with no doc/comment accessor. This is the research
doc's diagnosis and the reason SW-260 (a versioned `SemanticDocument v2`) precedes any ranking
work: measuring fusion over name-only documents would measure the wrong thing.

Generation is gated strictly on an embedder being configured — `generate.go:77-79` returns a
graceful-skip `GenerateResult{Configured: false}` having performed no embedding, no network and
no writes; `generate.go:91` is the single call site of `NodeText` on the embedding path.

---

## 4. Vector storage — the `vectors` table and its scope

### 4.1 DDL

`engine/embed/sqlite_vectorstore.go:45-52`:

```sql
CREATE TABLE IF NOT EXISTS vectors (
	node_id     TEXT NOT NULL,
	embedder_id TEXT NOT NULL,
	dim         INTEGER NOT NULL,
	vec         BLOB NOT NULL,
	PRIMARY KEY (embedder_id, node_id)
);
```

- `vec` is big-endian float32 components (`encodeVector`, `sqlite_vectorstore.go:158-164`) so a
  persisted vector is byte-identical across architectures.
- The table lives in the ingest meta sidecar `ingest-meta.db` (`sqlite_vectorstore.go:80`),
  beside `file_content_cache` / `reverse_deps` / `dirty_units` / `edit_provenance`; graphstore
  is **not** extended with a vectors column (`sqlite_vectorstore.go:17-19`).
- Driver: `modernc.org/sqlite` only (`sqlite_vectorstore.go:14`) — CGo-free.

### 4.2 The `(embedder_id, dim)` scope is the only invalidation key

`sqlite_vectorstore.go:22-28` states the contract; `Load` implements it at
`sqlite_vectorstore.go:112-124`: rows are selected `WHERE embedder_id = ?` and, when the expected
dimension is known (`dim > 0`), additionally `AND dim = ?`, in `ORDER BY node_id`. `Upsert`
(`sqlite_vectorstore.go:94-106`) is keyed by `(embedder_id, node_id)` and replaces in place.

What this means for the track, stated plainly:

- A changed or absent embedder reads **zero** rows — stale spaces never mix.
- There is **no** document fingerprint, no generation id, no "built from graph revision X"
  stamp, and no atomic publish. A re-index with the same embedder overwrites row by row; a
  node whose text changed but whose id did not keeps its old vector until it is re-embedded.
  This is what SW-261 (`GenerationStore`) replaces. Per the spec's "Rejected" list the
  `VectorTable` interface is replaced, not decorated.

### 4.3 The `VectorTable` seam and the in-memory index

`engine/embed/vectorstore.go:28-33`:

```go
type VectorTable interface {
	// Upsert durably stores (or replaces) the vector for v.NodeID.
	Upsert(ctx context.Context, v Vector) error
	// Load returns every stored vector, in canonical NodeId order.
	Load(ctx context.Context) ([]Vector, error)
}
```

Two implementations: `MemVectorTable` (`vectorstore.go:38-70`, tests and the reference) and
`SQLiteVectorTable` (§4.1). The live index is `embed.Index` — brute-force cosine over a
`map[NodeId][]float32` with a NodeId-ascending tie-break (`vectorstore.go:76-80`, `Search` at
`:146-168`), rebuilt from a `VectorTable` by `Rebuild` (`:89-108`). Scores are float64 cosine
values at this baseline; the integer quantisation the spec requires (decision 4) does not exist
yet.

---

## 5. Post-ingest semantic wiring order (`Composition.Client()`)

`cmd/internal/runtime/builder.go:192-195` — `Composition.Client()` builds the surface client as
`client.NewDirect(c.graphQuery, NewSearchService(c.store, c.metaDir))`. The search service is
composed **once**, post-ingest, in `NewSearchService` (`cmd/internal/runtime/runtime.go:770-804`),
in this order:

| Step | Line | What happens |
|---|---|---|
| 0 | `runtime.go:771` | `svc := search.New(store)` — lexical search, always available |
| 1 | `runtime.go:772` | selector read: `embed.Constructor(os.Getenv(embed.EnvSelector), embed.DefaultConstructors())`; `EnvSelector` is `GRAPHI_EMBEDDER` (`engine/embed/defaults.go:13`) |
| 1a | `runtime.go:773-778` | constructor error (e.g. non-loopback Ollama host) → fail closed: stderr line, **return the lexical-only service** |
| 1b | `runtime.go:779-781` | selector empty/unknown → `emb == nil` → **return the lexical-only service** (graceful skip: nothing below runs) |
| 2 | `runtime.go:782-789` | `embed.NewRegistry()` + `Register(emb)` |
| 3 | `runtime.go:790` | `reg.Freeze()` — embedder composition is complete (SW-222 / AX-02) |
| 4 | `runtime.go:791` | `index := embed.NewIndex()` |
| 5 | `runtime.go:792-802` | with a `metaDir`: `embed.OpenSQLiteVectorTable(ctx, metaDir, emb.ID(), emb.Dim())`, `index.Rebuild(ctx, table)`, `table.Close()` — the durable reload, a pure local read; reload errors are reported to stderr and leave the index empty |
| 6 | `runtime.go:803` | `return svc.WithSemantic(reg, index, store)` |

The same constructor is used by the two other client-construction paths in the composition
root (`runtime.go:283`, `runtime.go:421`), so there is one implementation of this order, not
three. The spec's deferred "explicit post-ingest finalisation of `Composition.Client()`" (open
question 8) refers to this seam; SW-263/SW-264 compose the retrieval instance here and hand it
to both consumers, they do not add a second composition root.

Generation (`graphi index --semantic`) mirrors the gate: `cmd/graphi/query.go:301-316` returns
before constructing anything when `--semantic` is absent or the selector is empty; the vectors
table is opened only at `query.go:329`, after the embedder exists.

---

## 6. Executor seam — the migrated set at the baseline

`surfaces/client/canary.go` `migratedOperations`, verbatim:

```go
var migratedOperations = []string{
	"architecture",
	"architecture_violations",
	"compound",
	"dead_code",
	"find_clones",
	"framework_map",
	"repo_overview",
	"search_ast",
	"search_hybrid",
	"test_impact",
}
```

Ten Labs operations. `search_hybrid` is **on** the seam (shadow by default; see §8);
`search_semantic` and `task_context` are **not**. `go run ./cmd/seamreach -check` at the
candidate reports "10 operation(s) on the seam, 10 dual-running (shadow or active)", none
reachable through the default `graphi mcp` profile, all ten through `graphi mcp -labs`.

Why the two absentees are absent, as recorded in `canary.go` and `migration_test.go`:

- `search_semantic` — argument-fidelity fixtures **exist** since SW-239
  (`surfaces/client/executor_argument_fidelity_test.go`, `query` and `limit` observable over
  `embed.MockEmbedder`). Migration is deferred until deterministic fixtures exist for all four
  states `configured | unavailable | stale | corrupt`; SW-265 owns that decision.
  `TestSW257_SearchSemanticIsNotMigrated` (`surfaces/client/migration_test.go`) pins the
  absence so it cannot change by accident.
- `task_context` — catalog determinism `environment-dependent`; a dual-run whose halves may
  legitimately disagree cannot prove parity (`canary.go`, "Deliberately absent" list). The spec
  keeps it outside a forced dual-run.

---

## 7. Current default-build bytes (AC-3)

"Default build" means: no `GRAPHI_EMBEDDER`, no `--semantic`, CGo-free, the committed Go
fixture `corpus/fixtures/go`, the client wired exactly as
`surfaces/characterization_golden_test.go:93-96` wires it (`search.New(store)` — no
`WithSemantic`).

### 7.1 Checked-in artifacts that pin these tools' *shape* (descriptors, wire names, routes)

None of the three tools has a checked-in canonical-bytes golden (see §7.2). What **is**
checked in is their advertised shape — name, tier, description, input schema — and those files
are the drift guards a later story's descriptor change would move:

| Artifact | sha256 at the candidate |
|---|---|
| `surfaces/mcp/testdata/mcp-wire-names.json` (`search_hybrid`, `search_semantic`, `task_context` all tagged `labs`) | `7abfcb59adb0017084f6c1dc3708cc862854d282a5fdab798d4958dc06344aa2` |
| `surfaces/mcp/testdata/mcp-descriptors-stdio-labs.json` | `17747e4dc6a5ee62ba1a90a062ef29f335f32ee0a1b700d1a1b0f419de260660` |
| `surfaces/mcp/testdata/mcp-descriptors-daemon-labs.json` | `cc820a988705bb9a80696458728d0b4511f87a1083b7a1a697793bd141f35e03` |
| `surfaces/mcp/testdata/mcp-descriptors-maximal.json` | `fbd759528a0c9c04e53269366209f4a51f122e10ce30f9192b19900499d5b139` |
| `surfaces/http/testdata/http-routes.json` | `30474cfb3324561bc08faa3a74a6af1cf14889e96be44017d67674e987bb4d27` |
| `surfaces/http/testdata/http-contract-labs.json` | `d1b8233d5e274c08d0f22fde81329f0789b237762dc0f55b47efb2b331cdf9b0` |
| `surfaces/testdata/stable-ops/manifest.json` (the Stable-11 canonical bytes; **none** of the three tools is in it — they are Labs) | `ddc8a3d1bab2c421f7d283634b96862e6d4bb80e8f1f3f63a9861b98f77caeab` |

`surfaces/client/testdata/` does not exist at the candidate, and no `engine/agenttools/*/testdata`
directory exists either (the only `*golden*` under any `testdata/` is
`engine/review/testdata/comment_golden.md`, unrelated).

### 7.2 Canonical result bytes — per tool

**`search_semantic` (typed unavailable response).** No checked-in golden. The bytes are pinned
in-process by `surfaces/parity_test.go:323-347` (`TestSemanticSearch_UnavailableParity`:
CLI, MCP and HTTP byte-identical for the queries `ParseGraph`, `p.A`, `missing`) and by
`engine/search/semantic_test.go:15-43`. They are fully determined by
`engine/search/semantic.go:37-42` (field order `query`, `available`, `reason`, `hits`),
`:59` (the graceful-skip return) and `MarshalSemantic` at `:110-115` (`json.Marshal`, nil hits
→ `[]`), with `UnavailableReason` at `:14`. The exact bytes for query `ParseGraph` (117 bytes):

```
{"query":"ParseGraph","available":false,"reason":"no embedder configured; run `graphi setup-embedder ...`","hits":[]}
```

| Query | Bytes | sha256 |
|---|---|---|
| `ParseGraph` | 117 | `29db37adbb3e90c69e88e776a0b3bd6bacef09c2172fb13c6e2f3359b1fdf5b4` |
| `p.A` | 110 | `24757ef4f2e695fa8276efdce9de6e9968f600190a31c276afe7da18029169d9` |
| `missing` | 114 | `642436e5ce15296b624d3e7961897bbe76a1f77835c58a7da9c45c9082f0f45e` |
| `hello greeter` | 120 | `8d7ff24f56ace5a2f7ec9e5a5792dba6cdff4a8a4b4de27f76d203b776ab4a11` |

(`hello greeter` is the query the agent-intel golden uses for `search_hybrid`; it is listed so
SW-265's five-state work has the unavailable-state twin of the same query.) The sha256 values
were computed over the literal bytes above with `shasum -a 256`, not by running the test —
`Direct.SemanticSearch` (`surfaces/client/direct.go:376-390`) has no other code path on the
default build, so the literal *is* the test's output.

**`search_hybrid` and `task_context`.** No checked-in golden. Both are pinned **in-process only**
by `surfaces/agentintel_golden_test.go`: `TestAgentIntel_ByteStableAcrossStoreConditions`
(`:92-131`, warm / warm-again / cache-rebuilt / fresh re-index all byte-identical on SQLite) and
`TestAgentIntel_MemAndSQLiteBackendsAgree` (`:135-154`, which deliberately **excludes**
`search_hybrid` — `:20-26` — because its candidate retrieval rides the backend search port whose
recall differs between MemStore substring match and SQLite FTS). The invocations are
`c.SearchHybrid(ctx, SearchHybridParams{Query: "hello greeter"})` (`:66`) and
`c.TaskContext(ctx, TaskContextParams{Task: "Hello"})` (`:49`) over `corpus/fixtures/go`,
rooted at the fixture dir (`:33`).

**Their sha256 was captured at the candidate** with the harness below (no product code): the
builder's sandbox could not compile, so the orchestrator dropped the harness into `surfaces/` as
a `_test.go`, ran `CGO_ENABLED=0 go test ./surfaces -run TestSW257CaptureBaselineBytes -count=1`
on branch `sw-257-s0-scope-lock-baseline` (tree = candidate + this story's three files), hashed
the four output files with `shasum -a 256`, and deleted the harness again. To re-verify, repeat
those steps — the harness is reproducible from this page alone.

```go
package surfaces_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/samibel/graphi/core/graphstore"
)

func TestSW257CaptureBaselineBytes(t *testing.T) {
	dir := os.Getenv("SW257_OUT")
	if dir == "" {
		t.Skip("SW257_OUT unset")
	}
	write := func(name string, b []byte) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), b, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	sq, err := graphstore.SQLiteFactory(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = sq.Close() }()
	indexCharFixture(t, sq)
	out := runAgentIntelOps(t, sq)
	write("search_hybrid.sqlite.bytes", []byte(out["search_hybrid"]))
	write("task_context.sqlite.bytes", []byte(out["task_context"]))

	mem := graphstore.NewMemStore()
	defer func() { _ = mem.Close() }()
	indexCharFixture(t, mem)
	memOut := runAgentIntelOps(t, mem)
	write("search_hybrid.mem.bytes", []byte(memOut["search_hybrid"]))
	write("task_context.mem.bytes", []byte(memOut["task_context"]))
}
```

| Tool | Backend | Bytes | sha256 at the candidate (captured 2026-08-30) |
|---|---|---:|---|
| `search_hybrid` (`Query: "hello greeter"`) | SQLite | 1590 | `0ec5fd56cf662defc4efe69ff9f7be2fe68645bc71bcc5e102535bed5888ae40` |
| `search_hybrid` (`Query: "hello greeter"`) | MemStore | 1837 | `b5852d726aedf528a4bf00c64e5071239a13d437b5991a6477a608038cabdf35` |
| `task_context` (`Task: "Hello"`) | SQLite | 3509 | `6decc1160cc48d0abf2e91c99fb5953cbab99e1c43f7932f7dae600be4b2e6c3` |
| `task_context` (`Task: "Hello"`) | MemStore | 3509 | `6decc1160cc48d0abf2e91c99fb5953cbab99e1c43f7932f7dae600be4b2e6c3` |

`task_context` is byte-identical across backends; `search_hybrid` is not (1590 vs 1837 bytes),
which is exactly the MemStore-substring vs SQLite-FTS candidate-recall difference that
`TestAgentIntel_MemAndSQLiteBackendsAgree` excludes it for. Later stories prove byte-identity
against the **SQLite** row (the shipped store); the in-process tests above remain the gate that
fails `go test ./surfaces` before any hash is consulted.
(The installed `~/.local/bin/graphi` on the measuring machine is built from `4f14966`, modified,
not from the candidate — it must not be used to fill the table.)

---

## 8. Rollback contract for the whole track (AC-5)

### 8.1 The invariant

**`GRAPHI_EMBEDDER` unset ⇒ the lexical path is unchanged and no default-path code reads the
vector store.** The guard is structural, not a flag check sprinkled through the code:

- `cmd/internal/runtime/runtime.go:772-781` — the search service is returned **before** the
  registry, the index or the vectors table exist when the selector is empty or unknown. The only
  production call of `embed.OpenSQLiteVectorTable` is `runtime.go:793`, below that return; the
  only production call of `embed.NewSQLiteVectorTableDB` is `cmd/graphi/query.go:329`, below the
  `--semantic` + embedder gates at `query.go:301-316`. Every other caller of either constructor
  is a `_test.go` file (`engine/embed/sqlite_vectorstore_test.go`,
  `engine/search/semantic_reload_test.go`, `internal/canary/reload_test.go`).
- `engine/search/semantic.go:57-60` — with no registry, `SemanticSearch` returns the typed
  unavailable response and never touches the index; lexical `Search` is a different method on
  the same service and is not consulted by it.
- `engine/embed/generate.go:77-79` — generation is a no-op without a configured registry.
- `engine/agenttools/hybridsearch/hybridsearch.go:11-12` — `search_hybrid` "never touches"
  semantic search at this baseline, so its default bytes cannot depend on the vector store by
  construction. SW-263/SW-264 change that **behind** the selector: the retrieval instance they
  compose must degrade to exactly today's lexical candidates when no embedder is configured, and
  §7 is what that is measured against.

Consequence: rolling the entire track back on a running install is `unset GRAPHI_EMBEDDER` plus
a restart. No schema on the default path depends on the `vectors` table (it is created lazily,
`CREATE TABLE IF NOT EXISTS`, only when an embedder opens it), so an install that never set the
selector never has one, and an install that did can leave it in place — it is scoped by
`embedder_id` and read by nobody on the default path.

### 8.2 Per-story kill switches

The mechanism that exists today is the executor-seam switch (`docs/executor-seam-rollback.md`):

- `GRAPHI_CANARY_ALL=legacy|shadow|active` — every migrated operation without a variable of its
  own (`cmd/internal/runtime/runtime.go:524`).
- `GRAPHI_CANARY_<OPERATION>` — one operation; wins over `ALL`
  (`runtime.go:518`, `EnvCanaryModeFor` at `:534-536`). The name is the upper-cased catalog id.
- Read once at startup by `ApplyCanaryMode` (`runtime.go:550-575`); an unrecognised value fails
  the session rather than defaulting. Non-migrated operations always answer `legacy`
  (`surfaces/client/canary.go`, `CanaryModeFor`). Shipped default: `shadow`
  (`canaryModeDefault`, SW-244).
- Rollback is a value change only: no schema, persisted state, cached artifact or wire
  identifier is keyed on the position.

| Story | Switch | Position that restores today's bytes | Status |
|---|---|---|---|
| (today) any of the ten migrated operations | `GRAPHI_CANARY_<OP>` / `GRAPHI_CANARY_ALL` | `legacy` + restart | live at the candidate |
| (today) `search_hybrid` specifically | `GRAPHI_CANARY_SEARCH_HYBRID` | `legacy` + restart | live at the candidate |
| (whole track) semantic on/off | `GRAPHI_EMBEDDER` | unset + restart | live at the candidate (§8.1) |
| SW-264 `search_hybrid/2`, `task_context/2` | _to be filled by SW-264_ (expected: `GRAPHI_CANARY_SEARCH_HYBRID=legacy` for the executor path; the `/2` contract's own selector for the renderer) | _to be filled_ | not started |
| SW-265 `search_semantic` migration, `semantic status` | _to be filled by SW-265_ (expected: `GRAPHI_CANARY_SEARCH_SEMANTIC`, created by adding the id to `migratedOperations`) | _to be filled_ | not started |

A row is filled **by the story that lands the switch**, in the same change, with the
`file:line` of the variable and the test that proves the `legacy` position reproduces the §7
bytes. A story without a row here has no rollback and does not merge (decision record, point 5).

---

## 9. What `internal/eval` measures — and what no number exists for

`internal/eval` is the **token-parity** harness (`EvalName = "token-parity-eval"`,
`internal/eval/report.go:4`). Its unit is a `Case` with a `GraphiContext` and a
`BaselineContext` string (`internal/eval/dataset.go:22-30`); `Run` counts tokens of both and
reports the ratio per case and in aggregate (`report.go:7-13`, `:56-60`), gated by a coverage
matrix over `query.Operations` plus `search` (`dataset.go:15-20`). Both contexts are **static
strings in `internal/eval/cases.json`** — the harness measures the size of a frozen graphi
payload against a frozen whole-file baseline. It does not run a retrieval, it does not know
what the right answer to a query is, and it cannot tell a relevant hit from an irrelevant one.

Therefore, at the candidate:

- **No retrieval-quality Recall@k, MRR or NDCG number exists** anywhere in the repository —
  i.e. no metric over labelled *queries* against ranked *search results*. `grep -rn
  'Recall\|MRR\|NDCG' --include='*.go'` returns matches in 14 files, and every one is
  something else:
  - `engine/embed/hnsw_test.go:89` — `TestHNSW_RecallAt10`, an ANN index checked against
    brute force on random vectors; no queries, no relevance labels;
  - `engine/analysis/taint/recall_test.go`, `engine/ingest/taint_vulngo_e2e_test.go` — the
    taint-flow recall gate over a labelled *flow* corpus (PB-001 §10), a static-analysis
    detection rate;
  - `internal/jvmgroundtruth/{groundtruth,capture,groundtruth_test}.go` — precision/recall of
    the JVM declared-type *resolver* against `go/types`-style ground truth (edges, not search);
  - `engine/link/clausebydir_test.go`, `engine/ingest/parseerror_test.go` — "recall" in the
    sense of edge/file coverage after a linker or parse-error fix;
  - `engine/memory/{memory,ledger,memory_test,provenance_test}.go`,
    `surfaces/client/direct.go`, `surfaces/parity_test.go` — the `RecallMemory` /
    `memory recall` verb and its ledger hook, not a metric.
  None runs a query through `search`, `search_hybrid` or `search_semantic` and scores the
  ranked hits against judged spans; that harness is SW-258.
- There is no `internal/eval/retrieval` package and no `docs/eval/retrieval-budgets.json`;
  `docs/eval/` holds `hero-budgets.json`, `hero-protocol.md`, `reference-scenario.json`, `p0/`
  and `runs/`.
- No labelled query set exists for any corpus repository.

SW-258 creates the instrument; SW-266 produces the only token-savings figure this track will
ever quote. Any number attributed to semantic search before those two land is not from this
repository.

---

## 10. What does not exist today (verified, not assumed)

| Claim | How it was checked at the candidate |
|---|---|
| No `SourceSpan` and no end position in `core/parse` | `grep -rn 'SourceSpan\|EndLine\|EndCol\|end position' core/parse` → no matches. `ParseResult` (`core/parse/parse.go:48-71`) carries `Meta`, `Root`, `Nodes`, `Edges`, `References`, `PendingRefs`, `Imports`; the only position field on the parse boundary is `PendingRef.Line` (`parse.go:93-95`). `model.Node` exposes `Line()` and `Column()` only (`core/model/node.go:136-139`) — a start point, never a range. SW-260 adds the non-identity span. |
| No agent tool consumes `SemanticSearch` | `grep -rn SemanticSearch engine/agenttools engine/context` → no matches. The only semantic reference under either tree is prose: `engine/agenttools/hybridsearch/hybridsearch.go:11-12` ("this tool never touches it"). Outside those trees the callers are `surfaces/client/direct.go:376-390` (the `search_semantic` operation itself) and tests. |
| No retrieval eval harness | §9. |
| No document fingerprint / generation on the vector index | §4.2. |
| No integer-quantised scores | §4.3; `embed.Hit.Score` is `float64` (`vectorstore.go:137-140`). |
| No `static:` embedder | The scheme table (`embed.RegisterScheme`, `engine/embed/defaults.go:112`) is populated by exactly two production registrations: `engine/embed/ollama/ollama.go:44` and `engine/embed/onnx/onnx.go:30`. No `static` scheme exists; SW-262 adds it and is gated on SW-259's GO. |

---

## 11. How later stories use this page

- **Byte identity.** A story that touches a shared path re-runs `go test ./surfaces` (the
  in-process pins of §7.2) and compares its output against the captured hashes in §7.2.
  A moved hash on the default build is a review failure unless the story's own page says why,
  with D7 ceremony (backlog D7DEBT-001).
- **Descriptors.** A moved sha256 in §7.1 means a descriptor or route changed; it must come
  with `GRAPHI_UPDATE_GOLDEN=1` in the story and a coverage-matrix regeneration
  (`go run ./cmd/coverage -generate`).
- **Rollback.** Every story that adds a switch fills its §8.2 row.
- **Corpus.** SW-258 cites §2 by repository name and pinned sha; a corpus bump is a manifest
  change with its own baseline re-measurement.
- **Gates that re-run on any product-byte change** are listed in the decision record, point 4.
