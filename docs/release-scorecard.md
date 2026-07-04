# Eval Report

**Timestamp:** 2026-07-04T17:18:21Z
**Version:** dev
**Commit:** fd2be7c899de689d470ed2891971c7508b4c465d
**OS/Arch:** linux/amd64
**Corpus version:** 1

## Scorecard

**Overall:** 100.0
**Pass:** true
**Baseline → Target:** 65.0 → 90.0

### Breakdown

| Area | Score | Weight | Contribution | Below Floor | Provenance |
|------|-------|--------|--------------|-------------|------------|
| agent_mcp | 100.0 | 25 | 25.00 | false | measured |
| evaluation | 100.0 | 10 | 10.00 | false | measured |
| performance | 100.0 | 20 | 20.00 | false | measured |
| setup_trust | 100.0 | 15 | 15.00 | false | measured |
| signal | 100.0 | 20 | 20.00 | false | measured |
| ux | 100.0 | 10 | 10.00 | false | measured |


## Per-Repo Metrics

| Name | Tier | Pass | Duration (ms) |
|------|------|------|---------------|


## Per-Scenario Results

| ID | Fixture | Operation | Outcome | Size | Latency (ms) | Anchor Present | Tier 1 |
|----|---------|-----------|---------|------|--------------|----------------|--------|
| agent-brief-fixture | tier1-fixture-go | agent_brief | pass | 2675 | 0 | true | true |
| anchor-absent | tier1-fixture-go | search | pass | 35 | 0 | true | true |
| change-risk-known-symbol | tier1-fixture-go | change_risk | pass | 1167 | 0 | true | true |
| empty-result | tier1-fixture-go | search | pass | 0 | 0 | true | true |
| explain-ambiguous-symbol | tier1-fixture-go | explain_symbol | pass | 1295 | 0 | true | true |
| explain-known-symbol | tier1-fixture-go | explain_symbol | pass | 940 | 0 | true | true |
| go-symbol | tier1-fixture-go | search | pass | 35 | 0 | true | true |
| related-files-single-file | tier1-fixture-go | related_files | pass | 298 | 0 | true | true |
| signal-default-high-confidence | synthetic-signal | diagnose | pass | 247 | 0 | true | true |
| signal-external-imports-aggregated | synthetic-signal | diagnose | pass | 1106 | 0 | true | true |
| signal-generated-suppressed | synthetic-signal | diagnose | pass | 247 | 0 | true | true |
| signal-safe-delete-gated | synthetic-signal | diagnose | pass | 1106 | 0 | true | true |
| signal-testcode-suppressed | synthetic-signal | diagnose | pass | 247 | 0 | true | true |


## Regressions vs Baseline

No regressions detected.
