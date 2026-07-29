# Why `explain_symbol`, `change_risk` and `related_files` return `partial` — and why gate 9 reads UNKNOWN

**Story:** SW-134 · **Spec:** P0 Diagnosis + Candidate Decision · **Date:** 2026-07-29
**Subject:** the 25-execution shortfall behind `agent_context_p95` = UNKNOWN in
[`docs/eval/runs/2026-07-28-ubuntu-latest/p0-baseline.md`](../runs/2026-07-28-ubuntu-latest/p0-baseline.md)
**Candidate under diagnosis:** v0.7.0 at `5815db5`
**Analysed on:** `main` at `6d1d8a4` (the code paths cited below are unchanged between the two)

> **This document proves and classifies. It decides nothing and fixes nothing.**
> Whether `partial` *should* be countable in the FR-8 pool is SW-135's question;
> any code change is SW-136, gated on that decision. This story changed **no
> production code** — see [§9](#9-what-this-story-changed).

---

## 1. The findings at a glance

| # | Finding | Root-cause site | Class | A correction would touch |
|---|---|---|---|---|
| F1 | `explain_symbol` returns `partial` because its item list (1 definition + one item per caller/callee/reference edge) exceeded the harness's cap of 10, and the harness does not count `partial` | downgrade: `engine/agenttools/shape/shape.go:170-172` · rejection: `cmd/eval/querylatency.go:431-432` | **P0-MEASUREMENT** | harness only (`cmd/eval/querylatency.go`) |
| F2 | `change_risk` — identical mechanism, different item population (one item per resolved seed + one per inbound edge) | downgrade: `engine/agenttools/shape/shape.go:170-172` · rejection: `cmd/eval/querylatency.go:431-432` | **P0-MEASUREMENT** | harness only (`cmd/eval/querylatency.go`) |
| F3 | `related_files` — identical mechanism, different item population again (one item per distinct related **file**), and a wider allowed set that already tolerates `empty` but not `partial` | downgrade: `engine/agenttools/shape/shape.go:170-172` · rejection: `cmd/eval/querylatency.go:433-435` | **P0-MEASUREMENT** | harness only (`cmd/eval/querylatency.go`) |
| F4 | The eval harness measures the three operations at an item cap of **10**; every shipped surface defaults to **20**. The harness therefore manufactures truncation for every answer sized 11–20 that no default-configured user would see | `engine/scenario/fixture.go:292,295,297` vs `engine/agenttools/shape/shape.go:21` | **P0-SCENARIO** | harness only (`engine/scenario/fixture.go`, which no shipped binary imports — see [§8](#8-what-a-correction-would-touch-ac-9)) |
| F5 | No product defect is established. `partial` under truncation is designed, documented and GA-frozen behaviour; the answers were not wrong, they were declared incomplete | `corpus/hero/hero-17-explain-symbol-partial.yaml`, `engine/agenttools/shape/shape.go:159-174` | **not a defect** (explicitly *not* P0-PRODUCT; scope stated in [§7](#7-why-this-is-not-p0-product)) | nothing |

**No finding is P0-UNKNOWN.** What remains unproven is narrower than a finding
and is stated as such in [§10](#10-what-is-not-proven).

---

## 2. The mechanism, proven

### 2.1 `partial` has exactly one producer for these three operations

`grep -rn OutcomePartial` over the non-test tree returns four sites:

| Site | What it is |
|---|---|
| `engine/agenttools/contract/contract.go:19,31` | the constant and its validity set |
| `engine/agenttools/shape/shape.go:171` | **the only producer reachable by `explain_symbol` / `change_risk` / `related_files`** |
| `engine/agenttools/brief/brief.go:161` | `agent_brief`'s own producer — a different operation, already accepted by the harness |
| `engine/scenario/scenario.go:274` | a validity set in the scenario runner, not a producer |

So for the three operations in question, `partial` is produced by one line and
one condition:

```go
// engine/agenttools/shape/shape.go:162-174
func Finish(r *contract.Result, maxItems int) (*contract.Result, error) {
	if maxItems <= 0 {
		maxItems = DefaultMaxItems // 20
	}
	out, err := contract.ApplyItemCap(r, maxItems)
	if err != nil {
		return nil, err
	}
	if out.Limits.Truncated && out.Outcome == contract.OutcomeFound {
		out.Outcome = contract.OutcomePartial
	}
	return out, nil
}
```

`Limits.Truncated` is set by `contract.ApplyItemCap` (`engine/agenttools/contract/limits.go:20-28`)
and is exactly `len(items) > itemCap`. Nothing else in the chain can set
`partial`. Pinned by
`engine/agenttools/shape/partial_characterization_test.go::TestFinish_PartialIsProducedExactlyByCapTruncationOfAFoundResult`,
which also shows that an `ambiguous`, `empty` or `unavailable` envelope is **not**
downgraded — so none of those can masquerade as the observed `partial`.

All three operations reach that line, and only that line:

- `engine/agenttools/explain/explain.go:101` — `return shape.Finish(r, maxItems)`
- `engine/agenttools/risk/risk.go:221` — `return shape.Finish(r, maxItems)`
- `engine/agenttools/related/related.go:242` — `return shape.Finish(r, maxFiles)`

(The other `shape.Finish` call sites in those files — `explain.go:42,55`,
`risk.go:68`, `related.go:68` — finish an *ambiguous* envelope, which `Finish`
never downgrades.)

### 2.2 The harness asks with no cap, and its engine reads "no cap" as 10

The latency harness prepares each of the three operations with the argument map
`{"symbol": <node id>}` and nothing else:

```go
// cmd/eval/querylatency.go:437-443
prepare: func(i int) warmExecution {
	return warmExecution{args: map[string]string{"symbol": agentAt(i)}, requirement: requirement, allowed: allowed}
},
invoke: envelope(op),
```

`envelope` dispatches to `scenario.FixtureEngine.InvokeContract`
(`cmd/eval/querylatency.go:359-364`), which supplies the cap the caller omitted:

```go
// engine/scenario/fixture.go:292,295,297
return explain.Explain(ctx, e.Deps, ref, intArg(args, "max_items", 10))
return related.Files(ctx, e.Deps, anchor, args["direction"], intArg(args, "max_files", 10))
return risk.Assess(ctx, e.Deps, firstArg(args, "target", "symbol"), args["diff"], intArg(args, "max_items", 10))
```

**The cap that produced every `partial` in the P0 baseline is 10, not
`shape.DefaultMaxItems`.** `DefaultMaxItems = 20` is never reached on this path,
because `10 > 0`.

### 2.3 The harness then refuses to count the result

`executeWarmOperation` retains a latency sample only for an execution whose
outcome the `observe` predicate accepts (`cmd/eval/querylatency.go:106-126`);
`observe` accepts exactly the outcomes in the execution's `allowed` set
(`cmd/eval/fullrun.go:599-616,617-637`). The declarations are:

```go
// cmd/eval/querylatency.go:430-455
for _, op := range []string{OpExplainSymbol, OpChangeRisk, OpRelatedFiles} {
	allowed := []string{"found"}
	requirement := "resolved target with a valid found envelope"
	if op == OpRelatedFiles {
		allowed = []string{"found", "empty"}
		requirement = "resolved target; found or legitimately empty related-file set"
	}
	…
}
ops = append(ops, warmOperation{
	op: OpAgentBrief, …
	requirement: "resolved topic with a valid found/partial project brief",
	allowed:     []string{"found", "partial"},
})
```

`agent_brief` sits in the **same** FR-8 pool (`docs/eval/reference-scenario.json`,
`agent_context_p95`: `["agent_brief", "explain_symbol", "change_risk", "related_files"]`)
and already treats `partial` as countable. Pinned by
`cmd/eval/partialoutcome_characterization_test.go::TestAgentContextPool_DeclaresPartialCountableForAgentBriefOnly`.

### 2.4 The whole chain, offline

`engine/scenario/partial_characterization_test.go::TestEvalHarnessArgs_ProducePartialForEachAgentContextOperation`
runs the harness's exact argument map through the harness's exact engine over an
in-memory graph and observes `partial` for each of the three operations
separately, with `Limits.Truncated` true and `Limits.CapApplied == 10` — and
`found` for the identical call on a target below the cap.

---

## 3. The three operations do **not** behave the same way (AC-2)

They share the downgrade site and the cap. They differ in what fills the item
list, which is why they contribute different numbers of rejected executions to
one pool. This was shown, not assumed — each row below is exercised separately
by its own sub-test in `TestEvalHarnessArgs_ProducePartialForEachAgentContextOperation`.

| Operation | Resolution | One item per… | Truncates when | Code |
|---|---|---|---|---|
| `explain_symbol` | `resolve.Strict`, single node | the definition, **plus** each caller edge, each callee edge, each reference edge whose endpoint node resolves | `1 + callers + callees + references > 10` | `explain.go:71-101`, `relationItems` at `:107-134` |
| `change_risk` | `resolve.Strict`, single node (or diff paths) | each resolved seed, **plus** each **inbound** edge whose source node resolves | `seeds + inbound > 10` | `risk.go:179-221` |
| `related_files` | `resolve.Seeds(…, 5)` — **up to five** seed nodes | each distinct related **file**, after folding all edges to that file into one score | `distinct related files > 10` | `related.go:63, 202-242` |

Three consequences that the published tallies confirm:

1. **`explain_symbol` truncates most often.** Its population is the union of
   three relation directions; `change_risk` counts only inbound edges. Observed
   on grpc-go: 16 vs 4 rejected executions.
2. **`related_files` is not simply "fewer than `change_risk`".** It folds many
   edges into one file item (pushing its count down) but reads **both**
   directions from **up to five** seeds (pushing it up). Observed on grpc-go it
   is *higher* than `change_risk` (5 vs 4) and on gin and uuid it is *zero*
   while `change_risk` is 9 and 8. A per-edge model of `related_files` would
   predict the wrong sign; the per-file model predicts the observed spread.
3. **`related_files` also has a wider allowed set** (`found`, `empty`), which is
   why it can return `empty` 90 times on grpc-go and 180 times on `lo` without
   losing a single execution. Its `partial` is refused for the same reason the
   other two are.

`agent_brief`, the fourth pool member, is invoked with `MaxItems: 0`
(`engine/scenario/fixture.go:303`) — it is not exposed to the harness's cap of
10 at all, and it lost **zero** executions in every published run of every repo.
Pinned by `TestAgentBrief_IsNotSubjectToTheHarnessItemCap`.

---

## 4. The 25-of-1000 arithmetic (AC-4)

### 4.1 Who contributed what

FR-8's floor is 1000 timed executions per gate pool
(`internal/evalreport/querylatency.go:40`). `queryExecutionPlan` splits it over
the pool's four operations: **250 attempted executions each**. Published
`stable_checks`, grpc-go, **identical in run-a and run-b**:

| Operation | Attempted | `found` | `empty` | `partial` | Allowed set | **Rejected** | Counted |
|---|---|---|---|---|---|---|---|
| `agent_brief` | 250 | 250 | – | 0 | `found`, `partial` | **0** | 250 |
| `change_risk` | 250 | 246 | – | **4** | `found` | **4** | 246 |
| `explain_symbol` | 250 | 234 | – | **16** | `found` | **16** | 234 |
| `related_files` | 250 | 155 | 90 | **5** | `found`, `empty` | **5** | 245 |
| **Pool** | **1000** | | | **25** | | **25** | **975** |

`16 + 5 + 4 + 0 = 25`. `1000 − 25 = 975`. That is the published pool size
exactly, and **`partial` is the only rejected outcome** — no errors, no
`ambiguous`, no `not_found`, no missing responses. Source:
`run-{a,b}/query-latency/grpc-go/report.json` → `.repo.stable_checks`.

Pinned by `cmd/eval/partialoutcome_characterization_test.go::TestPublishedBaseline_PartialAloneExplainsThe25ExecutionShortfall`,
which replays the committed tallies through the **live** allowed sets and fails
if either side moves.

### 4.2 Why it is deterministic across machines

The reject count for an operation is

```
|{ s ∈ sample : items(op, s) > 10 }|
```

Every term is machine-independent:

- **The sample is a total order over a fixed graph.** `DegreeStratifiedSymbols`
  orders candidates by incident degree DESC then node id ASC and picks one per
  quantile bucket (`cmd/eval/querylatency.go:331-334`). The ordered sample is
  published with a digest — grpc-go `332036d65a7ec805`, identical in both runs.
- **Each sampled symbol is asked exactly once per operation** on grpc-go:
  `agentAt(i) = symbolIDs[i % agentSymbols]` with `agent_symbols = 250` and 250
  executions.
- **The cap is a compile-time integer**, 10.
- **`items(op, s)` is a graph property** — counts of edges and of distinct
  neighbour files. Nothing in it reads a clock, a core count or a cache state.

Hence the identical 25 on an Intel Xeon 8573C (run-a) and an AMD EPYC 9V74
(run-b), and the identical tallies for all five pinned repos across both runs.
Pinned by `TestPublishedBaseline_TalliesAreRunInvariantAndLoIsClean`.

### 4.3 An independent confirmation from `uuid`

`uuid`'s degree-stratified sample returned only **137** symbols, yet each
operation still ran 250 timed executions over `symbolIDs[i % 137]`. Indices
0–112 are therefore asked **twice**, indices 113–136 once — and because the
sample is ordered by degree DESC, the doubled prefix is precisely the
high-degree half that can exceed a cap.

If rejection were an execution-level event (timing, runner load, cache state)
the counts would be arbitrary. If it is a deterministic property of the
**symbol**, every offending symbol in the doubled prefix must cost exactly two
executions. Observed on `uuid`: `change_risk` **8** and `explain_symbol` **6** —
both even, consistent with 4 and 3 offending symbols, in both runs. Pinned by
`TestPublishedBaseline_UuidRepeatsItsSampleAndRepeatsItsRejections`.

### 4.4 The full picture across the five pinned repos

Rejected executions, run-a and run-b identical throughout:

| Repo | nodes | edges | edges/node | sampled symbols | `explain_symbol` | `related_files` | `change_risk` | **total** |
|---|---|---|---|---|---|---|---|---|
| `lo` | 523 | 704 | 1.35 | 250 | 0 | 0 | 0 | **0** |
| `uuid` | 286 | 781 | 2.73 | 137 | 6 | 0 | 8 | **14** |
| `gin` | 1890 | 6794 | 3.60 | 250 | 14 | 0 | 9 | **23** |
| **`grpc-go`** (reference) | 14896 | 99736 | 6.70 | 250 | 16 | 5 | 4 | **25** |
| `cobra` | 938 | 4206 | 4.48 | 250 | 24 | 1 | 14 | **39** |

The count is **not** monotone in graph density, and the diagnosis does not claim
it should be: the sample is 250 symbols regardless of repository size, so the
*fraction* of the graph sampled varies from 1.7 % (grpc-go: 250 of 14896) to
27 % (cobra: 250 of 938). A degree-stratified sample of 250 buckets over 14896
nodes places most picks in low-degree buckets, which is why the densest
repository is not the worst offender.

---

## 5. Why `lo` passes (AC-5)

`lo` (`samber/lo`) records **zero** `partial` executions for all three
operations, in both runs. It passes by the *same* mechanism, below its
threshold — not by a different one.

Evidence, all from the published artifacts:

1. **It is the sparsest graph of the five by a wide margin**: 704 edges over 523
   nodes, 1.35 edges/node — half the next-sparsest (`uuid`, 2.73) and a fifth of
   grpc-go's 6.70. `samber/lo` is a flat generics utility library: each exported
   helper is self-contained, and there is almost no internal call graph to
   accumulate items from.
2. **Its answers are demonstrably small, measured directly.** `related_files`
   returned `empty` for **180 of 250** sampled `lo` symbols — 72 % of the sample
   has *no cross-file neighbour at all*. Compare grpc-go 90/250 (36 %), gin
   57/250, cobra 26/250, uuid 22/250. `lo` has the smallest answers in the
   corpus, by the corpus's own numbers.
3. **Consequently `ApplyItemCap` never truncates**: no sampled `lo` symbol
   reaches 11 items under any of the three item populations, so
   `Limits.Truncated` is never true, so `Finish` never downgrades, so no
   execution is rejected. All 1000 of `lo`'s pool executions counted.
4. **The same code path is shown returning `found` below the cap.** The `p.Cold`
   control in `TestEvalHarnessArgs_ProducePartialForEachAgentContextOperation`
   is exactly this case: identical call, identical code path, 2 callers → `found`
   for all three operations.

The cause is therefore complete in the sense AC-5 requires: it explains the
passing repository as well as the failing ones, and it is falsifiable — a single
`lo` symbol with more than 10 items would produce a non-zero count, and the
count is zero in both runs. Pinned by
`TestPublishedBaseline_TalliesAreRunInvariantAndLoIsClean`, which fails if `lo`
ever records a `partial`.

---

## 6. F4: the harness measures at half the shipped cap (P0-SCENARIO)

Every shipped surface resolves "caller passed no cap" to `DefaultMaxItems = 20`:

| Surface | How "no cap" arrives | Resolves to |
|---|---|---|
| CLI | `-max-items` / `-max-files` default **0** (`surfaces/cli/cli.go:627,654,682`) | `shape.Finish(r, 0)` → 20 |
| MCP | absent `limit` → `derefInt(nil)` = **0** (`surfaces/mcp/toolcalls.go:641-646,669,682,695`) | 20 |
| HTTP / daemon / direct | pass the caller's value through unchanged (`surfaces/client/direct.go:868,877,886`) | 20 |
| **eval harness** | `intArg(args, "max_items", **10**)` (`engine/scenario/fixture.go:292,295,297`) | **10** |

The measurement therefore asks a strictly harder question than any default
configuration of the shipped product: every answer whose natural size lands in
**11–20 items** is reported `partial` to the harness and `found` to a user. That
is a scenario-fidelity defect independent of F1–F3: even if `partial` were made
countable tomorrow, the harness would still be measuring a cap the product does
not ship.

Pinned by `engine/scenario/partial_characterization_test.go::TestEvalHarnessCap_IsHalfTheShippedDefault`,
which shows one fixture answering `partial` at the harness's cap and `found` at
the shipped default.

**What this finding does not claim.** It does **not** claim that raising the
harness cap to 20 would recover the 25 executions. That would require knowing
how many of the 250 sampled grpc-go symbols have 11–20 items versus more than
20, which is not derivable from the published artifacts — see
[§10](#10-what-is-not-proven).

---

## 7. Why this is not P0-PRODUCT

`partial` under truncation is **designed, documented and GA-frozen behaviour**,
not an unhandled state:

- `corpus/hero/hero-17-explain-symbol-partial.yaml` — hero scenario, one of the
  20 that cover the 12 stable operations: *"Failure class PARTIAL: an item cap
  below the natural answer size must be reported as partial (truncation is never
  silent), and the definition always survives the cap."* It asserts
  `outcome: partial` end to end and runs on the standing PR gate.
- `engine/agenttools/shape/shape.go:159-161` states the same contract in the
  code: *"downgrades the outcome to `partial` when the cap truncated the item
  list."*
- The envelope is not degraded: the definition item outranks everything
  (`explain.go:75`, `Rank: 1<<20`), evidence is intact, and `Limits` carries
  `TotalAvailable` / `Dropped` so the caller can ask again with a larger cap.

The harness's own requirement string for the rejection is *"resolved target with
a valid **found** envelope"* — but the target *was* resolved and the envelope
*was* valid. It was `partial`, which is a truthful statement about the answer,
not a wrong answer. The harness's own justification for the rule — *"a fast
wrong answer is not a fast answer"* (`cmd/eval/querylatency.go:102-105`) — does
not apply to a correct answer that declares its own truncation.

**Scope of this non-finding.** F5 establishes that no product defect explains
the observed `partial` outcomes. It does **not** establish that the shipped
default cap of 20 is the right product choice for large repositories — that is a
product-design question, it is not what caused the shortfall (the shortfall was
measured at 10), and it belongs to the PRD, not to this diagnosis.

---

## 8. What a correction would touch (AC-9)

This section is SW-135's input. It states where the code would move; it does
**not** choose.

| Finding | A correction would touch | Product code? | Implication for the candidate |
|---|---|---|---|
| F1 `explain_symbol` | `cmd/eval/querylatency.go:431-432` — the `allowed` set and its requirement string | **No** — `cmd/eval` is the measurement harness | The candidate **need not move** |
| F2 `change_risk` | same lines, same set | **No** | The candidate **need not move** |
| F3 `related_files` | `cmd/eval/querylatency.go:433-435` — the same, on the wider set | **No** | The candidate **need not move** |
| F4 harness cap | `engine/scenario/fixture.go:292,295,297` — the three `intArg` defaults | **No, despite the path.** `engine/scenario` is imported only by `cmd/eval`; it is **not** in the dependency graph of the shipped binary | The candidate **need not move** |
| F5 | nothing | — | — |

The claim about `engine/scenario` is mechanically checkable and was checked:

```
$ go list -deps ./cmd/graphi | grep -c engine/scenario
0
$ for d in cmd/*/; do go list -deps ./$d | grep -c engine/scenario; done
# 1 for cmd/eval only; 0 for cmd/graphi and all fourteen other binaries
```

Editing `engine/scenario/fixture.go` therefore changes no byte of the shipped
`graphi` binary, even though the file sits under `engine/`.

One arithmetic note, offered as input rather than as a recommendation: the 25
rejections split **20** across the two `found`-only operations and **5** on
`related_files` ([§4.1](#41-who-contributed-what)). A correction that addressed
only F1 and F2 would leave the pool at 995 of 1000 — still undersampled, still
UNKNOWN. Whatever SW-135 decides has to cover all three or none.

**Net input to SW-135: no finding in this diagnosis requires a change to code
that ships in the candidate binary.** Whether that is sufficient for Outcome A
is SW-135's call — the candidate-move rule also admits measurement-integrity
defects, and F1–F4 are exactly that.

---

## 9. What this story changed

Nothing but this document and three test files. AC-8, mechanically:

```
docs/eval/p0/partial-outcome-diagnosis.md                     (new, this file)
cmd/eval/partialoutcome_characterization_test.go              (new, test)
engine/agenttools/shape/partial_characterization_test.go      (new, test)
engine/scenario/partial_characterization_test.go              (new, test)
```

No non-test file under `core/`, `engine/`, `surfaces/` or `cmd/` was modified.
No fixture repository is cloned and no network access is required: every
characterization test runs under `CGO_ENABLED=0 go test ./...` over an in-memory
`graphstore` or over the committed baseline artifacts.

### Why a fixture reproduction is the same phenomenon (AC-7)

The corpus observation (25 rejected executions on grpc-go) and the fixture
reproduction (one `partial` per operation on an 11-caller in-memory graph) are
the same phenomenon because the mechanism is an integer comparison, not a
property of scale:

1. **Same producing line.** Both reach `shape.go:171`, the only site that can
   set `partial` for these operations ([§2.1](#21-partial-has-exactly-one-producer-for-these-three-operations)),
   and the fixture asserts `Limits.Truncated` is what produced it.
2. **Same cap.** The fixture asserts `Limits.CapApplied == 10`, the value
   `engine/scenario/fixture.go` supplied in the baseline run.
3. **Same entry point and same arguments.** The fixture calls
   `FixtureEngine.InvokeContract(ctx, op, {"symbol": …})` — literally the call
   `cmd/eval/querylatency.go:437-443` makes, with the harness's own argument map.
4. **Same rejection rule.** The harness-layer test replays the *published*
   grpc-go tallies through the *live* `allowed` sets and reproduces 975 and the
   25-way split exactly ([§4.1](#41-who-contributed-what)).

The only thing the fixture does not reproduce is the corpus's *item counts* —
which is the residual recorded in [§10](#10-what-is-not-proven), not part of the
mechanism.

---

## 10. What is **not** proven

Stated so it is not mistaken for proof.

1. **Why 16 and not 15.** The mechanism predicts the reject count is
   `|{s ∈ sample : items(op, s) > 10}|` and the published tallies confirm that
   the totals are 16 / 5 / 4 and deterministic. Recomputing those three integers
   from the graph would require re-indexing grpc-go v1.60.1 at `dbbcf5995…`,
   which needs a corpus clone and is out of this story's scope. This is a
   **falsifiable prediction**, not an assumption: exactly 16 symbols of the
   published 250-symbol sample (digest `332036d65a7ec805`) must have more than
   10 `explain_symbol` items, 5 must have more than 10 related files, and 4 must
   have more than 10 risk items. Anyone with the corpus can check it.
2. **The size of F4's contribution.** How many of the 25 rejections would
   survive at the shipped cap of 20 is unknown for the same reason.
3. **Whether `partial` ought to count.** Deliberately out of scope — SW-135.
4. **Anything about accuracy.** This diagnosis explains why executions were not
   *counted*. It says nothing about whether any answer was *right*; Gold Corpus
   and accuracy scoring are separate, deferred work.

---

## 11. The 471.250 ms is not a verdict (AC-11)

The undersampled pool's p95 in run-a was **471.250 ms**, which is inside the
gate's 500 ms threshold.

**It is recorded here as an observation and nothing else.** It is not a PASS, it
is not evidence that the gate would pass, and no recomputation of it in this
document or in the tests may be read that way:

- A percentile over 975 of a required 1000 executions is not the measurement
  FR-8 asked for.
- The 25 missing executions are **not** a random sample of the pool. They are
  precisely the executions on the highest-item-count symbols — the ones whose
  answers were large enough to truncate — and those are systematically the
  *most* expensive to compute, not the least. The excluded tail is biased
  towards the slow end, so the observed 471.250 ms is, if anything, an
  optimistic reading of a distribution that is missing its heaviest members.
- The published artifact records the gate as `UNKNOWN` with
  `unknown_is_not_pass` spelled out, and PRD §8.2 says UNKNOWN is not a PASS.

`cmd/eval/partialoutcome_characterization_test.go::TestPublishedBaseline_Gate9StaysUnknownDespiteAnUndersampledP95UnderThreshold`
exists to keep it that way: it fails if the published gate verdict ever becomes
anything other than `UNKNOWN` while the pool is still 975 of 1000.

---

## 12. Evidence index

| Claim | Source |
|---|---|
| 975 of 1000 pooled, both runs; gate 9 UNKNOWN | `docs/eval/runs/2026-07-28-ubuntu-latest/p0-baseline.{md,json}` |
| Per-operation outcome tallies (found / empty / partial) | `run-{a,b}/query-latency/<repo>/report.json` → `.repo.stable_checks` |
| Retained sample counts per operation | same → `.repo.query_latency.operations[].latency.n` |
| Symbol sample size, agent symbols, digest | same → `.repo.query_latency.symbol_sample` |
| Undersampled pool p95 = 471 250 µs | same → `.repo.query_latency.pools[]` (`agent_context_p95`) |
| `run_failures` naming the three operations | same → `.repo.failures` |
| Pool membership and threshold | `docs/eval/reference-scenario.json`, gate `agent_context_p95` |
| FR-8 floor of 1000 | `internal/evalreport/querylatency.go:40` |
| Designed `partial` behaviour | `corpus/hero/hero-17-explain-symbol-partial.yaml` |
