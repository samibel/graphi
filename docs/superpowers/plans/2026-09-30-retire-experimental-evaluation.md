# Retire Experimental Evaluation Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Remove the unreleased CodeRank/blind-evaluation track and its generated evidence from `main`, while preserving every shipped embedding, retrieval, privacy, build, and performance behavior.

**Architecture:** Keep the product retrieval and embedding interfaces unchanged, but delete the development-only CodeRank adapter and evaluation orchestration behind them. The release gate returns to deterministic product checks; retained evaluation commands write outside `docs/`, and only minimal package-owned fixtures remain in Git.

**Tech Stack:** Go 1.24, GitHub Actions YAML, npm/Vitest, Git.

**Spec:** `docs/adr/0014-retire-experimental-evaluation.md`

## Global Constraints

- Work from `codex/retire-experimental-eval`, created directly from `main` at `52769a50`.
- Do not rewrite Git history; removed evidence remains recoverable from historical commits.
- Preserve Potion, Ollama, unconfigured lexical retrieval, semantic search, and all published CLI/MCP/HTTP behavior.
- Preserve the CGo-free default binary and zero automatic non-loopback model access.
- Do not lower or reinterpret the historical 56/64 threshold; retire it from release policy.
- Keep `testgate`, coverage, privacy, bench-budget, and reproducible static release checks blocking.
- Do not copy complete run reports, bundles, databases, grades, or datasets into new fixture locations.
- Use package-local `testdata/` only for small deterministic fixtures that protect shipped behavior.
- New evaluation outputs must go to `.graphi/eval-runs/`, an explicit caller path, or a CI temporary directory—not `docs/`.
- Each task must leave its affected packages buildable and tested before commit.

## Review Focus

- A user with Potion, Ollama, or no embedder configured must see byte-compatible public behavior; Task 6 runs the public golden and contract suites.
- A stale CodeRank development invocation must fail because the command/package is absent, never fall back to Potion; Task 2 proves absence and removes the special egress allowlist.
- Removing `retrieval-targets` must not weaken missing-gate detection for the four retained release gates; Task 1 pins the exact required/default set and the absent-gate ERROR path.
- `-export-raw=auto` must never recreate tracked documentation; Task 5 pins `.graphi/eval-runs/<date>-<runner>` and updates CI to upload temporary artifacts.
- The cleanup must not become vacuous by leaving generated files under another `docs/` path; Task 7 adds an absence check and records exact post-cleanup file/byte counts.

---

### Task 1: Return the release gate to shipped product checks

**Files:**
- Modify: `cmd/release-gate/policy_test.go`
- Modify: `cmd/release-gate/gate_test.go`
- Modify: `cmd/release-gate/policy.go`
- Modify: `cmd/release-gate/runners.go`
- Delete: `cmd/release-gate/retrieval_targets_runner_test.go`

**Interfaces:**
- Consumes: `DefaultGates() map[string]Runner`, `requiredGates []string`, and `evaluateGates(Context, map[string]Runner) []GateOutcome`.
- Produces: an exact retained gate set: `bench-budget`, `coverage`, `privacy`, `testgate`.

- [ ] **Step 1: Update the required/default gate-set test first**

In `TestPolicy_RequiredGatesMatchDefaultGates`, assert sorted equality with:

```go
want := []string{"bench-budget", "coverage", "privacy", "testgate"}
```

Add an assertion that `DefaultGates()["retrieval-targets"]` is absent. Keep `TestPolicy_AbsentRequiredGateIsError` and make it delete one retained gate so missing-gate detection remains exercised.

- [ ] **Step 2: Run the focused test and observe RED**

Run: `go test ./cmd/release-gate -run 'TestPolicy_(RequiredGatesMatchDefaultGates|AbsentRequiredGateIsError)' -count=1`

Expected: FAIL because `retrieval-targets` is still both required and produced.

- [ ] **Step 3: Remove the retired gate wiring**

Delete `retrieval-targets` from `requiredGates`, remove its `shellRunner` from `DefaultGates`, remove the retrieval evaluator import, delete the dedicated runner tests, and remove it from `allPassGates()`.

- [ ] **Step 4: Verify release policy remains strict**

Run: `go test ./cmd/release-gate -count=1`

Expected: PASS, including the four-state/two-context table and absent-required-gate ERROR behavior.

- [ ] **Step 5: Commit**

```bash
git add cmd/release-gate
git commit -m "release: retire experimental retrieval target gate"
```

### Task 2: Remove the unreleased CodeRank adapter and qualification module

**Files:**
- Delete: `engine/embed/coderank/embedder.go`
- Delete: `engine/embed/coderank/embedder_test.go`
- Delete: `engine/embed/coderank/manifest.go`
- Delete: `engine/embed/coderank/manifest_test.go`
- Delete: `engine/embed/coderank/protocol.go`
- Delete: `cmd/embedded-model-qualification/main.go`
- Delete: `cmd/embedded-model-qualification/main_test.go`
- Delete: all `internal/eval/retrieval/model_qualification*.go`
- Delete: `internal/eval/retrieval/qualification_machine_darwin.go`
- Delete: `internal/eval/retrieval/qualification_machine_linux.go`
- Delete: `internal/eval/retrieval/qualification_machine_other.go`
- Delete: `scripts/eval/coderank_sidecar.py`
- Delete: `scripts/eval/provision_coderank_sidecar.sh`
- Delete: `scripts/eval/tests/test_coderank_sidecar.py`
- Modify: `internal/canary/gate.go`
- Modify: `internal/canary/gate_test.go`

**Interfaces:**
- Consumes: no shipped interface; CodeRank is imported only by the qualification module.
- Produces: the existing shipped `engine/embed` interface with static/Potion and Ollama adapters only.

- [ ] **Step 1: Change the canary test to reject a CodeRank exception**

Replace the test that expects `github.com/samibel/graphi/engine/embed/coderank` to be specially allowed with `TestAllowlistHasNoRetiredCodeRankException`, asserting `isAllowlistedPkg("github.com/samibel/graphi/engine/embed/coderank") == false`.

- [ ] **Step 2: Run the test and observe RED**

Run: `go test ./internal/canary -run 'CodeRank|Allowlist' -count=1`

Expected: FAIL because `outboundDialExactCodeRank` is still allowlisted.

- [ ] **Step 3: Delete the qualification implementation and exception**

Remove the files listed above and remove `outboundDialExactCodeRank` plus its branch from `internal/canary/gate.go`. Do not add a compatibility shim or Potion fallback.

- [ ] **Step 4: Verify the remaining embed and canary modules**

Run: `go test ./engine/embed/... ./internal/canary ./cmd/... -run 'Embed|Canary|Qualification|CodeRank' -count=1`

Expected: PASS; `go list ./...` must not report an import of the deleted package.

- [ ] **Step 5: Prove the retired surface is absent**

Run: `test -z "$(git ls-files cmd engine internal scripts | rg '(^|/)(coderank|embedded-model-qualification)(/|\.|$)' || true)"`

Expected: exit 0 with no output from tracked code or scripts. Documentation is removed in Task 7.

- [ ] **Step 6: Commit**

```bash
git add -A engine/embed/coderank cmd/embedded-model-qualification internal/eval/retrieval internal/canary scripts/eval
git commit -m "eval: remove unreleased CodeRank qualification"
```

### Task 3: Remove blind grading, qrel release evidence, and target policy

**Files:**
- Delete: all `internal/eval/retrieval/blindeval*.go`
- Delete: `internal/eval/retrieval/release_evidence.go`
- Delete: `internal/eval/retrieval/release_evidence_test.go`
- Delete: `internal/eval/retrieval/targetcheck.go`
- Delete: `internal/eval/retrieval/targets.go`
- Delete: `internal/eval/retrieval/targets_test.go`
- Delete: `internal/eval/retrieval/population.go`
- Delete: `internal/eval/retrieval/population_test.go`
- Delete: `internal/eval/retrieval/protected_artifacts_test.go`
- Modify: `internal/eval/retrieval/datasets_test.go`
- Delete: `cmd/retrieval-eval/blindeval.go`
- Delete: `cmd/retrieval-eval/blindeval_seal.go`
- Delete: `cmd/retrieval-eval/blindeval_test.go`
- Delete: `cmd/retrieval-eval/checktargets.go`
- Delete: `cmd/retrieval-eval/checktargets_test.go`
- Delete: `cmd/retrieval-eval/derive.go`
- Delete: `cmd/retrieval-eval/answerspan.go`
- Delete: `cmd/retrieval-eval/answerspan_test.go`
- Delete: `cmd/retrieval-eval/dev_grading_packets_test.go`
- Modify: `cmd/retrieval-eval/main.go`
- Modify: `cmd/retrieval-eval/main_test.go`

**Interfaces:**
- Consumes: retained evaluator entry points `retrieval.Evaluate`, aggregation, datasets in `internal/eval/retrieval/testdata`, and report writing.
- Produces: `cmd/retrieval-eval` as a deterministic local evaluator/aggregator only, with no release authorization, sealing, reader/grader, target derivation, or holdout commands.

- [ ] **Step 1: Narrow the command dispatch test**

Update `cmd/retrieval-eval/main_test.go` to assert the retained modes succeed and retired flags (`-blind-eval`, `-blind-eval-v2`, `-seal`, `-derive`, `-check-targets`, `-answer-span-ceiling`) are rejected as unknown flags with exit code 2.

- [ ] **Step 2: Run the dispatch test and observe RED**

Run: `go test ./cmd/retrieval-eval -run 'Retired|Flags|CLI' -count=1`

Expected: FAIL because the retired modes are still registered.

- [ ] **Step 3: Delete the retired command and library paths**

Remove the listed files, flags, mode branches, constants, and documentation strings. Keep only the dataset-backed deterministic evaluation and aggregation paths used by `cmd/differential` and `cmd/eval/labs_hero_test.go`.

Remove the answerable-population assertions from `datasets_test.go`; they define the retired holdout population rather than dataset parsing or scoring behavior. Keep schema, qrel, span, and deterministic scorer tests for retained evaluator inputs.

- [ ] **Step 4: Remove dead dependencies found by the compiler**

Run: `go test ./internal/eval/retrieval ./cmd/retrieval-eval ./cmd/differential ./cmd/eval -run '^$'`

Expected initially: compile errors naming any blind/target types still referenced. Remove those references; do not replace them with stubs.

- [ ] **Step 5: Verify retained evaluator behavior**

Run: `go test ./internal/eval/retrieval ./cmd/retrieval-eval ./cmd/differential ./cmd/eval -count=1`

Expected: PASS using only package-local fixtures.

- [ ] **Step 6: Commit**

```bash
git add -A internal/eval/retrieval cmd/retrieval-eval cmd/differential cmd/eval
git commit -m "eval: retire blind release authorization"
```

### Task 4: Remove development-only retrieval tuning tools and artifact-bound tests

**Files:**
- Delete: `cmd/compact-sufficiency-dev/`
- Delete: `cmd/payload-cost-dev/`
- Delete: `internal/eval/retrieval/answer_span_ceiling.go`
- Delete: `internal/eval/retrieval/answer_span_ceiling_test.go`
- Delete: `internal/eval/retrieval/answer_span_feasibility_dev_test.go`
- Delete: `internal/eval/retrieval/compact_sufficiency_dev.go`
- Delete: `internal/eval/retrieval/compact_sufficiency_dev_test.go`
- Delete: `internal/eval/retrieval/compact_wire_dev.go`
- Delete: `internal/eval/retrieval/compact_wire_dev_test.go`
- Delete: `internal/eval/retrieval/dev_grading_capture_test.go`
- Delete: `internal/eval/retrieval/draft_forecast_dev_test.go`
- Delete: `internal/eval/retrieval/equal_recall_dev.go`
- Delete: `internal/eval/retrieval/equal_recall_dev_test.go`
- Delete: `internal/eval/retrieval/followup_capture_test.go`
- Delete: `internal/eval/retrieval/followup_transcript.go`
- Delete: `internal/eval/retrieval/followup_transcript_test.go`
- Delete: `internal/eval/retrieval/grepread_dev_test.go`
- Delete: `internal/eval/retrieval/grepread_v2.go`
- Delete: `internal/eval/retrieval/grepread_v2_dev_test.go`
- Delete: `internal/eval/retrieval/grepread_v2_test.go`
- Delete: `internal/eval/retrieval/legacy_bundle_dev_capture_test.go`
- Delete: `internal/eval/retrieval/one_span_compact_dev_test.go`
- Delete: `internal/eval/retrieval/payload.go`
- Delete: `internal/eval/retrieval/payload_cost_dev.go`
- Delete: `internal/eval/retrieval/payload_cost_dev_test.go`
- Delete: `internal/eval/retrieval/product_compact_dev_test.go`
- Delete: `internal/eval/retrieval/recovery_dev_test.go`
- Delete: `internal/eval/retrieval/savings_aggregate.go`
- Delete: `internal/eval/retrieval/savings_aggregate_test.go`
- Delete: `internal/eval/retrieval/sw282_measure_test.go`
- Delete: `internal/eval/tokenizer/PIN_ROTATION.md`
- Delete: `internal/eval/tokenizer/pin_governance_test.go`
- Delete: `internal/eval/tokenizer/tokenizer.go`
- Delete: historical artifact tests `cmd/eval/g7jvmbaseline_characterization_test.go` and `cmd/eval/partialoutcome_characterization_test.go`
- Modify: `cmd/eval/partialoutcome_regression_test.go`
- Modify: `cmd/retrieval-eval/main.go`
- Modify: `cmd/retrieval-eval/main_test.go`
- Modify: `internal/eval/retrieval/real_tokenizer.go`
- Modify: `internal/eval/retrieval/real_tokenizer_test.go`
- Modify: `internal/eval/retrieval/taskcontext_test.go`
- Modify: `core/tokenizer/tokenizer_test.go`
- Modify: `engine/embed/static/pin_governance_test.go`
- Modify: `engine/embed/static/PIN_ROTATION.md`

**Interfaces:**
- Consumes: current product behavior tests and the static embedder's pin constants.
- Produces: no development-only command surface and no test that requires a historical run directory.

- [ ] **Step 1: Make current regression tests self-contained**

Move the small `agentContextPool`, `declaredAllowed`, and exact live allowed-set assertions needed by `partialoutcome_regression_test.go` into that file. Delete only the published-run replay assertions; retain the current rule that every agent-context operation admits `partial` and no invalid outcome.

- [ ] **Step 2: Replace static pin run enumeration with source-pin assertions**

Rewrite `TestStatic_PinRotationGovernance` to assert the current model revision, model SHA, tokenizer revision/SHA, CGo-free statement, and cross-architecture vector fixture. Remove `productionStaticRetrievalRuns`, `pinDependentRetrievalRuns`, and every historical run path from both the test and `PIN_ROTATION.md`.

- [ ] **Step 3: Run focused tests and observe failures before deleting artifacts**

Run: `go test ./cmd/eval ./engine/embed/static -count=1`

Expected before the rewrite is complete: tests still attempt to read `docs/eval/**/runs`. Expected after the rewrite: PASS without those directories.

- [ ] **Step 4: Delete the development-only files**

Remove the files listed above. Use compile failures to remove only symbols that have no retained caller; do not delete `internal/eval/retrieval`'s dataset, runner, metrics, aggregate, field-parity, report, tokenizer, RSS, or task-context primitives while they are still imported by retained commands/tests.

- [ ] **Step 5: Collapse the evaluator tokenizer shim onto the core module**

Change retained imports of `github.com/samibel/graphi/internal/eval/tokenizer` in `cmd/retrieval-eval` and `internal/eval/retrieval/real_tokenizer.go` to `github.com/samibel/graphi/core/tokenizer`. Update the tests' governed artifact path to `core/tokenizer/testdata/artifact`, then delete the alias-only `internal/eval/tokenizer` package and its duplicate pin record.

- [ ] **Step 6: Remove the artifact-bound task-context measurement test**

In `internal/eval/retrieval/taskcontext_test.go`, delete the test that loads `2026-09-02-sw264-task-context-v2-static-local`. Keep tests using `internal/eval/retrieval/testdata/datasets/cobra-v1.json` or hermetic temporary data. Change the tokenizer golden's path-shaped sample in `core/tokenizer/tokenizer_test.go` to `.graphi/eval-runs/2026-09-03-tokenizer/raw/task_context.jsonl` and update its expected token IDs/count.

- [ ] **Step 7: Prove no Go test reads historical runs**

Run: `rg -n 'docs/eval/(retrieval/)?runs/' --glob '*_test.go' cmd core engine internal surfaces`

Expected: no functional path references. A tokenizer string literal or explanatory comment may be rewritten to a neutral `.graphi/eval-runs/...` example rather than excepted.

- [ ] **Step 8: Verify affected packages**

Run: `go test ./cmd/eval ./cmd/retrieval-eval ./core/tokenizer ./engine/embed/static ./internal/eval/retrieval ./cmd/differential ./engine/agenttools/taskctx/... ./surfaces/... -count=1`

Expected: PASS.

- [ ] **Step 9: Commit**

```bash
git add -A cmd/compact-sufficiency-dev cmd/payload-cost-dev cmd/eval cmd/retrieval-eval core/tokenizer engine/embed/static internal/eval/retrieval internal/eval/tokenizer
git commit -m "eval: remove artifact-bound tuning tools"
```

### Task 5: Move retained evaluation output out of documentation

**Files:**
- Modify: `internal/evalreport/rawexport.go`
- Modify: `internal/evalreport/rawexport_test.go`
- Modify: `cmd/eval/rawexport.go`
- Modify: `cmd/eval/rawexport_test.go`
- Modify: `cmd/eval/fullrun.go`
- Modify: `.github/workflows/eval-full.yml`
- Modify: `.gitignore`

**Interfaces:**
- Consumes: `evalreport.RunDirName(date, runnerClass) string` and `evalreport.RunDirPath(date, runnerClass) string`.
- Produces: `evalreport.RunsRoot == ".graphi/eval-runs"`; explicit output paths continue to work unchanged.

- [ ] **Step 1: Change the path-contract test first**

Rename `TestRunDirPath_SitsBesideTheHistoricalRuns` to `TestRunDirPath_UsesIgnoredWorkspaceStorage` and assert:

```go
if RunsRoot != ".graphi/eval-runs" { t.Fatalf(...) }
if got := RunDirPath("2026-07-28", "ubuntu-latest"); got != ".graphi/eval-runs/2026-07-28-ubuntu-latest" { t.Fatalf(...) }
```

Add a test for `resolveExportDir(exportAuto, "ubuntu-latest", "2026-07-28")` with the same expected path.

- [ ] **Step 2: Run the tests and observe RED**

Run: `go test ./internal/evalreport ./cmd/eval -run 'RunDirPath|ExportDir' -count=1`

Expected: FAIL with the old `docs/eval/runs` root.

- [ ] **Step 3: Change the output root and help text**

Set `RunsRoot` to `.graphi/eval-runs`, update `exportAuto` comments/errors and full-run help text, and add `/.graphi/eval-runs/` to `.gitignore`.

- [ ] **Step 4: Update CI to use runner-temporary paths**

In `.github/workflows/eval-full.yml`, set exported run/report paths below `${{ runner.temp }}/graphi-eval/` and upload completed outputs with `actions/upload-artifact` pinned to a full commit SHA. Remove instructions that ask contributors to commit a run directory or fill budgets in the same PR.

- [ ] **Step 5: Verify local and workflow contracts**

Run: `go test ./internal/evalreport ./cmd/eval -count=1`

Run: `rg -n 'docs/eval/(retrieval/)?runs/' .github cmd internal/evalreport --glob '!**/*_test.go'`

Expected: tests PASS and no active output path remains under `docs/`.

- [ ] **Step 6: Commit**

```bash
git add internal/evalreport cmd/eval .github/workflows/eval-full.yml .gitignore
git commit -m "eval: store generated runs outside documentation"
```

### Task 6: Verify shipped product surfaces before the bulk deletion

**Files:**
- Modify only if a retained test still points at retired evaluation code: `cmd/graphi/*_test.go`, `surfaces/**/*_test.go`, `engine/retrieval/*_test.go`, `engine/agenttools/taskctx/**/*_test.go`

**Interfaces:**
- Consumes: shipped CLI, MCP, HTTP, embedder, and retrieval interfaces.
- Produces: evidence that the preceding deletions did not change published product behavior.

- [ ] **Step 1: Run public-surface golden and contract tests**

Run: `go test ./cmd/graphi ./surfaces/... ./engine/retrieval ./engine/agenttools/taskctx/... ./engine/embed/... -count=1`

Expected: PASS. If a failure is only an obsolete evidence-path assertion, remove that assertion; do not alter runtime bytes to satisfy the test.

- [ ] **Step 2: Run the CGo-free static build and privacy checks**

Run: `CGO_ENABLED=0 go build ./cmd/graphi`

Run: `go test ./internal/canary ./internal/audit ./surfaces/guard/... -count=1`

Expected: PASS; no new network exception and no CodeRank reference.

- [ ] **Step 3: Record a checkpoint commit only if fixes were needed**

```bash
git add cmd/graphi surfaces engine/retrieval engine/agenttools/taskctx engine/embed internal/canary internal/audit
git commit -m "test: preserve shipped retrieval surfaces"
```

Skip the commit when the task changed no files.

### Task 7: Delete generated evidence and obsolete documentation

**Files:**
- Delete: `docs/eval/runs/`
- Delete: `docs/eval/retrieval/runs/`
- Delete: `docs/eval/retrieval/harvests/`
- Delete: `docs/eval/retrieval/drafts/`
- Delete: `docs/eval/p0/`
- Delete: `docs/eval/retrieval/` after its generated subtrees are removed
- Delete: `docs/eval/retrieval-targets.json`
- Delete: `docs/eval/retrieval-budgets.json`
- Delete: `docs/superpowers/` (including this implementation plan after execution)
- Delete: all `scripts/eval/` files not already removed in Tasks 2–4
- Modify: `CHANGELOG.md`
- Modify: `readme.md`
- Modify: `docs/README.md`
- Modify: `docs/FEATURES.md`
- Modify: `docs/ci/bench.md`
- Modify: `docs/ci/release.md`
- Modify: any retained ADR or current guide with a broken link to a deleted path
- Modify: `.gitignore`

**Interfaces:**
- Consumes: the decoupled tests and output paths from Tasks 1–6.
- Produces: a current tree containing maintained user/architecture documentation, not generated evidence.

- [ ] **Step 1: Add ignore rules before deleting tracked runs**

Ignore both legacy generated roots (`/docs/eval/runs/`, `/docs/eval/retrieval/runs/`) as a safety net in addition to `/.graphi/eval-runs/`.

- [ ] **Step 2: Remove generated and retired trees with Git-aware deletion**

Use `git rm -r` on the listed directories. This is recoverable from commit history; do not use `git filter-repo`, `git gc`, or any history rewrite.

- [ ] **Step 3: Remove obsolete prose and scripts**

Delete the complete `docs/eval/retrieval/` documentation tree: CodeRank sidecar/qualification, preregistration, contract-v2, blind-eval threat-model/methodology, candidate-model, answer-span, qrel-target, drafts, harvests, and run records all describe the retired subsystem. Keep `hero-protocol.md`, `hero-budgets.json`, `reference-scenario.json`, static cross-architecture records, current ADRs, user guides, and active CI documentation.

- [ ] **Step 4: Update current documentation and changelog**

State in `CHANGELOG.md` that unreleased CodeRank/blind evaluation was retired, generated runs moved to CI artifacts, the general release gate no longer contains a model-promotion threshold, and Potion/Ollama/public surfaces are unchanged. Remove links and claims for commands or gates deleted in Tasks 1–4.

- [ ] **Step 5: Check for stale references**

Run:

```bash
rg -n -i 'coderank|embedded-model-qualification|qrel[_ -]?blind|blind[-_ ]eval|docs/eval/(retrieval/)?runs/' \
  --glob '!docs/adr/0014-retire-experimental-evaluation.md' \
  --glob '!CHANGELOG.md' .
```

Expected: no active code, workflow, or current-document reference. Historical mention in the ADR and one changelog retirement entry is allowed.

- [ ] **Step 6: Verify documentation paths**

Run the repository's existing documentation/citation tests with `go test ./internal/evidence ./cmd/evidence -count=1`. Fix retained links by pointing them to retained current docs or removing the obsolete claim; do not restore deleted evidence to satisfy a citation.

- [ ] **Step 7: Record the cleanup size**

Run:

```bash
git ls-files docs | wc -l
git ls-files -z docs | xargs -0 stat -f '%z' | awk '{s+=$1} END {print s+0}'
```

Expected: a material reduction from 13,696 tracked documentation files and 811,745,116 bytes. Add the exact post-cleanup figures to the ADR's implementation-status addendum or `CHANGELOG.md`.

- [ ] **Step 8: Commit**

```bash
git add -A docs scripts/eval .gitignore CHANGELOG.md readme.md
git commit -m "docs: remove generated evaluation archive"
```

### Task 8: Whole-repository verification and final hygiene

**Files:**
- Modify only for defects revealed by verification.

**Interfaces:**
- Consumes: the completed branch.
- Produces: a buildable, tested, reviewable cleanup ready for integration.

- [ ] **Step 1: Verify no forbidden tracked artifacts remain**

Run:

```bash
test -z "$(git ls-files 'docs/eval/runs/**' 'docs/eval/retrieval/runs/**')"
test -z "$(git ls-files | rg -i 'coderank|embedded-model-qualification|qrel[_-]?blind|blindeval' || true)"
```

Expected: both commands exit 0 with empty output, except the accepted ADR/changelog references must be excluded explicitly from the second command.

- [ ] **Step 2: Build all modules**

Run: `go build ./...`

Expected: PASS.

- [ ] **Step 3: Test all Go modules**

Run: `go test ./...`

Expected: PASS.

- [ ] **Step 4: Run web verification**

Run: `npm ci && npm test -- --run && npm run build` from `web/`.

Expected: PASS.

- [ ] **Step 5: Run the release checks**

Run: `go test ./cmd/release-gate ./cmd/release ./internal/release -count=1`

Run: `CGO_ENABLED=0 go run ./cmd/release -version cleanup-verification -verify-only`

Expected: PASS. Do not require the old qrel/model threshold.

- [ ] **Step 6: Check formatting, generated files, and diff**

Run: `gofmt -w` only on changed Go files, then `git diff --check` and the repository's generation/drift checks already used by CI.

Expected: clean output and no regenerated evaluation archive.

- [ ] **Step 7: Review the deletion summary**

Run: `git diff --stat main...HEAD` and `git status --short --branch`.

Expected: the large deletion is confined to retired evaluation/evidence paths plus the documented gate/output updates; the worktree is clean after the final commit.

- [ ] **Step 8: Commit any verification fixes**

```bash
git add -A
git commit -m "chore: finish experimental evaluation retirement"
```

Skip this commit when verification required no changes.
