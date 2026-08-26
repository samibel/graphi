# PARITY-012 control arm — `cmd/eval -aggregate` exits **3**, not 2, on a well-formed directory

**This directory is not a measurement, and it is not a language leaf.** It is a
control, and the only reason it is checked in is that a control nobody can open
is an assertion.

## The claim it exists to make falsifiable

SW-200's six language leaves each record `cmd/eval -aggregate <leaf>` → **exit
2**, `read run.json: no such file or directory`. On its own that proves only
that two files are missing, which would make PARITY-012 a paperwork problem
rather than a structural one. The stronger claim — *the aggregator cannot exit 0
on a parity run **even when the directory is perfectly well-formed*** — needs a
well-formed directory to be shown on.

## What is in here, and where each byte came from

| file | provenance |
|---|---|
| `report.json` | a **byte copy** of `../css/raw/realrepo-a.parity.json` — a real `parityreport` run from this campaign, unedited |
| `environment.json` | a **byte copy** of `../css/environment.json` — the same machine record the css leaf publishes |
| `run.json` | written for this control: `format_version: 1`, `harness_version: p0-perf/1`, `scorer_version: p0-aggregate/1`, `report: "report.json"`, `raw: []`, and the environment above embedded, because `evalreport.ReadRunDir` reads the run's environment out of the index rather than out of `environment.json` |
| `aggregate.json` | **written by the tool**, not by hand — this is `cmd/eval`'s own output, and it is the difference between this leaf and the SW-192 precedent leaf whose `aggregate.json` was hand-written |
| `aggregate-exit3.log` | the captured run, with the exit code |

Nothing here is a parity result and no evidence-index row cites this directory.

## The captured run

```
$ go build -trimpath -buildvcs=false -o eval ./cmd/eval   # CGO_ENABLED=0
$ ./eval -aggregate docs/eval/runs/2026-08-26-Darwin-ARM64/aggregate-control-arm
eval: wrote the aggregate reproduction to .../aggregate-control-arm/aggregate.json
eval: aggregate UNKNOWN (harness p0-perf/1, scorer p0-aggregate/1)
eval:   cold_index       not published by this run
eval:   query_latency    not published by this run
eval:   incremental      not published by this run
eval:   progress_stalls  not published by this run
eval: INCOMPLETE - this run is not publishable (0 metric(s) UNKNOWN)
exit status 3
```

## Why 3 and not 0, read off the artifact rather than off the source

`aggregate.json` records `"environment_complete": true` and **no**
`missing_environment`, so the machine is fully documented and the environment
half is not what refuses. It records `metrics_checked: 0`, `metrics_unknown: 0`,
`metrics_discrepant: 0` and `complete: false`. That is `evalreport.Reproduce`'s

```go
out.Complete = out.Checked > 0 && out.Unknown == 0
```

with `Checked` at zero: `Checked` is incremented once per metric of the four
`internal/evalreport` series, and **a `parityreport.Report` publishes none of
them, by design**. Zero checks passing zero comparisons is the false green that
line exists to refuse, and it refuses correctly. So the exit code is
`exitAggregateIncomplete` = **3** (`cmd/eval/aggregate.go:34`), which is a
different fact from the leaves' **2** (`exitAggregateUsage`, an unreadable
directory) — and PARITY-012 depends on that difference.

## What this control does NOT license

It does not make `-aggregate` applicable to a parity run, and it is not a way of
satisfying SW-200 AC-2's second half. Both routes past PARITY-012 stay
forbidden: teaching `internal/evalreport` a parity series
(`internal/parity/classes.go:15-24`, `internal/parity/doc.go`), and publishing a
`cmd/eval` performance run into a G4 parity leaf. The decision that is owed is
in PARITY-012, and it is an owner's, not a builder's.
