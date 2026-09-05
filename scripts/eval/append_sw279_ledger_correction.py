#!/usr/bin/env python3
"""Append a correction row to the SW-279 access ledger.

An append-only ledger cannot be edited, so a row that has been overtaken is corrected by
a later row rather than removed. This exists because the answerability finalizer ran
twice: the first run appended its row and then exited 3 because one candidate was
`unresolved`, which Section 4 says blocks completion. The independent reviewer's
resolution of that row was then applied and the finalizer re-run, replacing the artefact
the first row points at.

Leaving the first row in place with a digest that no longer resolves would be the exact
defect this harvest exists to avoid, so the correction says so in the ledger itself.
"""

from __future__ import annotations

import argparse
import json
import subprocess
import sys
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent))
import _access_ledger  # noqa: E402


ACTOR = "Claude Opus 5 (SW-279 Phase 2 orchestrator)"


def main() -> int:
    ap = argparse.ArgumentParser(description=__doc__)
    ap.add_argument("--ledger", required=True)
    ap.add_argument("--corrects", type=int, required=True, help="the sequence number being corrected")
    ap.add_argument("--artifact", required=True, help="the artifact whose current digest is authoritative")
    ap.add_argument("--detail", required=True)
    args = ap.parse_args()

    root = Path(subprocess.run(["git", "rev-parse", "--show-toplevel"], check=True,
                               stdout=subprocess.PIPE, text=True).stdout.strip())
    if Path.cwd().resolve() != root.resolve():
        print("run from the repository root", file=sys.stderr)
        return 2

    ledger = Path(args.ledger)
    artifact = Path(args.artifact)
    rows = [json.loads(line) for line in ledger.read_text(encoding="utf-8").splitlines() if line.strip()]
    corrected = next((r for r in rows if r.get("sequence") == args.corrects), None)
    if corrected is None:
        print(f"no row with sequence {args.corrects}", file=sys.stderr)
        return 2

    row = _access_ledger.append(
        ledger,
        actor=ACTOR,
        command_tool_class="ledger correction: an earlier row's output artifact was superseded",
        input_artifact=ledger.as_posix(),
        input_sha256=_access_ledger.sha256_file(ledger),
        output_artifact=artifact.as_posix(),
        output_sha256=_access_ledger.sha256_file(artifact),
        detail=args.detail,
        corrects_sequence=args.corrects,
        superseded_output_sha256=corrected.get("output_sha256"),
        authoritative_output_sha256=_access_ledger.sha256_file(artifact),
    )
    print(json.dumps(row, ensure_ascii=False, indent=2))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
