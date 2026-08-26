# json — W5.n real-repository parse-determinism, 2026-08-26 (SW-200)

> **This leaf holds a PARITY run, not a performance run.** No latency, no
> percentile and no RSS figure appears anywhere in it. The directory convention
> in [`../README.md`](../README.md) was written for `cmd/eval` runs; AC-2 of
> SW-200 names this path for parity raw samples, and the mismatch is why
> `cmd/eval -aggregate` cannot read it (measured below, and filed as
> PARITY-012).

## Machine, identified

Measured on **2026-08-26**, on this machine, by running the commands below.

| field | value |
|---|---|
| cpu_model | Apple M2 Max |
| cpu_count | 12 |
| ram_bytes | 68719476736 |
| os / arch | darwin / arm64 |
| kernel | 25.6.0 |
| go_version | go1.26.6 darwin/arm64 |
| filesystem | apfs (`/var/tmp/sw200-campaign`), cache **not dropped** |
| runner_class | `Darwin-ARM64/apple-m2-max` |
| measured_sha | `733ac0047c6b586756986d86d65dad819a6494a2` |
| candidate_sha | `9f687849cec2b26311401191e90b60e40b5f6cee` — **not moved by this story** (AC-6) |

Every field above is also in [`environment.json`](environment.json).

## What was run, and what came back

Two postures, two dispatches each, separate workdirs, serial.

```
go run ./cmd/parity -family json -manifest corpus/manifest.json -max-tier 3 \
  -runner-class "Darwin-ARM64/apple-m2-max" -workdir <per-dispatch> -report <a|b>.json
go run ./cmd/parity -family json -manifest corpus/manifest.json -max-tier 1 -allow-local \
  -runner-class "Darwin-ARM64/apple-m2-max" -workdir <per-dispatch> -report <a|b>.json
```

| posture | rows | verdicts (dispatch a) | exit | what it is |
|---|---|---|---|---|
| **realrepo** (`-max-tier 3`, local fixtures refused) | 12 | SKIPPED × 12 | 2 | **the G4 evidence posture** |
| **fixture** (`-max-tier 1 -allow-local`) | 12 | PASS × 0, SKIPPED × 12 | 2 | exercises the driver; **NOT G4 evidence** |

> **json has NO manifest entry at any tier**, so even the fixture posture
> abstains: SW-202 shipped tier-1 hero fixtures for css, hcl, markdown, toml
> and yaml but **not** for json, whose G6 row it left UNKNOWN on the ground
> that `ambiguous` is provably unreachable at parse-only. This language
> therefore has **zero exercised rows** on this tree, and that is recorded
> rather than filled in from a sibling language's run.


Dispatch b is identical to dispatch a in every verdict, every per-row count and
every snapshot digest. That is the two-dispatch agreement, and it is recorded by
the three diff modes:

| mode | exit | what the tool printed |
|---|---|---|
| `-verdict-diff` (realrepo) | 2 | parity: verdict sets agree, but at least one run is NOT publishable — publication refused. |
| `-counts-diff` (realrepo) | 2 | parity: counts agree, but at least one run is NOT publishable — publication refused. |
| `-refusal-diff` (realrepo) | 0 | parity: the two dispatches refuse for bit-identically the same reasons — the refusal is DETERMINISTIC. This is NOT a publication: both runs are refused, and exit 0 here says only that they are refused identically. |
| `-verdict-diff` (fixture) | 2 | parity: verdict sets agree, but at least one run is NOT publishable — publication refused. |
| `-counts-diff` (fixture) | 2 | parity: counts agree, but at least one run is NOT publishable — publication refused. |
| `-refusal-diff` (fixture) | 0 | parity: the two dispatches refuse for bit-identically the same reasons — the refusal is DETERMINISTIC. This is NOT a publication: both runs are refused, and exit 0 here says only that they are refused identically. |

Raw samples, checked in beside this file:
`raw/{realrepo,fixture}-{a,b}.parity.json` (the machine-readable reports),
`raw/{realrepo,fixture}-{a,b}.stderr.log` (the runner's own log, verbatim), and
`raw/{realrepo,fixture}-{verdict,counts,refusal}-diff.log` (the comparisons).

## G4 disposition for json: **UNKNOWN**

`GA-LANG-json-G4` stays UNKNOWN. The two-dispatch agreement HELD — both
dispatches produced identical verdict sets, identical per-row counts and
identical snapshot digests — but AC-3's PASS branch is a **conjunction**, and
its other conjunct is false. Three mechanisms refuse, each measured here rather
than assumed:

1. **No real-repository pin.** `corpus/manifest.json` at `733ac0047c6b586756986d86d65dad819a6494a2` declares no
   `json` entry above tier 1. SW-201's v3 pins exist only on the unmerged
   branch `sw-201-w5o-corpus-pins-v3-intra-parse` (75 behind `main`, 4 ahead,
   no PR) — and json has no pin even there, because SW-201 filed it as an
   honest `no_pin` abstention. Every realrepo row therefore SKIPS with the
   reason in its `detail`.
2. **Publication is refused while the candidate does not match the bytes.**
   `./cmd/graphi` built with `-trimpath -buildvcs=false`, `CGO_ENABLED=0`
   gives `af8b971b2bd3e1c1…` at `733ac0047c6b586756986d86d65dad819a6494a2` and `0de6e64d6174f179…` at candidate
   `9f687849`. `internal/parity/provenance.go` fails closed on that difference,
   so `Report.Publishable` is false and `-verdict-diff` / `-counts-diff` exit **2**
   on "publication refused" even when the two dispatches agree. This is
   `D7DEBT-001`, unpaid; **SW-200 AC-6 forbids this story moving the candidate.**
3. **The family is below the FR-7 completeness floor.** Six declared change
   classes crossed over the two dispatch-reachable axes is **12**, and
   `parityreport.Report.Finalize` requires `>= 15`. The refusal string says so
   verbatim. Filed as `PARITY-011`.

Any one of the three alone would keep the row UNKNOWN. A PASS on this tree
would have had to be manufactured, and was not.

## `cmd/eval -aggregate` on this leaf — **exit 2**, measured

```
$ go run ./cmd/eval -aggregate docs/eval/runs/2026-08-26-Darwin-ARM64/json
eval: evalreport: read run.json: open docs/eval/runs/2026-08-26-Darwin-ARM64/json/run.json: no such file or directory
exit status 2
```

AC-2 requires exit 0 here. It is **unreachable for a parity run**, and that was
established by control arm rather than inferred: a *well-formed* `evalreport`
run directory (`run.json` + `report.json` + this `environment.json`) whose
published report is a parity run exits **3**, because
`evalreport.Reproduce` sets `Complete = Checked > 0 && Unknown == 0` and a
parity run publishes none of the four series the aggregator reproduces
(cold_index, query_latency, incremental, progress_stalls — the tool prints
"not published by this run" for all four).

Both ways past it are refused by rules already recorded in this repository:

- Teaching `internal/evalreport` a parity series is forbidden by
  `internal/parity/classes.go:15-24` ("This harness keeps its own binding, to
  this YAML, and never touches internal/evalreport") and by
  `internal/parity/doc.go`'s instrument boundary.
- Publishing an `cmd/eval` performance run into this leaf so the aggregator
  goes green would put a latency measurement under a G4 parity heading. That is
  the substitution the whole gate exists to prevent.

Filed as `PARITY-012`. **AC-2's second half is therefore reported unimplementable
as written, not worked around.**
