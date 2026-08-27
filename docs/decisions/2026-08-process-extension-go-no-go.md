# Decision: process extensions (ADR 0013 trust tier C) — **NO-GO** for phase 1

> ## ⛔ NO-GO — graphi stays on rule packs (tier A) and static modules (tier B)
>
> The SW-231 spike was built, measured and works. It is not being adopted.
> Four of the plan's five go/no-go criteria are met; the fifth — *"a real user
> case justifies the added complexity"* — is **not**, and it is a conjunction
> member, not a tiebreaker. ADR 0013 D2 planned for exactly this answer:
> *"'no-go, graphi stays on rule packs and static modules' is a valid, planned
> outcome, not a failure."*
>
> The spike code is retained, unwired, as the **evidence for this record**.
> Removing it is one `rm -r` plus one two-line sweep (§6), and two tests inside
> the spike prove that the removal would be complete.

**Status:** decided · **Date:** 2026-08-27 · **Story:** SW-231 (AX-11) ·
**Spec:** Extension Platform Kernel · **Governing ADR:**
[0013 — Extension trust tiers](../adr/0013-extension-trust-tiers.md) ·
**Measured at:** `ec7f693` on branch `sw-231-process-extension-spike`, branched
from `main` at `abfa928`. (The later commit on that branch adds this document,
one error-message refinement and the catalog-reader allowlist entry of §5.8;
none of them moves a number here — `engine/exthost` is not in the shipped import
closure either way, which is exactly the property §2.3 measures.)

---

## 1. What was built

A complete, working tier-C host and one real extension:

| | |
|---|---|
| Host | `engine/exthost` — descriptor loading + SHA-256 pinning, spawn, handshake, call, port proxy, limits, shutdown |
| Example extension | `extensions/example-analyzer` — a separate executable computing a per-path census of symbol-search matches |
| Protocol | `graphi-ext/1`: `graphi-ext/1 <byte-length>\n<json>` frames over the child's stdin/stdout, bidirectional |
| Descriptor | `graphi.extension/v1alpha1` — the SW-229 pack manifest's schema version, field names, `artifact.{path,sha256}` pinning, `extpack.APIRange`, `extpack.Capabilities` and validators, plus a `ports` list and process `limits` |
| Judged by | `engine/extpack/conformance` (SW-230), which passes on all six applicable checks |

It answers, end to end: activation → hash verification → spawn → handshake →
call → the extension asks the host for `graph.search` → the host answers → the
extension returns findings → the host wraps them in provenance.

## 2. The measurements

Measured by `go test ./engine/exthost -run TestSW231_AC5 -v` and
`go test ./engine/exthost -bench BenchmarkSpike` on darwin/arm64 (Apple M2 Max,
go1.26.6). Numbers deliberately live **here** and not in
`docs/eval/hero-budgets.json`: a disposable spike must never become a row a
release gate depends on (standards: two measurement instruments stay separate).

### 2.1 Latency

| What | Median | Range (n=11) |
|---|---|---|
| Descriptor load + SHA-256 verify of the artifact | **2.58 ms** | — (5 runs) |
| Spawn + handshake | **17.26 ms** | 12.92 – 221.81 ms |
| First call round trip (warm process) | **0.217 ms** | 0.147 – 0.705 ms |
| `BenchmarkSpikeStartHandshake` | **22.63 ms/op** | 30 iterations |
| `BenchmarkSpikeCall` | **0.224 ms/op** | 30 iterations |

**Read this as: ≈20 ms to activate, ≈0.22 ms per call thereafter.** The
comparison that matters is with tier B, where a static module's "activation" is
zero and its call is a Go function call — sub-microsecond. The process boundary
therefore costs roughly **three orders of magnitude per call** and about **20 ms
of fixed cost per activation**, of which 2.6 ms is re-hashing a 3.9 MB artifact
that has to be re-hashed on every activation because pinning is only meaningful
if it is checked every time.

The 222 ms outlier is a cold first spawn (page-cache miss on the artifact). It
is reported rather than trimmed: a user's first activation after boot pays it.

### 2.2 Memory

| What | Measured |
|---|---|
| Child process RSS | **4,944 KiB (4.8 MiB)** |
| Host heap retained per live extension | **71,244 bytes (~70 KiB)** |
| Example extension artifact (darwin/arm64) | **3,888,322 bytes (3.71 MiB)** |
| Example extension artifact (linux/amd64) | **3,943,638 bytes (3.76 MiB)** |

The host side is cheap and bounded by design (a 32 KiB read buffer and an 8 KiB
stderr ring per extension). The **child** side is the honest cost: ~5 MiB of RSS
per activated extension, which is a real number on a laptop running several MCP
servers, and it is per extension rather than amortised.

### 2.3 Host binary size — **zero delta, byte-identical**

This is the criterion SW-230's retracted approval made non-negotiable, so it was
measured both ways with the canonical shape from `internal/release.CanonicalBuildArgs`:

```
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -buildvcs=<…> \
  -tags "<DefaultGrammarSubsetTags>" \
  -ldflags "-X …internal/version.Version=dev" -o <out> ./cmd/graphi
```

| Build | `-buildvcs=true` size | `-buildvcs=false` size | `-buildvcs=false` SHA-256 |
|---|---|---|---|
| `main` @ `abfa928` | 35,247,321 B | 35,247,329 B | `51f37a21695cbdc167b312d79c14a862d509fde8f4219fab8fd3dd8e8d08a13a` |
| spike @ `ec7f693` | 35,247,321 B | 35,247,329 B | `51f37a21695cbdc167b312d79c14a862d509fde8f4219fab8fd3dd8e8d08a13a` |
| **Delta** | **0 bytes** | **0 bytes** | **identical** |

`-buildvcs=true` produces the same SIZE but different BYTES, because that flag
stamps the git revision and the revision changed with the spike's own commit.
`-buildvcs=false` removes that stamp and the two binaries are **byte-identical**,
which is the property AC-4 actually asks for.

Headroom against the CI budget (36,100,000 B) is therefore unchanged at
**852,679 bytes / 2.36 %** — note that this is measured locally on go1.26.6 and
sits slightly below the 2.71 % figure the story quotes; the *delta* is what this
story is accountable for, and it is zero.

The zero is structural, not lucky: nothing in `cmd/`, `surfaces/` or the rest of
`engine/` imports `engine/exthost`, which
`TestSW231_AC4_SpikeIsNotInTheShippedImportClosure` checks with `go list -deps`
on every run. A package absent from the import closure contributes no code, no
symbol and no `init`.

### 2.4 Packaging effort

| What a tier-C author must produce | Tier A (rule pack), for comparison |
|---|---|
| A compiled executable **per GOOS/GOARCH** | none — a pack is data |
| A SHA-256 per artifact, by hand | `graphi extension install --sha256` computes and records it |
| A 21-line descriptor, hand-written | `graphi extension init` scaffolds a validate-clean manifest |
| 309 lines of Go for the example, ~32 of them pure protocol plumbing | 0 lines of code, by definition |
| A reimplementation of the frame codec if the author is not writing Go | n/a |

Four packaging findings, each pinned by a test rather than left as prose:

1. **No scaffold exists and none can be added cheaply.** `extpack.Kind` is a
   closed tier-A vocabulary and `extpack.Manifest.Validate` rejects anything
   else, so `graphi extension init --kind process-analyzer` cannot be expressed
   without putting an *executable* kind into the type whose entire guarantee is
   that it cannot execute. `exthost.KindProcessAnalyzer` therefore lives outside
   `extpack`, and the tier-C author starts from a blank file.
2. **The descriptor pins exactly one artifact.** There is no `artifacts:` list,
   so one descriptor cannot describe a multi-platform extension. Pinned by
   `TestSW231_AC5_PackagingEffort`.
3. **Operation ids have no namespace.** `conformance.wireIdentifier` accepts
   `[a-z][a-z0-9_]*` — no dot, no vendor prefix. A third-party operation lands
   in the same flat namespace as graphi's own twelve, with nothing in the
   mechanism preventing two extensions from claiming one id. The example is
   `example_symbol_census`, not `example.symbol_census`, and
   `TestSW231_AC5_TierCOperationIdsHaveNoNamespace` asserts the refusal as data
   so the finding cannot drift away from the code.
4. **The protocol has no configuration channel.** The host spawns the binary
   with no arguments and an empty environment; a descriptor pins a *binary*, not
   a command line. A user cannot configure an activated extension at all.

### 2.5 gRPC, as the comparison the plan asked for — **costed, not implemented**

The plan asks for "stdio-framed JSON **or gRPC as a comparison**". gRPC was
**not** implemented, and the reason is itself a measurement:

- Adopting `google.golang.org/grpc` + `google.golang.org/protobuf` puts
  `golang.org/x/net/http2` and a full RPC stack into the module. If tier C ever
  shipped, the *host* half would be in the default binary's import closure — and
  the default binary has 852,679 bytes of headroom. A multi-megabyte dependency
  family is not a rounding error against that; it is the SW-230 regression again,
  with a bigger cause. **This figure is an estimate from the dependency's shape,
  not a measured build** — measuring it would have required adding the dependency
  to `go.mod`, which is the change being evaluated.
- It requires `protoc` (or `buf`) in the build for an author *and* for graphi,
  against a project whose build is "plain `go` commands, no Makefile".
- It puts an HTTP/2 stack on the default path of a product whose headline claim
  is zero egress. Loopback-only gRPC is possible and `surfaces/guard` would still
  refuse a non-loopback dial — but the claim is currently defensible because the
  stack is *absent*, not because it is *restrained*.

Against that, the stdio codec is ~200 lines (`protocol.go`), imports only the
standard library, and its length-prefixed framing gives a property gRPC would
have hidden inside its own flow control: **the maximum response size is enforced
on the declared length, before the body is read.**

**Comparison verdict: for a local, single-consumer, request/response boundary,
stdio-framed JSON is the cheaper and more auditable choice, and gRPC's
advantages (streaming, multiplexing, cross-language codegen) address problems
this boundary does not have.** If tier C is ever revisited, this conclusion
should be re-derived rather than inherited — it depends on the binary budget.

## 3. The go/no-go, criterion by criterion

The criteria are the source plan's (§AX-11 *Go/No-Go-Kriterien*), restated by
ADR 0013 §4.2's "Tier C shipping at all" revisit trigger.

| # | Criterion | Verdict | Evidence |
|---|---|---|---|
| 1 | **The default path is unaffected** | ✅ **MET** | Byte-identical binary, 0-byte size delta (§2.3); nothing imports the package; `layerguard` PASS; `coverage -check` PASS with no matrix row added; egress canary unaffected (no `net` import, no dialer — the spike uses `os/exec` and `os.Pipe` only) |
| 2 | **No CGo dependency** | ✅ **MET** | The whole spike builds and its full suite passes under `CGO_ENABLED=0`; `os/exec`, `os.Pipe`, `encoding/json` and `gopkg.in/yaml.v3` are pure Go. No new module dependency was added at all |
| 3 | **Comprehensible errors, hard response-size and time limits** | ✅ **MET** | 14 typed sentinels plus two REUSED SW-222 registry sentinels (`ErrMissingDependency`, `ErrUnsupportedOverride`) rather than parallel ones, each raised with the extension id, the two values that disagreed and the limit that was crossed; crash/hang/oversize proven against a real process in `journey_subprocess_test.go`; the size limit is checked on the *declared* frame length before the body is read |
| 4 | **Documented as trusted local code, not a sandbox** | ✅ **MET** | ADR 0013 D3 restated verbatim in `engine/exthost/doc.go`, in the example's package doc, and **carried in the data** as `Provenance.Trust`, so every consumer of a tier-C result holds the sentence |
| 5 | **A real user case justifies the added complexity** | ❌ **NOT MET** | §4 |

**Four of five is a NO-GO, not an 80 %.** Criterion 5 is the one that decides
whether the other four are worth having, and ADR 0013 §4.2 states the trigger as
a conjunction ending in *"**and** a real user case justifying the added
complexity"*.

## 4. Why criterion 5 fails

There is no named user, no named extension anyone has asked to write, and no
capability that tier A cannot express and tier B should not host. That is not an
oversight in the search for one — it is what the tier ladder predicts:

- **Everything read-only and data-shaped is tier A's job**, and tier A already
  ships: architecture rules, taint sources/sinks/sanitizers, with
  framework-detection, query-presets, classification-rules and export-profiles
  already booked in the backlog. A rule pack executes nothing, costs no process,
  needs no per-platform build, and its rollback is byte-identical by contract.
- **Everything that needs code is tier B's job**, and SW-227's `RuntimeBuilder`
  plus SW-230's conformance harness made adding one cheap: a module directory, a
  registration, and contract tests, with no dispatch or descriptor edits. The
  spike's own example is 309 lines of Go, of which ~32 exist *only* to speak the
  protocol; as a tier-B module the same analyzer would be the other ~277 with
  none of the process cost.
- **The one thing tier C uniquely offers is third-party authorship** — and ADR
  0013 D3/T2 say plainly what that costs: a subprocess is trusted local code
  with the user's full OS rights, unmitigated by construction. Offering that
  without a user asking for it is spending the project's trust budget on
  speculation.

ADR 0013 D2 named this failure mode in advance: *"An extension mechanism reached
for because a feature is inconvenient to build in-tree is the failure mode this
ordering exists to prevent."* Nothing is currently inconvenient to build in-tree.

## 5. Findings that survive the no-go

These are the durable output. They are facts about graphi discovered by building
the spike, and they remain true whether or not tier C is ever revisited.

1. **The zero-egress promise needs its scope stated wherever tier C is
   mentioned.** `surfaces/guard`'s own package doc is precise — it constrains
   graphi's **listeners and dialers** — and it has no authority over another
   process's syscalls. (Note in passing: `context/architecture.md` describes
   guard as blocking "outbound network/**exec**"; `guard.go` contains no exec
   chokepoint, and adding one would not help, because the extension's own
   syscalls happen after `exec` returns.) The egress canary measures the graphi
   process. **Activating a tier-C extension takes the user outside the scope of
   the zero-egress claim, and this must be said wherever the claim is said**
   (ADR 0013 T7).
2. **Killing a process is not killing a process tree.** The host kills the child
   it spawned. A grandchild survives. Doing better needs per-OS code
   (`Setpgid` + a negative-pid kill on unix, job objects on Windows), which is
   real, non-portable work nobody has costed. The host stays healthy either way
   — this is a resource-leak finding, not a containment failure.
3. **Hash pinning proves "the same bytes as when you installed it", not "the
   bytes the author intended".** Signing is deferred (ADR 0013 T1, N3). For
   tier A that residual is small because a pack cannot execute; for tier C it is
   the full T2 blast radius.
4. **The conformance harness transfers to a process boundary, and two of its
   checks get stronger there.** Determinism becomes a comparison across two
   independent OS processes rather than two calls into one warm object; port
   honesty becomes double-entry (the harness's gate records what the proxy asked
   for, and `Extension.PortViolations` independently records what the subprocess
   asked for). **Surface projection does not transfer**: the real projections
   live in `surfaces/mcp` and `surfaces/http`, above `engine` rank, so a tier-C
   contribution can only be projection-verified from a surfaces-rank test.
5. **The tier-A manifest schema cannot express tier C, and should not be made
   to.** The gap is `kind`, `ports` and process `limits`. Reusing the *machinery*
   (schema version, field names, artifact pinning, API range, validators, the
   `Bound` length discipline) while owning the three additions turned out to be
   the right seam, and cost no edit to shipped code.
6. **ADR 0013 D5 is enforceable at a process boundary, by rejection.** A result
   claiming `confirmed` is refused, not downgraded. Downgrading would produce a
   result whose tier nobody chose and would teach authors that the ceiling is
   advisory.
7. **The 2026-08-27 process-explosion lesson generalises.** Any package that
   spawns processes must refuse `os.Args[0]` and `*.test` before `exec`. This
   spike refuses both and pins the refusals; the daemon fix (`4328a5c`) was the
   first instance, not the only place the shape can appear.
8. **The catalog-reader boundary caught the spike, which is the boundary working.**
   `engine/opcatalog`'s `TestAX04_OnlyTheExecutorReadsTheCatalog` failed the
   first full-suite run of this branch and named `engine/exthost/descriptor.go`
   and `host.go`. The widening was made deliberately and with a written
   justification, per the rule that these boundaries may only be widened by
   ADDING, visibly — the spike reads the catalog for its `Port` and `Permission`
   vocabularies, because the alternative is a second port vocabulary for the
   process tier and a permission list nobody could audit against the first. Those
   two allowlist lines are the **only** reference to the spike outside its own
   directories and the deletion recipe in §6 names them;
   `TestSW231_AC6_SpikeIsConfinedToItsOwnDirectories` allows that one file by
   name (not by prefix) so a second exception cannot appear beside it.

## 6. Consequences

- **graphi stays on tiers A and B.** No `graphi extension activate` verb, no
  tier-C surface, no coverage-matrix row, no capability-manifest entry. Nothing
  changed for a user.
- **ADR 0013 needs no amendment.** It predicted this outcome and reserved the
  trigger; this record IS the "spike's own go/no-go record" §4.2 points at.
- **The spike code stays in the tree, unwired, as this record's evidence.** It is
  not a partial feature and must not be treated as one: it is advertised nowhere,
  imported by nothing, and has no default-path dependency at any point. Two
  tests keep that true —
  `TestSW231_AC4_SpikeIsNotInTheShippedImportClosure` (`go list -deps
  ./cmd/graphi` contains neither package) and
  `TestSW231_AC6_SpikeIsConfinedToItsOwnDirectories` (a `git grep` over the
  repository finds no reference outside `engine/exthost/`,
  `extensions/example-analyzer/`, `docs/decisions/` and the one
  individually-named exception of §5.8).
- **Deleting it is one command plus one sweep**, and the guards delete with it:

  ```
  rm -r engine/exthost extensions/example-analyzer
  # then remove the two `engine/exthost/…` lines from allowedImporters in
  # engine/opcatalog/shadowmode_test.go (see §5.8)
  ```

  Keep this document; a no-go record whose evidence is gone is an opinion.
- **`extensions/` now holds a Go main package** beside `vscode` and
  `github-action`. It is unranked for `layerguard` and is not part of any release
  artifact — `cmd/gen-packaging` and the release DAG build `./cmd/graphi` only.

## 7. What would reopen this

Only criterion 5 is open, so only criterion 5 reopens it. Per ADR 0013 §4.2 the
mechanism is this record, not a design review.

| Reopens when | What must be true |
|---|---|
| **A named user case appears** | A real, named person or team wants a specific extension that (a) tier A cannot express as data, (b) graphi should not host as a first-party module, and (c) they are willing to grant tier-C trust to, having been told in D3's words what that means |
| **Third-party authorship becomes the point** | Someone outside the project wants to ship an analyzer graphi will not carry. Until that person exists, tier C is a mechanism for a constituency of nobody |

If it does reopen, four things must be costed **before** any code, because this
spike proved they are the real work and none of them is protocol work: **binary
budget** (the host would then be in the default closure; today's headroom is
852,679 bytes), **distribution** (per-platform artifacts, one per descriptor,
plus signing), **operation-id namespacing**, and **process-tree cleanup**.

## 8. Reproducing every number here

```bash
# Latency, memory and packaging measurements (§2.1, §2.2, §2.4)
go test ./engine/exthost -run TestSW231_AC5 -v -count=1

# Benchmarks (§2.1)
go test ./engine/exthost -run '^$' -bench BenchmarkSpike -benchtime 30x -count=1

# The byte-identical binary claim (§2.3) — the buildvcs=false pair is the
# one that proves it; buildvcs=true differs only by the git revision stamp.
# TAGS is internal/release.DefaultGrammarSubsetTags, space-joined (build.go:47).
TAGS="grammar_subset grammar_subset_typescript grammar_subset_javascript \
grammar_subset_tsx grammar_subset_python grammar_subset_java grammar_subset_c \
grammar_subset_ruby grammar_subset_rust grammar_subset_php grammar_subset_c_sharp \
grammar_subset_kotlin grammar_subset_cpp grammar_subset_bash grammar_subset_sql \
grammar_subset_lua grammar_subset_css grammar_subset_yaml grammar_subset_toml \
grammar_subset_markdown grammar_subset_hcl"
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -buildvcs=false \
  -tags "$TAGS" -ldflags "-X github.com/samibel/graphi/internal/version.Version=dev" \
  -o /tmp/spike ./cmd/graphi
git stash && git checkout main   # or: git worktree add
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -buildvcs=false \
  -tags "$TAGS" -ldflags "-X github.com/samibel/graphi/internal/version.Version=dev" \
  -o /tmp/base ./cmd/graphi
cmp /tmp/spike /tmp/base   # silence = byte-identical

# Containment, fail-closed and isolation claims (§3, §5, §6)
go test ./engine/exthost -count=1
go test -race ./engine/exthost -count=1
```
