#!/usr/bin/env bash
# derive-dev.sh — mechanically derive the SW-270 dev-only measurement population.
#
# What this does, literally: it reads the frozen COMBINED dataset
# internal/eval/retrieval/testdata/datasets/cobra-v1.json (which contains both
# the dev and the holdout split) and emits a new dataset containing every and
# only query whose split is "dev". It then asserts that no emitted row is
# anything but split == "dev", prints ONLY counts and sha256 hashes, and
# verifies the output byte-for-byte against the pinned artifact the two SW-270
# runs were judged on.
#
# What this does NOT do: it does not make "the holdout split was not read"
# literally true — any program that filters the combined file necessarily
# reads bytes that contain holdout rows. It guarantees that no holdout row is
# emitted, printed, logged, passed to the harness or scored. Whether that
# operational reading satisfies AC-5 is an owner decision recorded (or owed) in
# projects/graphi/stories/SW-270/ — see README.md, "Population".
#
# Usage (from the graphi repo root):
#   docs/eval/retrieval/runs/2026-09-04-sw270-bare-filename-path-rule/derive-dev.sh <out.json>
set -euo pipefail

SRC="${SW270_SRC:-internal/eval/retrieval/testdata/datasets/cobra-v1.json}"
OUT="${1:?usage: derive-dev.sh <out.json>}"
EXPECTED_SRC_SHA=be604ff7b17db5c35b0c63ddbb5d758633535e81e6771858ff860c724fb50d82
EXPECTED_OUT_SHA=1436a29b6f1b2432f7c79266fbfcb80000105eaeefcc827489657dc553cec9d5

if ! command -v jq >/dev/null; then
  echo "derive-dev.sh: jq is required" >&2
  exit 2
fi
if [ ! -f "$SRC" ]; then
  echo "derive-dev.sh: source dataset not found: $SRC" >&2
  exit 2
fi

src_sha="$(shasum -a 256 "$SRC" | awk '{print $1}')"
echo "source sha256: $src_sha"
if [ "$src_sha" != "$EXPECTED_SRC_SHA" ]; then
  echo "derive-dev.sh: source sha256 mismatch (expected $EXPECTED_SRC_SHA); refusing to derive from an unpinned file" >&2
  exit 1
fi

# Counts only. The non-dev count is reported as a number so the reader can
# check total == dev + other; no holdout id, text or judgement is printed.
total="$(jq '.queries | length' "$SRC")"
dev="$(jq '[.queries[] | select(.split == "dev")] | length' "$SRC")"
echo "source queries: total=$total dev=$dev other=$((total - dev))"

# The notes string is part of the pinned artifact and is reproduced verbatim
# so the output hash matches; its wording predates review round 1 and is
# superseded by the README's provenance statement.
jq --arg sha "$src_sha" \
  '.id = "cobra-v1-dev" | .notes = ("SW-270 dev-only measurement population: every and only split=dev query from source dataset cobra-v1 at sha256 " + $sha + "; the holdout split is excluded so it is never read (AC-5). Judgements, grades and relevant_min_grade are inherited unchanged from the source.") | .queries |= map(select(.split == "dev"))' \
  "$SRC" > "$OUT"

# Assert every emitted row is split == "dev" (the count of violations must be 0).
non_dev="$(jq '[.queries[] | select(.split != "dev")] | length' "$OUT")"
out_n="$(jq '.queries | length' "$OUT")"
echo "output queries: $out_n (non-dev rows: $non_dev)"
if [ "$non_dev" != "0" ]; then
  echo "derive-dev.sh: $non_dev emitted row(s) are not split=dev; removing output" >&2
  rm -f "$OUT"
  exit 1
fi
if [ "$out_n" != "$dev" ]; then
  echo "derive-dev.sh: emitted $out_n rows but the source has $dev dev rows" >&2
  rm -f "$OUT"
  exit 1
fi

out_sha="$(shasum -a 256 "$OUT" | awk '{print $1}')"
echo "output sha256: $out_sha"
if [ "$out_sha" != "$EXPECTED_OUT_SHA" ]; then
  echo "derive-dev.sh: output sha256 mismatch (expected $EXPECTED_OUT_SHA); the derived population is not the one SW-270 measured" >&2
  exit 1
fi
echo "OK: $OUT is the pinned SW-270 dev-only artifact"
