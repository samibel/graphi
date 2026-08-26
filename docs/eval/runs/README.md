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
├── raw/
│   ├── cold-index.json       SW-124 — one record per cold run
│   ├── query-latency.json    SW-125 — every timed execution, plus pool membership
│   ├── incremental.json      SW-126 — one record per change
│   └── progress-stalls.json  SW-127 — one record per interval between events
└── profiles/                 SW-129 — present ONLY when a gate was missed
    ├── profiles.json         which gate each set answers for, with digests
    └── <scenario>/           cpu.pprof, heap.pprof, allocs.pprof, io.pprof
```

The split is the point. **`raw/` carries samples and nothing derived** — no
percentile, no aggregate, no verdict. That is what makes checking `report.json`
against it a real check rather than a comparison of a number with a file that
already contains it.

`profiles/` is absent from a green run, and its absence is a statement: the
profiler is never started when no gate was missed, because a harness that
profiled every run would distort the numbers it exists to establish. When it is
present, `run.json` and `report.json` both point at it, each set naming the gate
it answers for — that is the citation PRD §8.5 asks a fix to make. The profiles
come from a diagnostic **re-execution** of the affected scenario, and `io.pprof`
is the runtime block profile; both caveats, and how to read the files, are in
[`../hero-protocol.md`](../hero-protocol.md).

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

`2026-07-28-ubuntu-latest/` is the **published P0 baseline** (SW-130): two
complete runs on the frozen candidate v0.7.0 at `5815db5`, over the five pinned
Go repositories, with raw samples, environment capture, in-CI reproduction and
the profiles the one missed gate produced. Start at its `README.md`, and read
the numbers in `p0-baseline.md`.

It deviates from the one-directory-per-run shape above in one visible way, and
the reason is in the workflow rather than in the convention: the four
measurement families run as separate CI jobs on separate machines, so each
exports its own run directory. They are grouped `run-<a|b>/<family>/<repo>/`,
and each of those leaf directories is a run directory exactly as described
above — `-aggregate` reads any of them directly.

`2026-07-15-local-sandbox/` and `2026-07-15-ubuntu-latest/` predate this
convention. They hold aggregates only — no `raw/`, no environment capture and
no reproduction — which is exactly the gap SW-128 closes; see their own READMEs
for what they do and do not establish. They are historical evidence and are not
rewritten.

## 2026-08-26 — one directory here is NOT a performance run (SW-200, W5.n)

`2026-08-26-Darwin-ARM64/` holds a **parity** campaign, not a performance one.
Added as a dated section rather than by rewriting anything above, because the
convention this document states is unchanged and still correct.

Its six leaves — one per intra/parse residual language (`css`, `hcl`, `json`,
`markdown`, `toml`, `yaml`) — carry `environment.json`, a `notes.md`, and a
`raw/` directory of `parityreport` reports and diff logs from
`cmd/parity -family <lang>`. **No latency, percentile or RSS figure appears in
any of them**, which is `internal/parity/doc.go`'s standing rule: parity is a
reliability property and this harness publishes no performance number.

**`-aggregate` does not read these leaves, and cannot.** It reproduces
`internal/evalreport` metrics from raw samples, and a parity run publishes none
of the four series it knows. Measured: exit 2 on each leaf; exit 3 even on a
well-formed `evalreport` directory whose published report is a parity run. The
finding, both control arms and the two forbidden ways past it are filed as
**PARITY-012**; each leaf's `notes.md` repeats the measurement in place, so a
reader who opens one directly is not left to assume the tool was simply not run.

**A seventh directory, `2026-08-26-Darwin-ARM64/aggregate-control-arm/`, is not
a language leaf and not a measurement** — it is that exit-3 control arm, checked
in so the claim can be opened rather than taken on trust: the well-formed
directory it ran on, the `aggregate.json` `cmd/eval` itself wrote, and
`aggregate-exit3.log` with the exit code. It publishes no parity result and no
evidence-index row cites it; see its own `notes.md`. It was added in SW-200's
review round 1, because until then the exit-3 half of PARITY-012 was the one
claim in this campaign a reader could not reproduce from what was checked in.

The parity-side reproducers for these leaves are
`cmd/parity -verdict-diff`, `-counts-diff` and `-refusal-diff` over the checked-in
report pairs. All three were run for all twelve pairs; the exit codes and what
each one means are in every leaf's `notes.md` and in
[`../../rc/intra-parse-residual-parity-abstention.md`](../../rc/intra-parse-residual-parity-abstention.md).
