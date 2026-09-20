# Embedded-model development qualification operator guide

This workflow produces a development-promotion decision from sealed evidence.
It does not make a release decision. The command is evaluation-only and does
not change GrapHi's default embedder selection.

## Preconditions and trust boundary

The operator provisions the exact local CodeRank model tree and pinned Python
environment, verifies the manifest, and starts the sidecar in a separate
terminal. GrapHi never starts or supervises that process and never downloads a
model. Only the three protocol operations documented in
[`coderank-sidecar-protocol.md`](coderank-sidecar-protocol.md) are used.

The preregistration must exactly describe the machine being used. The measure
command observes OS version, CPU identity, physical-core count, the sidecar's
effective runtime thread count, and the sidecar's own peak RSS and artifact
bytes. Background load cannot be inferred reliably; the operator must provide
the named observation protocol and a literal metadata statement, such as an
inventory digest and observation timestamp. Those strings are sealed, and the
declaration must equal `reference_machine.background_load`.

The source repository argument is only a commit source. The command creates a
private detached worktree at the preregistered source SHA, hashes its canonical
tracked file bytes, performs the entire reindex there, and rechecks its digest,
HEAD, and cleanliness before evidence can be written. The candidate GrapHi
worktree is observed before and after the complete measurement against the
preregistered candidate commit and diff, excluding only the frozen qualification
run path. Any drift invalidates the run.

## Preregistration

Author `preregistration.json` from [`preregistration.md`](preregistration.md),
which carries the commented template, the per-field derivation of every digest,
and the paste-ready copy at
[`preregistration.example.json`](preregistration.example.json). Do not
hand-compute the digests:

```bash
CGO_ENABLED=0 go run ./cmd/embedded-model-qualification digests \
  --dataset /absolute/run/dataset.json \
  --candidate-root /absolute/graphi \
  --manifest /absolute/pins/coderank.json \
  --grading-rubric /absolute/graphi/docs/eval/retrieval/runs/embedded-model-qualification/grading-rubric.md \
  > /absolute/run/fragment.json
```

The preregistration is authored LAST. The CodeRank arm's four pins cannot be
derived until that manifest is final, and every repository change moves
`candidate_diff_sha256`, so a file frozen before the work is finished names a
candidate the run cannot bind to.

No trial index build is needed: the canonical fingerprint's eighth field,
`graph_generation`, is minted from `crypto/rand` by every index build and is
therefore bound at runtime rather than preregistered. The gate compares fields
0-6 against the pin and requires every arm and every observation of one run to
name one and the same generation (`graph_generation_consistency`). See
[`preregistration.md`](preregistration.md) for the full reasoning.

## Capture

Capture is the stage that produces the evidence `finalize` consumes: two
independent builds of each of the four arms (lexical, Potion/512,
Potion/8192, CodeRank), from which the blind evidence sets and blind decisions
are then derived.

First verify and start the already provisioned sidecar. The same running
sidecar serves capture and measure:

```bash
python3 scripts/eval/coderank_sidecar.py verify \
  --manifest /absolute/pins/coderank.json \
  --model-dir /absolute/models/CodeRankEmbed

python3 scripts/eval/coderank_sidecar.py serve \
  --manifest /absolute/pins/coderank.json \
  --model-dir /absolute/models/CodeRankEmbed \
  --bind 127.0.0.1 --port 8765
```

Every precondition below is refused rather than repaired:

- `--out` names a directory that **already exists and is strictly empty**. This
  is not the same rule as `measure --work-dir`, which may also be absent:
  capture reads the directory, so a path that does not exist is a refusal.
- `--static-model-dir` names the pinned Potion artifact directory and must
  contain the pinned `model.safetensors`. The Potion arms themselves resolve
  their artifact through `$GRAPHI_STATIC_MODEL_DIR`, falling back to
  `$XDG_CACHE_HOME/graphi/models/`, so export that variable to the same
  directory you pass here. Capture compares the two by device and inode, not by
  path, so a symlinked cache is fine but a genuine divergence is refused before
  any embedding, naming both directories and which of the two sources resolved
  the second one.
- `--manifest` names the finalized CodeRank manifest. Its bytes are hashed
  against the preregistered `manifest_sha256` before the run and re-read around
  the sidecar constructor, so a manifest edited mid-run invalidates the run.
- `--preregistration` names the sealed file, and `--dataset` the frozen dataset
  whose byte digest equals its `dataset_sha256`.
- `--candidate-root` names the GrapHi candidate worktree. It must be clean at
  the preregistered `candidate_sha` and `candidate_diff_sha256`, excluding only
  the frozen qualification run path, and it is observed again after the
  complete capture; any drift over the run invalidates it.
- `--source-repo` is only a commit source. Capture creates a private detached
  worktree at the preregistered `source_repo_sha` for every build and reads the
  corpus there, never from the operator's checkout.

```bash
CGO_ENABLED=0 GRAPHI_STATIC_MODEL_DIR=/absolute/models/potion-code-16M-v2@e9d2a44ca6a05ac6685f3b23709ea57eb7352d5b \
go run ./cmd/embedded-model-qualification capture \
  --preregistration /absolute/run/preregistration.json \
  --dataset /absolute/run/dataset.json \
  --manifest /absolute/pins/coderank.json \
  --source-repo /absolute/source/repository \
  --candidate-root /absolute/graphi \
  --static-model-dir /absolute/models/potion-code-16M-v2@e9d2a44ca6a05ac6685f3b23709ea57eb7352d5b \
  --out /absolute/run/captures
```

Capture takes every input from the command line. There is no environment
variable that names a capture input, and running the package tests is not a
way to produce evidence: the live test in `internal/eval/retrieval` is gated on
`GRAPHI_*` variables only so that CI skips it, and it goes through the same
`CaptureQualification` entry point this subcommand calls.

The run is all-or-nothing. It stages `build-1/` and `build-2/` per arm inside
the output directory, compares the two builds of each arm digest for digest,
and publishes everything with a single rename; a failed build leaves the
output directory empty. On success it prints the capture root path:

```
CAPTURES: /absolute/run/captures/captures.json
```

That file is exactly what `finalize --captures` takes. Exit 1 means the run was
refused or failed, exit 2 means the invocation was wrong; there is no partial
success.

## Measure

With the verified sidecar from the capture step still running, use a work
directory that does not exist or is strictly empty, and an output file that
does not exist:

```bash
CGO_ENABLED=0 go run ./cmd/embedded-model-qualification measure \
  --preregistration /absolute/run/preregistration.json \
  --dataset /absolute/run/dataset.json \
  --manifest /absolute/pins/coderank.json \
  --source-repo /absolute/source/repository \
  --candidate-root /absolute/graphi \
  --work-dir /absolute/run/operating-work \
  --out /absolute/run/operating.json \
  --background-protocol operator-process-inventory-v1 \
  --background-metadata 'captured=2026-09-16T10:00:00Z; inventory_sha256=<sha256>'
```

`--background-metadata` is sealed as literal operator-supplied metadata. Include
the content digest of any separately retained process-inventory artifact rather
than only its mutable path;
the command does not claim it measured system-wide load. The measurement runs
one fresh 768-document M3 reindex, closes and reopens its stores to prove a
durable ready generation, performs 64 successful unmeasured warmups, then two
ordered passes of all 64 queries. Each measured sample covers the complete
attested query helper and records latency, vector digest, and unknown-token
count. A sidecar epoch or identity change fails closed.

## Finalize

The blind-evidence file is one JSON array of exactly seven sealed
`BlindEvidenceSet` records. The decision file is one JSON array of exactly 448
sealed `BlindDecision` records. The capture root is the typed eight-capture
`captures.json` the [capture](#capture) subcommand published.

```bash
CGO_ENABLED=0 go run ./cmd/embedded-model-qualification finalize \
  --preregistration /absolute/run/preregistration.json \
  --dataset /absolute/run/dataset.json \
  --captures /absolute/run/captures.json \
  --blind-evidence /absolute/run/blind-evidence.json \
  --blind-decisions /absolute/run/blind-decisions.json \
  --operating /absolute/run/operating.json \
  --out /absolute/run/final-report
```

The finalizer loads all evidence strictly, takes observations only from build 1
of each arm, includes all eight build digests, and accepts oracle evidence only
from M3/build 1. It validates and evaluates everything before creating the new
report directory. A valid negative decision exits successfully and prints
`DEVELOPMENT PROMOTION: NO`; a valid positive decision prints
`DEVELOPMENT PROMOTION: YES`.
