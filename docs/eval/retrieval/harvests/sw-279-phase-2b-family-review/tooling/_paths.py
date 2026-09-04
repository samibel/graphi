"""Repo-relative path resolution for the SW-279 phase 2b family-review tooling.

The first cut of these scripts hard-coded absolute paths into a session scratchpad
that no longer exists, which made them unrunnable the moment the session ended and
leaked a local username into a public repository. Everything is resolved from this
file's own location instead, so the scripts run from any checkout.

The harvest directory is a parameter, not a constant: the phase 2a harvest that
produced the first candidate set was superseded (see ../../SUPERSEDED.md), and these
scripts must be able to run against whichever harvest is current.
"""

import argparse
import pathlib

# tooling/ -> sw-279-phase-2b-family-review/ -> harvests/ -> retrieval/ -> eval/ -> docs/ -> repo
REVIEW_DIR = pathlib.Path(__file__).resolve().parent.parent
HARVESTS_DIR = REVIEW_DIR.parent
REPO_ROOT = HARVESTS_DIR.parents[3]

BLIND_QUERIES = REVIEW_DIR / "blind-queries.txt"
REVIEWER_A = REVIEW_DIR / "family-reviewer-A-pi-minimax-m3.txt"
REVIEWER_B = REVIEW_DIR / "family-reviewer-B-codex.txt"
DATASET_V1 = REPO_ROOT / "internal/eval/retrieval/testdata/datasets/cobra-v1.json"

DEFAULT_HARVEST = "sw-279-phase-2a-superseded"


def harvest_arg(description):
    """Parse --harvest, returning the candidate-ledger path for that harvest."""
    ap = argparse.ArgumentParser(description=description)
    ap.add_argument(
        "--harvest",
        default=DEFAULT_HARVEST,
        help=(
            "harvest directory under docs/eval/retrieval/harvests/ "
            f"(default: {DEFAULT_HARVEST})"
        ),
    )
    args = ap.parse_args()
    ledger = HARVESTS_DIR / args.harvest / "candidate-ledger.jsonl"
    if not ledger.is_file():
        raise SystemExit(f"no candidate ledger at {ledger}")
    return ledger
