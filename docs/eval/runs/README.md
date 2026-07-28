# Measurement runs — the directory convention

Every performance run graphi publishes lives in exactly one directory here, and
the name of that directory is a rule rather than a habit (SW-128, FR-9):

```
docs/eval/runs/<date>-<runner-class>/
```

`<date>` is `YYYY-MM-DD` in UTC and `<runner-class>` is the runner class the run
declared, lowercased and hyphenated. Two runs of the same class on the same day
share a directory; two classes never do. The name is produced by
`evalreport.RunDirName`, so a run cannot land somewhere a reader does not look.

## What is inside one

```
2026-07-28-ubuntu-latest/
├── run.json            table of contents: versions, environment, file digests
├── environment.json    the machine and the provenance the run was produced under
├── report.json         the PUBLISHED aggregate — percentiles, gates, verdicts
├── aggregate.json      the reproduction: every published number, recomputed
└── raw/
    ├── cold-index.json       SW-124 — one record per cold run
    ├── query-latency.json    SW-125 — every timed execution, plus pool membership
    ├── incremental.json      SW-126 — one record per change
    └── progress-stalls.json  SW-127 — one record per interval between events
```

The split is the point. **`raw/` carries samples and nothing derived** — no
percentile, no aggregate, no verdict. That is what makes checking `report.json`
against it a real check rather than a comparison of a number with a file that
already contains it.

## Reproducing the numbers

```sh
go run ./cmd/eval -aggregate docs/eval/runs/2026-07-28-ubuntu-latest
```

It recomputes **every** statistic `report.json` publishes from `raw/`, writes
`aggregate.json`, and exits with a code that says which of four things happened:

| Exit | Meaning |
|------|---------|
| `0` | every published metric reproduced and the environment is documented — **publishable** |
| `1` | **discrepancy**: a published number does not follow from its raw samples |
| `2` | unreadable: not a run directory, or a format/harness version this build does not define |
| `3` | **incomplete**: nothing contradicted, but something is unmeasured or undocumented |

`3` is deliberately not `1`. "The number is wrong" and "the number cannot be
checked" are different facts, a CI job reacts to them differently, and merging
them would let a real discrepancy be triaged as a flaky job. Neither is `0`: an
aggregate without raw data behind it is not published.

## Producing one

The reference-scenario jobs in `.github/workflows/eval-full.yml` export a run
directory and reproduce it in the same job. To produce one by hand:

```sh
go run ./cmd/eval -manifest corpus/manifest.json -full-run grpc-go \
  -runner-class ubuntu-latest \
  -reference-scenario docs/eval/reference-scenario.json \
  -candidate docs/rc/evidence-index.yaml \
  -export-raw auto            # or an explicit directory
```

`-export-raw auto` applies the convention above; an explicit path is for CI,
which exports to the workspace and uploads the directory as an artifact.

## Versioning, and why a directory can be refused

`run.json` and every raw file carry two versions:

- **`format_version`** — the file *shape*. A directory written by a version this
  build does not define is refused whole rather than half-read.
- **`harness_version`** — the measurement *method*: what is inside a timed
  region, which runs count, how a sample is produced. Two runs whose harness
  versions differ did not measure the same question.

A directory whose raw files **disagree** about `harness_version` is refused
outright (exit `2`). That is the defence against the silent methodology drift in
the P0 risk register: an old and a new method are not one measurement, and a
warning would not stop them being averaged.

## Environment

`environment.json` records CPU, RAM, OS, kernel, Go version, filesystem and
page-cache state, plus the runner class, the frozen candidate SHA, and the
harness and scorer versions. A field that could not be read is **absent**, and
`aggregate.json` renders it `UNKNOWN` with the reason the probe failed — an
empty `kernel` never reads as a documented kernel. A run whose environment is
incomplete is not publishable, however cleanly its arithmetic reproduces.

## Existing directories

`2026-07-15-local-sandbox/` and `2026-07-15-ubuntu-latest/` predate this
convention. They hold aggregates only — no `raw/`, no environment capture and
no reproduction — which is exactly the gap SW-128 closes; see their own READMEs
for what they do and do not establish. They are historical evidence and are not
rewritten.
