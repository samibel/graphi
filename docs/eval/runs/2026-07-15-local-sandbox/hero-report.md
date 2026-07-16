# Eval Report

> Historical internal sandbox snapshot for the commit shown below. It is not a
> current release result, independent project rating, production certification,
> or competitor benchmark.

**Timestamp:** 2026-07-15T00:17:07Z
**Version:** 0.0.0-dev
**Commit:** 660b5a32e7c67a1cc40446bea44d99666e9d75f2
**OS/Arch:** linux/amd64
**Corpus version:** 2

## Scorecard

**Overall:** 96.2
**Pass:** false
**Baseline → Target:** 65.0 → 90.0

### Breakdown

| Area | Score | Weight | Contribution | Below Floor | Provenance |
|------|-------|--------|--------------|-------------|------------|
| agent_mcp | 100.0 | 25 | 25.00 | false | measured |
| evaluation | 100.0 | 10 | 10.00 | false | measured |
| performance | 100.0 | 20 | 20.00 | false | measured |
| setup_trust | 100.0 | 15 | 15.00 | false | measured |
| signal | 100.0 | 20 | 20.00 | false | measured |
| ux | 62.0 | 10 | 6.20 | true | carried |


## Per-Repo Metrics

| Name | Tier | Pass | Duration (ms) |
|------|------|------|---------------|


## Per-Scenario Results

| ID | Fixture | Operation | Outcome | Size | Latency (ms) | Anchor Present | Tier 1 |
|----|---------|-----------|---------|------|--------------|----------------|--------|
| hero-01-index-inventory | tier1-fixture-hero-go | index | pass | 78 | 0 | true | true |
| hero-02-search-hit | tier1-fixture-go | search | pass | 35 | 0 | true | true |
| hero-03-search-empty | tier1-fixture-go | search | pass | 0 | 0 | true | true |
| hero-04-definition-leaf-empty | tier1-fixture-go | definition | pass | 0 | 0 | true | true |
| hero-05-definition-unknown-not-found | tier1-fixture-go | definition | pass | 0 | 0 | true | true |
| hero-06-references-type-usage | tier1-fixture-hero-go | references | pass | 189 | 0 | true | true |
| hero-07-callers-direct | tier1-fixture-go | callers | pass | 202 | 0 | true | true |
| hero-08-callers-ambiguous | tier1-fixture-go | callers | pass | 263 | 0 | true | true |
| hero-09-callers-cross-file | tier1-fixture-hero-go | callers | pass | 280 | 0 | true | true |
| hero-10-callees-single-hop | tier1-fixture-go | callees | pass | 95 | 0 | true | true |
| hero-11-agent-brief-topic | tier1-fixture-go | agent_brief | pass | 2675 | 0 | true | true |
| hero-12-neighborhood-depth1 | tier1-fixture-go | neighborhood | pass | 324 | 0 | true | true |
| hero-13-neighborhood-depth2 | tier1-fixture-go | neighborhood | pass | 1636 | 0 | true | true |
| hero-14-impact-blast-radius | tier1-fixture-go | impact | pass | 280 | 0 | true | true |
| hero-15-impact-forward-deps | tier1-fixture-go | impact | pass | 199 | 0 | true | true |
| hero-16-explain-symbol-cited | tier1-fixture-go | explain_symbol | pass | 940 | 0 | true | true |
| hero-17-explain-symbol-partial | tier1-fixture-go | explain_symbol | pass | 652 | 0 | true | true |
| hero-18-explain-symbol-ambiguous | tier1-fixture-go | explain_symbol | pass | 1295 | 0 | true | true |
| hero-19-related-files-dependents | tier1-fixture-hero-go | related_files | pass | 758 | 0 | true | true |
| hero-20-change-risk-inbound | tier1-fixture-hero-go | change_risk | pass | 1347 | 0 | true | true |


## Regressions vs Baseline

No regressions detected.



## Performance Warnings

- area ux carried from baseline (62), not measured by this run
- suite corpus/hero not diffed against docs/eval-baseline.json (baseline covers the default suite only)
