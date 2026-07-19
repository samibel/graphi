# graphi documentation map

This folder mixes four different kinds of files on purpose — user docs,
contributor docs, machine-written evidence, and project planning. This index
says which is which, so the difference stays visible.

## User & product documentation

| Doc | What it is |
|---|---|
| [HOWTO.md](HOWTO.md) | Install, index a repo, use every surface — start here |
| [FEATURES.md](FEATURES.md) | Complete catalogue of MCP tools, CLI subcommands, HTTP endpoints, analyzers |
| [stability-tiers.md](stability-tiers.md) | **Canonical** definition of the GA / Preview / Labs / Source-only tiers |
| [query-language.md](query-language.md) | Query language reference |
| [agent-workflows.md](agent-workflows.md) | Recommended MCP call order for AI agents, per-client examples |
| [setup-privacy.md](setup-privacy.md) | `graphi setup` + `graphi privacy-audit` |
| [real-world-report.md](real-world-report.md) | Before/after record of the two external field tests, every row reproducible |
| [tutorial/](tutorial) | Using graphi with the Claude CLI |
| [surfaces-http.md](surfaces-http.md) · [surfaces-tui.md](surfaces-tui.md) · [surfaces-web.md](surfaces-web.md) · [surfaces-vscode.md](surfaces-vscode.md) · [surfaces-wiki.md](surfaces-wiki.md) | Per-surface documentation |

## Architecture & contributor documentation

| Doc | What it is |
|---|---|
| [architecture-plan.md](architecture-plan.md) | The single design entry point: layers, data flow, CI gates |
| [graphi-broad.md](graphi-broad.md) | The opt-in CGO grammar flavor |
| [default-tier-security.md](default-tier-security.md) | Security controls behind the CGo-free / zero-egress default tier |
| [adr/](adr) | Architecture decision records |
| [decisions/](decisions) | Project decision records (cited by the RC evidence index) |
| [build/](build) · [ci/](ci) · [context/](context) · [edit/](edit) · [ledger/](ledger) · [meter/](meter) · [price/](price) · [savings/](savings) | Per-subsystem docs |
| [history/](history) | Historical before/after engineering change records — see its README |

## Machine-written / CI-wired files — do not move or hand-edit

These paths are hard-coded in Go code and GitHub workflows. Renaming or moving
them breaks gates; the generated ones are overwritten on the next run.

| File | Written / checked by |
|---|---|
| [coverage-matrix.md](coverage-matrix.md) | Generated from [coverage-matrix.yaml](coverage-matrix.yaml) by `go run ./cmd/coverage -generate`; drift fails CI (`internal/coverage`) |
| [capability-manifest.json](capability-manifest.json) | Generated alongside the coverage matrix |
| [release-scorecard.md](release-scorecard.md) / [release-scorecard.json](release-scorecard.json) | Published by the release gate (`cmd/release-gate`) |
| [eval-baseline.json](eval-baseline.json) · [mcp-tool-baseline.json](mcp-tool-baseline.json) | Ratchet baselines read by `cmd/eval` / `cmd/release-gate` |
| [eval/](eval) | Hero protocol, budgets, and checked-in run evidence (`eval-full.yml` CI) |
| [rc/](rc) | RC evidence index — [rc/evidence-index.md](rc/evidence-index.md) is generated from [rc/evidence-index.yaml](rc/evidence-index.yaml) by `go run ./cmd/evidence` |

## Planning — not product documentation

| Doc | What it is |
|---|---|
| [plan/2026-07-graphi-9of10-execution-plan.md](plan/2026-07-graphi-9of10-execution-plan.md) | The **single active planning authority** |
| [plan/reviews/](plan/reviews) | The seven external expert reviews (July 2026 snapshot) the plans were built on |
| [plan/superseded/](plan/superseded) | Archived earlier plans — kept verbatim, no longer binding |
