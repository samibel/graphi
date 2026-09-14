# Named-declaration completion, and the measured ceiling behind it

Status: **small positive development result plus a negative architectural
finding; not a release result.**

The capture is bound to candidate
`40133db511cd363dcb9fed7bda5f330d90c957db`, the pinned Cobra checkout
`a0a6ae020bb3899ff0276067863e50523f897370`, and development dataset
`2d05e3bb015a1447e0c31a9a855712e6aae6f4281adbf7acd72e86c923a43d6c`. It
contains only the 44 development records (40 answerable, three no-hit, and
`cb-31`, which carries no grade-3 judgement). The
spent independent holdout was not opened, rerun, inspected or tuned against.

Read the architectural finding first. The accepted change is real but narrow,
and the measurements below say plainly why no larger change was accepted: on
this development split, response budget is no longer the binding constraint on
answer quality.

## 1. Where development answer quality is actually lost

Every one of the 63 grade-3 answer spans in the 40 answerable development
questions was classified by the first stage that loses it. Stage membership is
observed, not assumed: "over ceiling" is the exact cl100k cost of a response
whose only source is that span; retrieval rank is the span's first position in
the existing 50-row retrieval window, recorded in the capture; the candidate
cap is `taskctx.candidatePoolLimit`.

| Stage that loses the span | Spans | Share |
|---|---:|---:|
| Delivered complete to the actor | 35 | 55.6% |
| Larger than a whole 1,200-token response | 7 | 11.1% |
| Never enters the 50-row retrieval window | 7 | 11.1% |
| In the window, below the 15-candidate task-context cap | 3 | 4.8% |
| Retrieved, admitted, affordable — lost in compact selection | 11 | 17.5% |

Two consequences follow directly.

**The frozen ceiling caps development answerability well below 40/40.** Five
development questions (`cb-07`, `cb-08`, `cb-09`, `cb-14`, `cb-35`) have *no*
grade-3 answer span that fits inside one 1,200-token response at all, before a
single competing region is admitted:

| Query | Smallest grade-3 span | Lines | Minimum whole response |
|---|---|---:|---:|
| `cb-07` | `flag_groups.go:1-290` | 290 | 3,117 tokens |
| `cb-08` | `doc/man_docs.go:1-246` | 246 | 2,725 tokens |
| `cb-35` | `completions.go:678-839` | 162 | 1,897 tokens |
| `cb-09` | `shell_completions.go:1-98` | 98 | 1,241 tokens |
| `cb-14` | `completions.go:193-264` | 72 | 1,220 tokens |

So "at least one complete grade-3 span" has a hard upper bound of **35/40**,
and "every grade-3 span complete" a hard upper bound of **33/40**, for this
projector and for every possible future one at this ceiling.
`TestAnswerSpanFeasibilityAgainstFrozenCeilingDev` computes and pins both
bounds. This candidate reaches 33/40 and 18/40.

**More response budget no longer buys answers.** The internal source frontier
was swept over the same 40 questions. Only the rebuild path can vary the
frontier, so the sweep replays the committed pre-compact capture
`2026-09-07-answer-recovery-dev/bundles-after.json` through this candidate's
projector. That capture predates the retrieval-side exact-path promotion, so
its 325 row sits one complete span below section 3; the shape of the frontier,
which is the finding, is unaffected, and the 325 row is the comparable
baseline for the rest of the table.

| Source frontier | Grade-3 line coverage | ≥1 complete span | All spans complete | Every span overlapped | Median tokens | Cheaper than GrepRead/2 | Paired median saving |
|---:|---:|---:|---:|---:|---:|---:|---:|
| 275 | 0.3395 | 27/40 | 15/40 | 29/40 | 938.5 | 29/40 | +166.5 (14.49%) |
| **325** | **0.3864** | **32/40** | **18/40** | **29/40** | **1,010.5** | **24/40** | **+67.0 (5.91%)** |
| 375 | 0.4182 | 32/40 | 18/40 | 29/40 | 1,121.0 | 17/40 | −24.5 (−2.21%) |
| 425 | 0.4286 | 32/40 | 18/40 | 29/40 | 1,144.0 | 13/40 | −56.0 (−5.07%) |
| 500 | 0.4203 | 32/40 | 18/40 | 29/40 | 1,182.5 | 10/40 | −64.5 (−5.83%) |
| 600 | 0.3943 | 30/40 | 17/40 | 29/40 | 1,148.0 | 12/40 | −43.0 (−4.01%) |
| 750 | 0.3788 | 31/40 | 18/40 | 29/40 | 1,157.5 | 13/40 | −60.0 (−5.47%) |

`≥1 complete span` is flat at 32/40 from 325 fields to 500 and falls beyond;
`all spans complete` is flat at 18/40 from 325 to 750; `every grade-3 span
overlapped` is 29/40 at every frontier tried — completely budget-insensitive.
Meanwhile the paired median saving falls from +14.5% to −5.8%. Between 325 and
425 the projector buys 4.2 points of grade-3 line coverage and pays the entire
token-savings claim for it; past 425 it buys nothing and keeps paying.

The measured rows are consistent with the sweep: the frontier is a single
global whitespace-field proxy for a per-response cl100k cost that varies by
about a third across questions, so a response that respects the proxy leaves a
mean of 194 tokens (16% of the ceiling) unused, ranging from 26 to 383.
Raising the proxy spends that headroom on all forty questions, including the
ones that were already answered.

The conclusion this evidence supports is that the next real gain is **not** in
budget policy. It is 7 spans of retrieval recall, 3 spans of candidate
admission and 11 spans of compact ranking — 21 of 63 — and none of those is a
budget question. Section 5 states the concrete structural alternative.

## 2. Accepted change

For an exact-identifier lookup the selector charges every admitted region for
its anchor line and only afterwards tries to complete the named declaration.
A declaration that fits the source budget whole could therefore still be
emitted cut, with its remaining branches and its return displaced by a few
one-line neighbour citations.

For `cb-01` (`ExecuteC`) the whole declaration `command.go:1051-1137` costs
318 of the 325 available fields. Four one-line neighbour citations were
charged 32 fields first, so the definition was truncated at line 1129 — losing
the final `SilenceUsage` branch and `return cmd, err`.

The selector now gives the weakest of those citations back, lowest rank first,
until the complete definition fits, and only ever when it then fits the same
unchanged budget. It cannot reorder regions, cannot admit a new one, and
cannot fire at all when the definition is larger than the budget — that case
already routes to the existing "give one long implementation the whole budget"
path, which is untouched.

Selection reads only query text, retrieval results, graph data and repository
bytes. No query ID, qrel, target span, answer callback or repository-specific
path is consulted. The source budget, region ranking, region weights, the
retrieval ranking, the frozen 1,200-token serialized ceiling, the methodology,
the targets and the scoring rules are all unchanged. The actor-visible wire
identity becomes `task_context/2-compact/9`.

Regression tests written against the rule, in
`engine/agenttools/taskctx/compact/v9/named_declaration_test.go`:

- `TestNamedDeclarationSurvivesNeighbourAnchors` — the complete definition must
  be emitted; fails on the parent commit.
- `TestNamedDeclarationReclamationStopsWhenTheDefinitionFits` — the fewest
  citations that let the definition close are reclaimed and the rest kept;
  fails on the parent commit.
- `TestNamedDeclarationReclamationKeepsRankOrder` — rank order is unchanged.
- `TestNamedDeclarationTooLargeToFinishKeepsItsExistingDepth` — an unaffordable
  definition keeps its existing single-region depth behaviour.

## 3. Before / after

The before row is the immediately preceding candidate
`2576d486a7510ad46f894e983663f3536da6eecf`, measured with the same
instruments. The after row uses the exact production MCP bytes in
`bundles.json`.

| Measure (40 answerable dev queries) | Before | After |
|---|---:|---:|
| Required grade-3 overlap reached | 40 | 40 |
| Every grade-3 span overlapped | 30 | 30 |
| At least one complete grade-3 span | 32 | **33** |
| Required complete spans reached | 32 | **33** |
| Every grade-3 span complete | 17 | **18** |
| Grade-3 answer lines delivered | 946/2389 (0.3960) | **954/2389 (0.3993)** |
| `exact_identifier` with a complete span | 3/4 | **4/4** |
| `exact_identifier` with every span complete | 3/4 | **4/4** |
| `config_docs` with a complete span | 19/19 | 19/19 |
| `architecture_flow` with a complete span | 3/5 | 3/5 |
| Responses at or below 1,200 cl100k tokens | 40 | 40 |
| Median response tokens | 1,021.0 | **1,019.5** |
| Maximum response tokens | 1,174 | 1,174 |
| Bundles with a strictly contained duplicate source | 0 | 0 |
| Individually cheaper than equal-recall GrepRead/2 | 24 | 24 |
| Paired median token saving | 76.0 | 76.0 |
| Paired median percent saving | 6.6892% | 6.6892% |
| No-hit queries returning a successful empty result | 3/3 | 3/3 |

No registered cost measure regresses; the median falls slightly. The change is
surgical: comparing the two captures byte for byte after normalising the wire
version string, **43 of 44 responses are identical and only `cb-01` changes**.

```
cb-01 before: command.go:1051-1129, command.go:874, command.go:114,
              command.go:1401, command.go:1421          1,018 cl100k tokens
cb-01 after:  command.go:1051-1137                        948 cl100k tokens
```

The one changed response is both more complete and 70 tokens cheaper.

Ranking gates are untouched by construction — the change is inside the compact
projection, not retrieval — and are re-verified green: architecture-flow
nDCG@10 `0.46246715228468249` (required `0.4578575262772977`), NL-behaviour
nDCG@10 `0.70290472235070689` (required `0.54497025306999103`),
exact-identifier Top-1 `1.0` (required `1.0`), bundle coverage 6/6.

Two independent index builds used 768 input documents each, recorded distinct
freshness generations, and produced identical MCP response bytes, SHA-256
digests and cl100k token counts for all 44 records (`44/44`). The capture
SHA-256 is
`b446466e0c0e7864b77a9a3e5f9dcc77f5168f464b3dd3beecb0aca452fc11d8`. The
capture's candidate binding records a clean candidate worktree at the frozen
candidate SHA and a clean pinned checkout.

## 4. Hypotheses that were falsified

Each was implemented, measured on the same 40 development questions against
the same instruments, and reverted. None is in the candidate.

| Hypothesis | Implementation | Observed | Decision |
|---|---|---|---|
| The projector systematically underspends the real ceiling, so any cut declaration that still fits should be finished. | After the wire is measured, extend any admitted region to its declaration boundary while the response fits 1,200 tokens. | ≥1 complete span 32→33, all complete 17→18, **but paired median saving 67→10 tokens (5.91%→0.83%) and cheaper 24/40→21/40**. Six responses grew; five bought no measured coverage at all. | Reject: it pays for depth on questions that were already answered. |
| A declaration cited in two disjoint windows shows the reader a hole in a causal chain, so the omitted middle should be filled. | Additionally join or complete any unit the response cites in ≥2 windows. | Fires on **14 of 40** responses, median tokens 1,033→1,064, cheaper 24/40→20/40, paired median saving 67→1 token — **and does not fix `cb-24`, the query that motivated it**, whose complete 409-field function does not fit beside its own call site. | Reject: the second window is a deliberate economy of the flow allocator, not a defect; re-joining it spends exactly what it saved. |
| The anchor-starvation defect is mode-independent, so reclamation should apply to the top-ranked region in every mode. | Same reclamation, gate widened to all query modes. | Same 33/40 and 18/40, cheaper, **but every-span-overlapped 30/40→29/40 and grade-3 line coverage 0.3993→0.3817**. | Reject: outside an exact lookup the caller did not name one declaration, and buying its tail costs real answer content. |
| The remaining gap is a budget gap. | Source frontier swept 275→750 (section 1). | ≥1 complete span flat from 325 to 500 and all-complete flat at 18/40 from 325 to 750, while the savings claim inverts from +14.5% to −5.8%. | Reject: budget is no longer the binding constraint. |

`cb-19` was deliberately not pursued. Its complete `ExecuteC` span fits
(974 tokens), so the metric could be moved by emitting it — but the question
is "what is the lifecycle of running a root command from Execute to the Run
hooks", and the hooks are in `command.go:874-1013`, whose complete span needs
1,438 tokens and can never fit. The current response spends its budget on
`command.go:940-1005`, the hook-calling region itself. Trading that for a
complete `ExecuteC` would raise `any_complete` by one and make the answer
worse. It is recorded here rather than taken.

## 5. What the evidence says to do next

Ranked by measured spans recoverable, with the frozen ceiling held:

1. **Compact ranking, 11 spans.** These are retrieved, admitted and
   individually affordable, and lost to region ranking. Inspection of the
   losses (`ci-1408`, `ci-1991`, `ci-2249` all miss the same
   `command.go:1847-1853`) shows the reviewer's key marks 2–4 answer spans per
   `config_docs` question while the projector ranks other, plausible regions
   above the second and third. This is a multi-answer ranking problem, not a
   depth problem, which is why it is flat under the budget sweep.
2. **Retrieval recall, 7 spans.** These never enter the existing 50-row
   window, so nothing downstream can recover them except the query-only
   GrepRead/2 lexical fallback, which measurably already rescues several
   (`ci-678`'s only answer span has retrieval rank 0 and is delivered
   complete). Widening or diversifying that window is upstream of the compact
   projector and must be re-gated on nDCG.
3. **Candidate admission, 3 spans.** `taskctx.candidatePoolLimit` is 15 against
   a 50-row window; three answer spans sit at ranks 16, 21 and 26. The
   preceding candidate already promoted one such case (rank 16) on an exact
   filename signal; the general form is a cap question, not a ranking one.
4. **Nothing here is a budget change.** The 7 over-ceiling spans are not
   recoverable at 1,200 tokens by any projector. Raising the ceiling itself
   would change the frozen estimand and is out of scope for this contract.

A separate finding, recorded because it bears directly on why development
gains have not transferred: the compact selector now carries twelve
`compactTaskContextWants…` query-shape predicates (`ShellProtocol`,
`Traversal`, `InitFlagValue`, `ParentFlagValue`, `TestFlagFlow`,
`ExecutingFlag`, `CommandParentLink`, `CompletionCallbackContract`,
`ShellCompletion`, `LifecycleHooks`, `RecursiveWalk`, `MarkdownFlow`), several
of which branch on literal Cobra source text such as `ShellCompRequestCmd` or
`ParseFlags(a)`, all fitted against these same 40 development questions.
Development `reached` has been 40/40 for several candidates while the last
independent holdout returned 42/64. Before another holdout is spent, the
generalisation of those predicates deserves its own scrutiny; this candidate
adds none.

## 6. Release status

**No release YES. No release claim of any kind.**

The last independent holdout returned **RELEASE: NO, 42/64 against a
pre-registered k=56**. It is sealed and was not reopened, rerun or consulted.
It evaluated an earlier compact candidate, not this one. The target checker
continues to report the four development gates as PASS and to exit 1 on the
immutable historical qrel-blind `RELEASE: NO`; there is no override, exception
or waiver, and this development result does not touch that decision.

This candidate is frozen and clean at
`40133db511cd363dcb9fed7bda5f330d90c957db` and is eligible to be
pre-registered for a new independent holdout. Note for whoever registers one:
`40133db5` is the commit the capture in this directory is bound to and the
last commit that changes product behaviour. The commit that records this
evidence additionally enumerates this run directory in the two pin-rotation
inventories, which is required by their governance gates but changes no
product code; pre-register against that evidence commit, not against
`40133db5`, so the capture's candidate binding can match a clean worktree. Whether it is worth spending one
is a judgement call this document deliberately does not make for the reader:
the measured development gain is **one question of forty**, and section 1
predicts that a holdout evaluated at this ceiling cannot exceed roughly the
same 87.5% of questions with a complete answer span that the development split
bounds. If the intent is to clear k=56 of 64, the evidence above says the work
is in ranking and recall, not in this candidate.

## 7. Reproduce

```sh
export CGO_ENABLED=0
export GRAPHI_STATIC_MODEL_DIR=/absolute/path/to/potion-code-16M-v2@e9d2a44ca6a05ac6685f3b23709ea57eb7352d5b
export GRAPHI_RECOVERY_COBRA=/absolute/path/to/cobra-at-a0a6ae020bb3899ff0276067863e50523f897370

# Two independent index builds; requires identical MCP bytes for all 44 rows.
GRAPHI_RECOVERY_EMBEDDER=static:potion-code-16M-v2@e9d2a44ca6a05ac6685f3b23709ea57eb7352d5b \
GRAPHI_RECOVERY_OUT="$PWD/docs/eval/retrieval/runs/2026-09-14-named-declaration-dev/bundles.json" \
GRAPHI_RECOVERY_CANDIDATE_SHA=40133db511cd363dcb9fed7bda5f330d90c957db \
GRAPHI_RECOVERY_REQUIRE_IDENTICAL=1 \
go test ./internal/eval/retrieval -run '^TestRecoveryDevCapture$' -count=1 -v

# Section 3.
GRAPHI_PRODUCT_COMPACT_DEV_COBRA="$GRAPHI_RECOVERY_COBRA" \
GRAPHI_PRODUCT_COMPACT_DEV_BUNDLES=docs/eval/retrieval/runs/2026-09-14-named-declaration-dev/bundles.json \
GRAPHI_PRODUCT_COMPACT_DEV_REQUIRE=1 \
go test ./internal/eval/retrieval -run '^TestProductCompactTaskContextDev$' -count=1 -v

# Section 1 ceiling. Add GRAPHI_PRODUCT_COMPACT_DEV_TRACE=1 to the run above
# for per-response unused ceiling and per-span completeness.
GRAPHI_PRODUCT_COMPACT_DEV_COBRA="$GRAPHI_RECOVERY_COBRA" \
go test ./internal/eval/retrieval -run '^TestAnswerSpanFeasibilityAgainstFrozenCeilingDev$' -count=1 -v

# Section 1 frontier sweep (repeat per frontier).
GRAPHI_PRODUCT_COMPACT_DEV_COBRA="$GRAPHI_RECOVERY_COBRA" \
GRAPHI_PRODUCT_COMPACT_DEV_SOURCE_BUDGET=425 \
go test ./internal/eval/retrieval -run '^TestProductCompactTaskContextDev$' -count=1 -v

go run ./cmd/retrieval-eval -check-targets \
  docs/eval/retrieval/runs/2026-09-13-product-compact-v7-dev/cobra-v2-dev-report.json

go test ./...
```

## 8. Identities

| Thing | Value |
|---|---|
| Candidate | `40133db511cd363dcb9fed7bda5f330d90c957db` |
| Previous candidate | `2576d486a7510ad46f894e983663f3536da6eecf` |
| Dataset SHA-256 | `2d05e3bb015a1447e0c31a9a855712e6aae6f4281adbf7acd72e86c923a43d6c` |
| Pinned Cobra checkout | `a0a6ae020bb3899ff0276067863e50523f897370` |
| Capture artifact SHA-256 | `b446466e0c0e7864b77a9a3e5f9dcc77f5168f464b3dd3beecb0aca452fc11d8` |
| Compact wire identity | `task_context/2-compact/9` (was `/8`) |
| Embedder | `static:potion-code-16M-v2@e9d2a44ca6a05ac6685f3b23709ea57eb7352d5b` |
| Tokenizer | `tiktoken:cl100k_base:ordinary`, vocabulary `223921b76ee99bde995b7ff738513eef100fb51d18c93597a113bcffe865b2a7` |
| Measurement contract | `sw266-measurement-contract/1` |
| `CGO_ENABLED=0 go test ./...` | PASS |
