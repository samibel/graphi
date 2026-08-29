# AX-00 — extension-kernel baseline freeze

**Story:** SW-220 (AX-00) · **Spec:** extension-platform-kernel · **Baseline commit:** `284d733`

Before the strangler refactor toward an Operation Catalog begins, "no damage" has to be
**measurable**. graphi already had strong per-invariant gates and route/tool snapshots. What
it did not have was (a) golden coverage of the *full* advertised tool/verb metadata, (b)
recorded canonical result bytes for the read-only Stable operations, (c) one place to read a
commit's gate posture, and (d) the `surfaces/client` fan-out written down.

This page is the index of what was frozen and how to work with it.

---

## 1. What is frozen

### 1.1 MCP wire names and descriptors — per binding profile

| Artifact | Contents |
|---|---|
| `surfaces/mcp/testdata/mcp-wire-names.json` | every advertised tool name + its tier tag, `StableOperations`, `StableMCPToolNames()`, the strict-query operation list |
| `surfaces/mcp/testdata/mcp-descriptors-stable.json` | profile-static Stable catalog — what an **unbound** stdio session answers `tools/list` with while the first index runs |
| `surfaces/mcp/testdata/mcp-descriptors-maximal.json` | profile-static Stable+Labs registry (the complete `-labs` catalog before any narrowing) |
| `surfaces/mcp/testdata/mcp-descriptors-stdio-stable.json` | bound stdio default profile, fully wired `client.Direct` |
| `surfaces/mcp/testdata/mcp-descriptors-stdio-labs.json` | bound stdio `-labs` profile |
| `surfaces/mcp/testdata/mcp-descriptors-daemon-stable.json` | bound daemon profile (the `DaemonClient` wired-RPC allow-list) |
| `surfaces/mcp/testdata/mcp-descriptors-daemon-labs.json` | bound daemon `-labs` profile |

Each descriptor is frozen with its **name, tier tag, description, `inputSchema` and
annotations**, in the advertised order. Any unknown descriptor key is captured under `extra`,
so a future field cannot be dropped by the very test meant to catch drift.

**Measured tool counts at the baseline** (all from the live registry, not from prose):

| Profile | Tools |
|---|---|
| profile-static Stable | 11 |
| profile-static Stable+Labs (`maximal`) | 56 |
| bound stdio, Stable | 11 |
| bound stdio, `-labs` | 44 |
| bound daemon, Stable | 7 |
| bound daemon, `-labs` | 25 |

> **Prose reconciliation — done by SW-225 (AX-05).** AX-00 measured a disagreement it was
> not allowed to fix: `docs/FEATURES.md` said 44 MCP tools and the delivery portfolio's
> `context/architecture.md` said "up to 45 = 11 stable + 34 labs", while the live registry
> advertises **56** names. Both prose figures were describing the *narrowed stdio Labs*
> catalog rather than the registry, and both were stale. `docs/FEATURES.md` now states 56
> (11 Stable + 45 Labs) for the registry **and** carries the measured narrowing figures
> above, so the two numbers can no longer be mistaken for each other; the portfolio context
> file was corrected in the same change. The table above stays the citation — a prose figure
> in this project is only as good as the artifact it points at.

### 1.2 HTTP operation / capability list

| Artifact | Contents |
|---|---|
| `surfaces/http/testdata/http-routes.json` | the ordered route table with each route's resolved capability and tier; mixed routes (`/query/{op}`, `/analyze/{analyzer}`, `/events?analyzer=`) are probed at **both** tiers |
| `surfaces/http/testdata/http-contract-stable.json` | the live `GET /contract` document under the shipped default (no Labs opt-in) — 11 stable resources |
| `surfaces/http/testdata/http-contract-labs.json` | the same document with `GRAPHI_HTTP_LABS=1` |

Tier classification comes from `surfaces/mcp.IsStableOperation` — the same membership check
`capabilityGuard` applies at runtime — so the golden cannot hold a second opinion.

### 1.3 CLI help of the user-facing verbs

`cmd/graphi/testdata/cli-help.txt` — the top-level blurb, the verb listing with its
Stable/Labs markers, and every long verb's and short alias's own help. Plain text on purpose:
this is what a user sees, and a diff of it needs no decoding. The `[labs] ` markers are
**derived** (`stabilityMarker` → `mcp.IsStableOperation`), so a re-tiering moves this file.

### 1.4 Canonical result bytes — read-only Stable operations

`surfaces/testdata/stable-ops/` holds the **raw** canonical bytes of each read-only Stable
operation over the committed `corpus/fixtures/go` fixture, plus a `manifest.json` recording
the request that produced each artifact, its length and its sha256.

Eleven operations: `agent_brief`, `callees`, `callers`, `change_risk`, `definition`,
`explain_symbol`, `impact`, `neighborhood`, `references`, `related_files`, `search`.

Deliberate choices worth knowing:

- **The twelfth Stable operation, `index`, is a lifecycle operation, not a read-only one.**
  AC-3 scopes this artifact to the read-only ops. `index` is represented in the manifest by
  the sha256 of the whole indexed graph, so a graph-level change is still visible without
  committing an 8 KB blob that would churn on every fixture edit.
- Artifacts are **not** re-indented. The canonical serialization *is* the thing being frozen;
  pretty-printing would forgive a formatting change a client can observe.
- Anchors are chosen to be representative: `callees` is anchored on `ChainA` (which calls
  `ChainB`) rather than on `Hello` (which calls nothing). `references` stays on `Hello` and
  records the **`empty`** outcome honestly — the pinned Go fixture produces no reference
  edges, and the empty envelope is itself contractual wire shape.
- `search` carries a SQLite FTS5 BM25 `rank` (`-2.5615254503350093` at the baseline). It is
  deterministic arithmetic in pure-Go SQLite, but it is the one field in these artifacts whose
  value comes from a ranking implementation rather than from the graph — if it ever moves,
  read it as a ranking change, not as a graph change.

---

## 2. Regenerating a golden — deliberately

There is **one** command shape, and it is never automatic:

```bash
GRAPHI_UPDATE_GOLDEN=1 go test ./surfaces/mcp -run TestAX00      # MCP descriptors + wire names
GRAPHI_UPDATE_GOLDEN=1 go test ./surfaces/http -run TestAX00     # HTTP routes + /contract
GRAPHI_UPDATE_GOLDEN=1 go test ./cmd/graphi -run TestAX00        # CLI help
GRAPHI_UPDATE_GOLDEN=1 go test ./surfaces -run TestAX00          # canonical Stable-op bytes
GRAPHI_UPDATE_GOLDEN=1 go test ./internal/importfanout           # fan-out baseline
```

Without `GRAPHI_UPDATE_GOLDEN=1` a mismatch is a **test failure** naming the file and the
command. A golden that rewrites itself on mismatch protects nothing — it records whatever the
code just did. The `baseline-freeze` workflow runs `git diff --exit-code` after the golden
tests for exactly this reason: an artifact rewritten without the opt-in fails CI.

Verify the whole set, plus reproducibility:

```bash
CGO_ENABLED=0 go test ./surfaces/... ./cmd/graphi ./internal/importfanout ./internal/evidence -run 'TestAX00'
```

---

## 3. Aggregated protection-gate posture

```bash
go run ./cmd/evidence -gate-view [-results <dir>] [-sha <commit>]
```

Renders the seven invariant gates in one table. It is a **view**: it executes no gate and
re-derives no verdict. The declaration lives in [`protection-gates.yaml`](protection-gates.yaml).

| Gate | Owning job |
|---|---|
| `parity` | `parity.yml` › `parity-matrix` (dispatch/nightly only) |
| `cgo` | `cgoconformance.yml` › `cgo-free-conformance` |
| `egress` | `canary.yml` › `gate` |
| `layer` | `release.yml` › `layer-direction` |
| `coverage` | `coverage-matrix.yml` › `coverage-matrix` |
| `repro` | `release.yml` › `release` |
| `binary-size` | `bench.yml` › `bench-budget-gate` |

**The rule:** an absent, unreadable, statusless, invalid or unbacked result renders
**UNKNOWN**, and UNKNOWN is never rendered as PASS — the `internal/evidence` honesty rule
applied to gates. A `PASS` claimed without both an `evidence_uri` and a `sha` is downgraded
to UNKNOWN. `parity` is UNKNOWN on a pull request **by design** (it shallow-clones public
repositories and is deliberately outside the zero-egress PR posture); reading that as
anything else is the mistake this view exists to prevent.

Result records are `<results-dir>/<gate-id>.json`:

```json
{"gate":"cgo","status":"PASS","evidence_uri":"https://…/runs/123","sha":"<commit>","run":"https://…"}
```

Why a view and not an aggregating CI job: these seven gates live in seven **separate workflow
files**, and GitHub's `needs:` cannot reference a job in another workflow. An aggregating job
would have to re-run or re-implement them — which SW-220 forbids. What *is* wired into CI is
the declaration's integrity: `TestProtectionGatesDeclaration_IsIntactInThisCheckout` runs in
the ordinary test gate, so a workflow or job renamed by a later PR breaks on that PR instead
of turning a gate into a permanent, plausible UNKNOWN.

---

## 4. Import fan-out metric — reported, not enforced

`surfaces/client` directly imports **41** internal packages at the baseline (over 8 non-test
files). The full set is checked in at [`ax00-import-fanout.json`](ax00-import-fanout.json);
recording the *set*, not only the count, is what makes a later diff readable — "41 → 38" says
nothing about which three went away.

This is a **metric, not a ratchet**. `go test ./internal/importfanout -v` prints the current
number and what moved; nothing fails when it changes. AX-04 (generic executor) and AX-07
(RuntimeBuilder) are the stories expected to move it, and a threshold adopted before anyone
knows the healthy range mostly teaches people to route around it. Turning it into a gate is a
separate, deliberate decision.

The measurement uses `go/ast` rather than `go list` so it stays hermetic inside an ordinary
test. The one real difference is build tags: it counts imports declared in every non-test
`.go` file, including any behind a build constraint. For a *coupling* metric that is the more
honest answer — an import hidden behind `//go:build graphi_broad` is still a package this code
knows about. Test files are excluded: a test importing a helper is not production coupling.

At the baseline the two methods agree exactly (`go list -deps=false` over `./surfaces/client`
also reports 41 internal imports).

**Addendum (SW-253, AX-16a, 2026-08-29) — the separate, deliberate decision has been taken.**
Since v0.11.0 the number stood at **44** (+`core/registry`, `engine/extpack`, `engine/opcatalog`),
so `go test ./internal/importfanout` now also runs a **ratchet** against its own declaration,
[`ax16-import-fanout-ceiling.json`](ax16-import-fanout-ceiling.json): ceiling **44**, every one
of the 44 edges declared with a category and a reason, and the targets **≤ 30** (intermediate)
and **< 20** (final) recorded beside it as intent — the build fails when the direct fan-out rises,
when an undeclared edge appears (even at an unchanged count), or when the count falls without
the ceiling being lowered with it; the transitive internal closure is measured and logged on the
same line (`direct 44 (ceiling 44) · transitive N`) as the anti-gaming instrument but is not
gated. The baseline file above is history and is unchanged.

---

## 5. What this story deliberately did not do

- **No production code changed.** Tests, golden artifacts, CI tooling
  (`internal/goldenfile`, `internal/importfanout`, the `cmd/evidence -gate-view` mode) and one
  workflow. The shipped `cmd/graphi` binary is byte-identical to the pre-story build.
- **No refactoring, no catalog, no registry change.** AX-00 only measures.
- **No new blocking gate on fan-out.**
- **No re-baselining of `docs/eval/hero-budgets.json`** — known separate debt.
