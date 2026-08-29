# ADR 0013 — Extension trust tiers: four tiers, a trusted-local-code honesty statement, and the non-goals that bound them

- **Status:** Accepted (design decision of record for the Extension Platform
  Kernel). The original text read *"nothing is implemented — this ADR precedes
  all extension code and changes no runtime behavior"*; that was true on
  2026-08-26 and is no longer. See **Implementation status (addendum,
  2026-08-29)** below for what has shipped per tier. The Context, Decisions,
  Threat model, Rollback, Consequences and "does not decide" sections are
  unchanged.
- **Date:** 2026-08-26
- **Story:** SW-221 — AX-01: ADR for extension trust tiers
- **Spec / Gate:** the Extension Platform Kernel spec §"Boundaries (whole-slice)"
  and §"Requirement accounting", and its source plan §"Extension-Stufen",
  §"Warum kein natives Go-`plugin`", §"Schutzmatrix gegen Schäden",
  §"Was ausdrücklich nicht getan werden sollte". **Both live outside this
  repository**, in the delivery portfolio
  (`projects/graphi/specs/extension-platform-kernel.md` and
  `projects/graphi/research/graphi-architecture-extension-plan.md`); they are
  cited, not linked, because a relative link would dangle.
- **Depends on:** [`../rc/ax00-baseline.md`](../rc/ax00-baseline.md) (AX-00 — the
  frozen wire names, descriptors and canonical result bytes that define what
  "no damage" means for everything below)
- **Related:** [ADR 0006](0006-status-vs-trust-separation.md) (a surface may
  consume facts but must not mint them — the same shape as D7 here),
  [ADR 0007](0007-semantic-resolver-registry.md) and
  [ADR 0012](0012-capability-levels-graded-on-demonstrated-evidence.md) (capability
  levels are derived from live registries and graded on demonstrated evidence —
  the mechanism T3 below shows an extension could launder)
- **Feeds:** SW-222 (registry lifecycle), SW-229 (rule packs — the first tier-A
  product), SW-230 (extension developer kit), SW-231 (the tier-C spike and its
  go/no-go), SW-232 (Stable internal migration)

## Implementation status (addendum, 2026-08-29)

Added by SW-252 (AX-13) and maintained as the implementation evolves. This
block records what is shipped against each tier; it decides nothing and changes
no text below it. The fuller as-built description lives in
[`../architecture-plan.md`](../architecture-plan.md) §6 "The extension kernel as
built".

| Tier | Status | Stories | Where |
|---|---|---|---|
| **A — Declarative packs** | **implemented** | SW-229 (rule packs, AX-09); SW-230 (developer kit + conformance harness, AX-10); SW-246 (`graphi extension` verbs) | [`engine/extpack`](../../engine/extpack), [`../extension-developer-kit.md`](../extension-developer-kit.md) |
| **B — Static first-party modules** | **implemented** | SW-222 (registry lifecycle, AX-02); SW-223 (operation catalog, AX-03); SW-224 (generic executor, AX-04); SW-225 (MCP/HTTP projections, AX-05); SW-226 / SW-228 (canary, per-operation switch, AX-06/AX-08); SW-227 (module kernel + composition root, AX-07); SW-232 (durable divergence record); SW-244 / SW-245 / SW-248 (shadow default, off-critical-path comparison, reachability gate) | [`engine/module`](../../engine/module), [`core/registry`](../../core/registry), [`engine/opcatalog`](../../engine/opcatalog), [`surfaces/client/executor.go`](../../surfaces/client/executor.go), [`surfaces/client/canary.go`](../../surfaces/client/canary.go) |
| **C — Trusted subprocess extensions** | **spiked and decided NO-GO** for phase 1 | SW-231 (AX-11) | [`../decisions/2026-08-process-extension-go-no-go.md`](../decisions/2026-08-process-extension-go-no-go.md); the spike is retained unwired as evidence, at the paths the decision record names |
| **D — WASM** | **not shipped** (N5 unchanged; no revisit trigger has fired) | — | — |

The tier-B implementation now contains two real engine-side handlers:
`dead_code` and `compound`. Each is registered as a spec plus a handler bound to
typed ports; the executor prefers that handler when present and falls back to a
legacy adapter for the other 54 operations. Zero operations are on the `active`
canary position. The legacy
adapters, the shadow comparison, the canary code and the dual
descriptor/contract sources are all still present and are **transitional**
under the AX-17 rule (architecture-plan.md §6). The two `Feeds:` items above
that were resliced: SW-228 shipped the per-operation switch without splitting
the built-in operations module, and SW-232's "Stable internal migration" became
SW-232 (durable divergence record) plus SW-238 (Stable migration
preconditions), which waits on evidence that the default profile cannot yet
produce (SW-247, SW-248).

## Context

graphi is about to grow an extension story. The question this ADR settles is not
*how* extensions are wired — that is SW-222 through SW-232 — but **which kinds of
extension graphi is willing to trust, and how far**.

The decision has to be made now, before any extension code exists, because trust
is the one property a refactor cannot retrofit. Every later story inherits the
answer: a manifest schema (SW-229) can only express permissions that a trust
model has already bounded; a process protocol (SW-231) can only have a go/no-go
bar if someone wrote down what "acceptable" means; and the Stable migration
(SW-232) can only be safe if it was never possible for an optional extension to
be load-bearing for a Stable answer.

Four facts about the current tree frame the decision.

**1. graphi's product promise is a set of hard runtime invariants, not a
feature list.** The default binary is CGo-free and makes no non-loopback network
call — enforced at one chokepoint, `surfaces/guard`, whose package doc states the
two invariants it exists to make inheritable rather than re-auditable per surface
(`surfaces/guard/guard.go:1-15`), and gated in CI by `cgoconformance` and
`canary`. Exactly twelve operations carry `tier: stable` (`mcp.StableOperations`,
`surfaces/mcp/tools.go:236`), and a thirteenth fails `cmd/coverage -check`. Any
extension mechanism that can weaken one of these is not an extension mechanism;
it is a regression with a configuration flag.

**2. The registry seams already exist, and they do not agree with each other.**
`core/parse.Registry.Register` is **last-wins**: "a later registration for the
same extension/language overrides the earlier one, allowing opt-in backends
(e.g. a CGO grammar) to supersede a stdlib default" (`core/parse/registry.go:31-33`).
`engine/analysis.Registry.Register` is **first-wins**: a duplicate name "is
rejected with an error rather than silently overwriting"
(`engine/analysis/registry.go:9-14`), and its `Replace` deliberately refuses
unknown names so it cannot become a second registration path
(`engine/analysis/registry.go:53-56`). Both policies are correct for their own
package and were chosen deliberately. Neither was chosen with *third-party*
registrants in mind. Under a naive extension mechanism the same act — "register
a thing" — silently supersedes a built-in in one registry and errors in the
other. Unifying that vocabulary is SW-222's job; deciding that an extension may
never reach the last-wins door is this ADR's.

**3. Confidence in graphi is minted, not asserted, and the minting is
restricted.** `TierConfirmed` means "confirmed by an authoritative source"
(`core/model/edge.go:26-27`). The linker is explicit that it is not one:
"The linker NEVER returns TierConfirmed" (`engine/link/link.go:60`). Confirmed
edges come from the go/types pass, where the tier "is pinned by construction"
(`engine/typeresolve/check.go:624-631`), and from intrinsic parse facts such as a
file defining a symbol. Downstream, per-language capability levels are **derived
at read time from the live registries** rather than a maintained table
(ADR 0007; ADR 0012 D4). That derivation is keyed on *registration*, and ADR 0012
D4 accepted that predicate only because a CI audit
(`surfaces/client/capabilityaudit_test.go`) binds registration to
fixture-demonstrated outcome in both directions. An extension that can register
is an extension that can move a published capability level — which is why T3
below is a threat and not a footnote.

**4. `extensions/` in this repository is already taken, and means something
else.** It holds product integrations — `extensions/vscode`,
`extensions/github-action`. There is no graphi extension contract for analyzers,
rules, operations or exporters today. This ADR is about the latter; the
directory name will need disambiguating when SW-229 lands, and that is called out
here so the collision is inherited as a known naming decision rather than
discovered as a surprise.

## 1. The four tiers

Extensions are not one thing with a permission slider. They are four distinct
tiers with **different trust assumptions**, and the difference is what is being
trusted, not how much.

| | **A — Declarative packs** | **B — Static first-party modules** | **C — Trusted subprocess extensions** | **D — WASM** |
|---|---|---|---|---|
| What ships | versioned YAML/JSON data | Go source, in the graphi binary | a separate executable | a `.wasm` module + a host runtime |
| Executes foreign code | **no** | no (it *is* graphi's code) | **yes**, out of process | yes, in a sandbox |
| Author | anyone | graphi maintainers | anyone | anyone |
| Trust assumption | the **schema validator** is trusted; the pack is not | ordinary first-party code review | the **author** is trusted, as fully as the user themself | the **runtime** is trusted; the module is not |
| Default state | off until installed; then on | on (it is the product) | **off**, and Labs-only | **not shipped at all** |
| Highest stability reachable | **Labs** — a pack-consuming operation is Labs by I1, and a pack is data, never itself a tiered capability | **Stable** — the only tier Stable may be built from | **Labs**, until a new ADR | n/a |
| Blast radius of a malicious artifact | bounded by what the schema can express | equals a first-party bug | **equals the user's OS account** | bounded by the runtime |
| Status | **the first product** (SW-229) | the standard for Stable (SW-227) | **spike only** (SW-231), go/no-go | **rejected for phase 1** |

**Tier A — declarative packs.** Versioned, schema-validated, checksum-pinned data:
architecture rules, framework detection, taint sources/sinks/sanitizers, query
presets, classification rules, export profiles. No code execution, no network,
deterministic merge order, reviewable in git, installable and disablable without
rebuilding the binary. This is the tier that delivers most of the practical value
of a plugin system at close to none of its risk, and it is therefore graphi's
**first** extension product, not its fallback.

**Tier B — statically compiled first-party modules.** Existing Go capabilities
become modules but stay inside the graphi binary: full performance, CGo-free,
reproducible build, today's tests and provenance still apply, and no ABI or
runtime supply-chain problem exists because there is no runtime loading. This is
the **only** tier from which Stable behavior may be built.

**Tier C — trusted opt-in subprocess extensions.** A versioned stdio protocol,
evaluated only after internal modularization succeeds, opening with read-only
analyzers, exporters and additional Labs operations. Required properties:
explicit activation with no automatic discovery or execution; a handshake
carrying protocol and API version; a manifest with SHA-256 pinning; timeout,
cancellation and a maximum response size; crash isolation; **no direct database
file access, host ports only**; a complete provenance record carrying extension
id, version and hash; and Labs-only status until conformance is demonstrated.

**Tier D — WASM.** Technically the right shape for genuinely untrusted
extensions, and `wazero` is the CGo-free-compatible fit. Not in phase 1 — see D6.

## 2. Decisions

### D1 — The four tiers are separate trust domains, and the separation is the decision.

A capability does not "graduate" from A to C by adding permissions. Tier A cannot
execute code no matter what its manifest says; tier C is trusted-author code no
matter how narrow its permission list. Deciding a tier is deciding *who* is
trusted (validator / maintainer / author / runtime), and that question is
answered once, per tier, not per extension.

### D2 — The delivery order is A → B → C, and it is not negotiable by convenience.

Rule packs (A) ship first as the user-facing extension product. Static modules (B)
are the standard for anything Stable. The subprocess tier (C) is evaluated **only
after** the internal modularization lands, as an explicitly disposable spike
(SW-231) with a real go/no-go — and **"no-go, graphi stays on rule packs and
static modules" is a valid, planned outcome**, not a failure. An extension
mechanism reached for because a feature is inconvenient to build in-tree is the
failure mode this ordering exists to prevent.

### D3 — Honesty statement: a subprocess is trusted local code, NOT a sandbox.

**This is the load-bearing sentence of this ADR.** A normal subprocess is not a
portable security sandbox. It runs with the user's OS rights. A permission
manifest makes host-API access *transparent* and *bounded at the host API*; it
does **not** reliably prevent the process from reading any file the user can
read, writing any file the user can write, or opening any network connection the
user can open. `surfaces/guard` constrains **graphi's** dialers and listeners
(`surfaces/guard/guard.go:1-15`); it has no authority over a different process's
syscalls.

Therefore: **a tier-C extension must be treated as trusted local code — the same
category as a shell script the user chose to run — and every user-facing surface
that offers one must say so in those words.** Not "sandboxed", not "isolated",
not "restricted". The permission manifest is a *transparency and host-API
bounding* mechanism, and describing it as a security boundary would be the single
most damaging thing this platform could claim.

The corollary is D6: the tier that *would* be a real sandbox is WASM, and it is
deliberately not in phase 1 — which means graphi has, by design, **no tier for
untrusted extension code**. That gap is a decision, not an oversight.

### D4 — Invariants an extension may never violate.

These bind every later story in this program. A design that requires violating
one is rejected, not accommodated.

- **I1 — Stable behavior never depends on an optional extension.** The twelve
  frozen operations must produce their AX-00 canonical bytes with every optional
  extension absent, disabled, crashed, or timed out. An extension may *add* Labs
  capability; it may never be a dependency of a Stable answer. Corollary: no
  extension may claim one of the twelve Stable operation ids, and no extension
  may appear in `mcp.StableOperations`.
- **I2 — The default binary stays CGo-free and zero-egress.** No extension
  mechanism may introduce CGo into the default build or a non-loopback network
  call into the default path — including for installation, update or telemetry.
  Enforcement is the existing `cgoconformance` and `canary` gates, unchanged and
  unweakened.
- **I3 — V1 extension capability is read-only.** Extensions read the graph
  through host ports; they do not write graph state, do not write source files,
  and do not participate in the Edit/Apply path. Bounded write ports are
  deferred to a separate, later decision (spec §"Requirement accounting").
- **I4 — Third-party executable code is default-off and Labs-only.** No automatic
  discovery, no automatic execution, no default activation, and no promotion out
  of Labs without a new ADR and demonstrated conformance.
- **I5 — Extensions never mint confidence.** See D5.

### D5 — Extensions may never upgrade a provenance tier, and `confirmed` is closed to them.

Three distinct laundering routes are closed by one rule:

1. **Edge tier.** `TierConfirmed` means "confirmed by an authoritative source"
   (`core/model/edge.go:26-27`). The host does not regard an extension as an
   authoritative source — which is exactly the reasoning by which `engine/link`,
   graphi's *own* first-party resolver, is barred from it
   (`engine/link/link.go:60`). **The ceiling for any extension-produced edge is
   `derived`.** An extension may not emit `confirmed`, and the host must reject
   rather than downgrade an attempt (fail-closed, per standards: a superseded or
   illegitimate wire value is rejected, not silently accepted).
2. **Provenance identity.** Every extension-produced artifact carries the
   extension's id, version and content hash in its provenance record. An
   extension-produced result must be distinguishable from a first-party one at
   the point of consumption, not only in a log.
3. **Capability level.** Per-language capability levels are derived at read time
   from the live registries (ADR 0007), and ADR 0012 D4 accepted that
   registration-keyed predicate **only** because
   `surfaces/client/capabilityaudit_test.go` binds registration to
   fixture-demonstrated outcome. An extension that registers into a
   capability-feeding registry would therefore move a *published* capability
   level with no demonstrated evidence — reintroducing precisely the over-claim
   ADR 0012 measured away. **Extension registrations must not feed the capability
   derivation unless they are graded by that same outcome audit.** SW-222 and
   SW-229 inherit this as a constraint on where an extension may register at all.

Restated as the invariant: **an extension can add a claim, but it can never
raise the confidence of a claim.**

### D6 — Decided non-goals.

Each is closed for this program. Reopening one requires a new ADR, not a design
review.

- **N1 — No native Go `plugin`.** Host and plugin must be built with practically
  identical toolchain, build configuration and dependencies; mismatches surface
  as runtime failures; foreign code runs *inside* the host process and can
  compromise or crash it; and platform support and distribution are strictly
  worse than graphi's current cross-platform single-static-binary promise. If
  executable extensions later become necessary, a versioned **process** protocol
  is the more robust shape (HashiCorp's `go-plugin` demonstrates the pattern;
  graphi need not adopt the library and may specify a small graphi-specific stdio
  protocol first). This one is closed hardest: it fails on all four of
  reliability, security, portability and distribution at once.
- **N2 — No automatic discovery or execution.** graphi does not scan directories
  for extensions and does not run what it finds. Activation is explicit,
  per-extension, and recorded.
- **N3 — No network installation in the default path.** No unverified downloads.
  Installation is local; SHA-256 pinning is mandatory from day one. (Signing is
  deferred, not rejected — spec §"Requirement accounting".)
- **N4 — No extension access to SQLite files.** Extensions reach the graph
  through host ports only. Direct file access would bypass the read-only
  discipline (I3), the selective-read cost contract
  ([ADR 0003](0003-selective-read-contract.md)) and the generation-binding that
  trust evidence depends on.
- **N5 — No WASM in phase 1.** Runtime, ABI, debugging, binary size and SDK
  complexity add risk that current demand does not justify. `wazero` is recorded
  as the CGo-free-compatible candidate **for whenever this is revisited**, so the
  eventual decision starts from evidence rather than a fresh survey.
- **N6 — No external storage or graphstore plugins in V1.** The graph backend is
  not an extension point.

Two further shapes were rejected at the program level and are recorded here so
they are not re-proposed as "the modular option":

- **Microservice split** and **big-bang rewrite** — both destroy graphi's
  latency, determinism and local-install advantages, and neither addresses the
  actual cost being attacked (the per-capability integration tax across
  surfaces). Rejected on the merits, not deferred.

### D7 — Extensions consume host facts; they do not mint host judgements.

The same shape as ADR 0006's status/trust split. An extension may read the graph
and produce a finding. It may not produce freshness prose, a trust verdict, a
rebuild recommendation, a capability level, or a stability tier — those are host
judgements, minted in exactly one place each, and a second minting site is how
two surfaces begin to disagree.

## 3. Threat model

Threats are stated per tier, because a threat that is severe for C is often
structurally impossible for A. "Residual" is what remains **after** the
mitigation and is accepted deliberately.

| # | Threat | Applies to | Mitigation | Residual risk (accepted) |
|---|---|---|---|---|
| T1 | **Supply chain** — a malicious or compromised artifact reaches a user | A, C | local installation only (N3); SHA-256 pinning mandatory from day one; default-off (I4); explicit per-extension activation (N2); Labs-only for C (I4) | signing is deferred, so pinning proves *"the same bytes as when you installed it"*, not *"the bytes the author intended"*. For **C** the residual is the full T2 blast radius, because the user has authorised local code. |
| T2 | **Hostile or buggy code with the user's OS rights** | **C only** | process boundary; permission manifest bounding *host-API* access; the D3 honesty statement on every offering surface; explicit activation | **not mitigated, by construction.** A subprocess runs with the user's rights; graphi cannot portably prevent its filesystem or network access. This is why C is trusted-author-only and why D is the tier that would close it. |
| T3 | **Confidence laundering** — an extension makes a weak claim look strong | A, B, C | D5: `confirmed` closed to extensions (ceiling `derived`); extension id/version/hash in every provenance record; extension registrations kept out of the capability derivation unless graded by the ADR 0012 outcome audit; I1 keeps Stable ids unclaimable | a `derived`-tier claim from a low-quality pack still reads as `derived`. Pack *quality* is a review problem, not a tier problem — the tier system bounds the ceiling, it does not grade the content. |
| T4 | **Crash or hang** | C | separate process, so a crash cannot take the host down; hard timeout; cancellation; maximum response size; the host must degrade to the extension-absent answer, never to a partial one | a pathological extension can still burn CPU and wall-clock up to its timeout on every invocation. |
| T5 | **Silent shadowing of a built-in** | A, C | `core/parse.Registry.Register` is last-wins by design (`core/parse/registry.go:31-33`) — extensions may not reach it; SW-222 will unify registry lifecycle so collision policy is explicit and documented per registry, and freeze-after-build will stop any registry mutating at runtime | until SW-222 lands, the two policies still differ. This ADR forbids extension registration into a last-wins seam; it does not by itself change the seam. |
| T6 | **Stable drift** — an extension changes a frozen operation's output | all | I1; the AX-00 golden wire names, descriptors and canonical result bytes (`docs/rc/ax00-baseline.md`); the 12-op freeze in `cmd/coverage -check`; surface-parity tests | none identified — this is the best-defended boundary in the tree, and the reason AX-00 preceded AX-01. |
| T7 | **Egress or CGo through the back door** — an extension mechanism weakens a headline invariant | all | I2; `canary` and `cgoconformance` unchanged; no network install (N3); no WASM runtime shipped (N5), so no runtime pulls a CGo dependency in | a **tier-C** subprocess may open its own network connections and is invisible to `canary`, which measures the graphi process. The zero-egress claim is therefore scoped to the default binary and to the default path — and activating a tier-C extension takes the user outside that scope. This scoping must be stated wherever the guarantee is stated. |
| T8 | **Privilege escalation via write access** | C | I3: V1 is read-only; no Edit/Apply participation; write ports deferred to a separate decision | none in V1 — the capability does not exist. The residual is entirely in the future decision that would grant it. |
| T9 | **Binary bloat** | D (were it shipped) | N5: no WASM runtime in phase 1; the existing binary-size budget (`bench.yml` › `bench-budget-gate`) applies unchanged | none while D is unshipped. |

Two entries deserve their full sentence rather than a table cell.

**T2, restated without hedging.** graphi's answer to "what if an extension is
hostile?" in tier C is **"do not activate an extension you would not trust with
your shell"**. That is a real answer and an unusually common one, but it must be
said in those words rather than implied by a permission list. See D3.

**T7's scope carve-out is the one place where an extension changes a headline
claim.** graphi's zero-egress promise is enforced at `surfaces/guard` for the
graphi process. A tier-C subprocess is a different process. The promise that
survives is: *the default binary, in the default path, makes no non-loopback
connection* — and every tier-C activation is a user decision to step outside it.
Any documentation of tier C that omits this is wrong.

## 4. Rollback and revisit triggers

### 4.1 Rollback, per tier

Every tier has a rollback that returns the system to *exactly* its prior
behavior, and the rollback is a property of the tier's design rather than an
operational procedure.

| Tier | Rollback | Cost | Verification that it worked |
|---|---|---|---|
| **A — packs** | disable or uninstall the pack | seconds; no rebuild | **a disabled pack must produce exactly the pre-pack behavior** — byte-identical, and that is a testable contract SW-229 owes, not an expectation |
| **B — static modules** | **it is not compiled in** — revert the module registration and rebuild; there is no runtime state to unwind because there was no runtime loading | one revert; ordinary release path | the AX-00 golden artifacts (`docs/rc/ax00-baseline.md`) |
| **C — subprocess** | do not activate it; if running, kill the process — the host stays up | immediate | the extension-absent answer is the Stable answer by I1, so "rolled back" and "correct" are the same state |
| **D — WASM** | nothing to roll back: the optional runtime is not shipped | zero | binary size unchanged |

Program-level rollback is inherited from the strangler design: every AX step is
individually revertible, and the legacy path stays the source of truth until
parity is proven. This ADR itself has no runtime rollback because it changes no
runtime code — the ADR is the artifact, and reverting it is reverting a document.

### 4.2 Revisit triggers

Each non-goal is closed **until a stated condition holds**. The condition is the
only thing that reopens it.

| Closed decision | Reopens when | How |
|---|---|---|
| **N5 — WASM (tier D)** | a **demonstrated need for untrusted extension code** exists: a real, named user case that tier A cannot express and that the user is unwilling to grant tier-C trust to | **a new ADR** evaluating `wazero` against runtime, ABI, debugging, binary-size and SDK cost. Demand must be demonstrated, not anticipated. |
| **Tier C shipping at all** | the SW-231 spike meets its go/no-go bar: default path unaffected; no CGo dependency; comprehensible errors with hard response-size and time limits; documented as trusted local code; **and a real user case justifying the added complexity** | the spike's own go/no-go record. **No-go is a planned, valid outcome** and leaves graphi on tiers A and B. |
| **Tier C leaving Labs** | conformance is demonstrated against the SW-230 harness across a release line | a new ADR; I4 is not waivable by a maintainer decision |
| **I3 — read-only V1** | read-only extension maturity is demonstrated in the field | a separate later decision on bounded write ports (already booked in the spec's deferred list) |
| **Signing (beyond SHA-256 pinning)** | extensions are distributed through any channel other than a local install the user performed | a separate decision (already booked as deferred) |
| **N1 — native Go `plugin`** | **no trigger.** It is closed on four independent grounds (reliability, security, portability, distribution) and a change in any one of them does not repair the others. If Go were ever to ship a stable, cross-platform plugin ABI, that would be a new question about a different mechanism — not a revisit of this one. | — |
| **Microservices / big-bang rewrite** | **no trigger.** Rejected on the merits. | — |

## 5. Consequences

- **SW-229 (rule packs) is unblocked and bounded.** Its manifest may express only
  what tier A can honour: schema-validated data, checksum pinning, deterministic
  merge order, no execution, no network. Anything else it wants to express is a
  tier violation, and now has a document to fail against.
- **SW-231 is explicitly disposable, and that is now written down.** A spike with
  a pre-agreed valid "no" is a cheap experiment; a spike everyone expects to
  succeed is a commitment with extra steps. This ADR makes it the former.
- **graphi has, deliberately, no tier for untrusted extension code.** Tier A is
  safe because it cannot execute; tier C is safe only because its author is
  trusted. There is nothing in between until D exists. Users with genuinely
  untrusted extension needs are, for now, correctly served by "no".
- **The zero-egress claim acquires an explicit scope.** "The default binary, in
  the default path." Every document stating the guarantee alongside tier C must
  carry the scope (T7).
- **A documentation obligation is created, not discharged.** The D3 sentence must
  appear on every surface that offers a tier-C extension — CLI help, manifest
  docs, the SDK README. This ADR states the requirement; SW-230 and SW-231 owe
  the text.
- **The `extensions/` naming collision is inherited, knowingly.** Today that
  directory means product integrations (`vscode`, `github-action`). SW-229 must
  choose a name that does not overload it.
- **No runtime code changes, no coverage-matrix rows, no product bytes.** This
  story adds one markdown file under `docs/adr/`. No capability was added,
  removed or re-tiered, so `docs/coverage-matrix.{md,yaml}` and
  `docs/capability-manifest.json` are correct unchanged. No Go file is touched,
  so the built binary at HEAD is byte-identical to the pre-story build and no
  parity-candidate move is incurred.

## 6. What this ADR does not decide

- **The manifest schema.** The plan's proposed YAML shape (`schema_version`, `id`,
  `version`, `kind`, `api`, `artifact.sha256`, `capabilities.provides`,
  `permissions`, `determinism`, `limits`) is a *proposal recorded for
  continuity*, not a ratified schema. SW-229 designs it.
- **The extension host-port surface** — which ports exist, their signatures, and
  their cost contracts. SW-230.
- **The wire protocol for tier C** — stdio-framed JSON versus gRPC, handshake
  shape, version negotiation. SW-231, and only if it goes.
- **Whether tier C ships at all.** That is the SW-231 go/no-go, and this ADR
  deliberately declines to prejudge it.
- **Removal of the legacy `client.Client` adapters.** A separate, later ADR, and
  explicitly not part of this program (spec §"Requirement accounting").
- **Registry collision policy and the freeze mechanism.** SW-222. This ADR
  constrains *where* an extension may register (D5.3, T5); it does not redesign
  the registries.
- **Any change to the twelve Stable operations.** Untouched, by I1 and by the
  freeze that predates this program.
