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

## Measure

First verify and start the already provisioned sidecar:

```bash
python3 scripts/eval/coderank_sidecar.py verify \
  --manifest /absolute/pins/coderank.json \
  --model-dir /absolute/models/CodeRankEmbed

python3 scripts/eval/coderank_sidecar.py serve \
  --manifest /absolute/pins/coderank.json \
  --model-dir /absolute/models/CodeRankEmbed \
  --bind 127.0.0.1 --port 8765
```

In another terminal, use a work directory that does not exist or is strictly
empty, and an output file that does not exist:

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
`captures.json` produced by qualification capture.

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
