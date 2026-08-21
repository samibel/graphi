---
project: graphi
slug: ga-language-withdrawals-2026-08-21
created: 2026-08-21
shaping: SW-178 matrix-discipline, applied per owner decision 2026-08-21
status: active
---

# GA-language matrix-discipline withdrawals — 2026-08-21

Per SW-178 ("a matrix row is in place ONLY when its gate is green"), 21 of the 22
ga-language rows added by SW-186 are withdrawn on 2026-08-21. Each row carries a
re-introducer story id below.

The owner chose matrix-discipline on 2026-08-21 to make `cmd/coverage -check` pass
on PR #128 (F5-dispatch Voll-Lösung: 198 rows flipped to PASS where possible; the
remaining 86 UNKNOWN rows correspond to gates whose F5 work is multi-session epic,
filed as SW-187..SW-203 in `projects/graphi/stories/`, all `status: open`).

## Withdrawn rows (21)

| Language | Re-introducer story | Notes |
|---|---|---|
| java | SW-188 (JVMSOUND closure done 2026-08-20) + SW-190 (JVM G4 closure) | G2SUB flipped PASS in `c5abfef`; G4 stays UNKNOWN until SW-190 lands (PARITY-COV-001 + owed candidate move) |
| kotlin | SW-188 + SW-190 | same as java |
| python | SW-192 (Python G4 on flask) | G4 honest UNKNOWN per SW-181 AC-9 — PYTHONFANOUT-001 (python resolver fans out `import typing as t` into `tests/typing/` package) |
| typescript | SW-193 (TS family G4 on ky + express) | G4 honest UNKNOWN — PARITY-TS-FAMILY-DRIVER-001 (`internal/parity/` has no `-family typescript` driver; SW-176-style multi-commit W6 scope) |
| tsx | SW-193 + family-share judgement | same PARITY-TS-FAMILY-DRIVER-001 blocker; family-share judgement is provisional |
| javascript | SW-193 | same PARITY-TS-FAMILY-DRIVER-001 blocker |
| bash | SW-194 + SW-195 + SW-196 + SW-197 + SW-198 (5 streams) | no parity-class YAML, no corpus pin, no hero fixture, no perf campaign, no real-repo parity |
| c | SW-194 + SW-195 + SW-196 + SW-197 + SW-198 | same; c/cpp shared resolver |
| c_sharp | SW-194 + SW-195 + SW-196 + SW-197 + SW-198 | same |
| cpp | SW-194 + SW-195 + SW-196 + SW-197 + SW-198 | same as c (shared resolver) |
| lua | SW-194 + SW-195 + SW-196 + SW-197 + SW-198 | same |
| php | SW-194 + SW-195 + SW-196 + SW-197 + SW-198 | same |
| ruby | SW-194 + SW-195 + SW-196 + SW-197 + SW-198 | same; sinatra pin lifted to 40-char sha `b626e2d82c23b4fde0b51782fd32ca27ccde1d1a` (commit `f7b3f47`) |
| rust | SW-194 + SW-195 + SW-196 + SW-197 + SW-198 | same |
| sql | SW-194 + SW-195 + SW-196 + SW-197 + SW-198 | same; SW-183 noted sql's over-claim shape |
| css | SW-199 + SW-200 + SW-201 + SW-202 + SW-203 | intra-file-only; abstention witnesses |
| hcl | SW-199 + SW-200 + SW-201 + SW-202 + SW-203 | intra-file-only; SW-185 noted hcl as the strongest "no permissively-licensed repo" case |
| json | SW-199 + SW-200 + SW-201 + SW-202 + SW-203 | parse-only; vacuous-pass risk |
| markdown | SW-199 + SW-200 + SW-201 + SW-202 + SW-203 | intra-file-only |
| toml | SW-199 + SW-200 + SW-201 + SW-202 + SW-203 | intra-file-only |
| yaml | SW-199 + SW-200 + SW-201 + SW-202 + SW-203 | intra-file-only |

## Honesty notes

- The PASS rows that flipped in PR #128 (citation-only set per commit `dad3092`)
  are NOT removed. They live in `docs/rc/evidence-index.yaml` and will be re-bound
  when the matrix rows are re-introduced.
- The surface sweep that named all 22 languages "GA" in `docs/language-support.md`
  (commit `157d1de`) is reverted for the 21 withdrawn languages; those rows go
  back to "Preview" until the matrix row is re-introduced.
- The user-facing capability surface is **intentionally reduced** until the F5 work
  lands. Go is the only language at GA. Per the spec's naming rule:
  `Go — GA (typed-confirmed)`, never GA alone.
- This is NOT a revert of SW-186: SW-186 added the rows. This discipline is the
  staging the WP-J11 flip gate (SW-178) specifies — the matrix row stays in place
  ONLY when its gate is green. The withdrawal is reversible per-row.

## Re-introduction

Each withdrawn row is re-introduced by its named story in `projects/graphi/stories/`
once its G1..G9 evidence rows are all PASS. The matrix row is then uncommented in
`docs/coverage-matrix.yaml` and `cmd/coverage -check` must pass before the
re-introduction commit lands.
