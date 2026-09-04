#!/usr/bin/env python3
"""Project the accepted candidates' Q and opening body for the Section 5 stratum assigner.

Section 5 assigns a stratum "to `Q` and the issue author's opening body, before source
access". The assigner therefore needs exactly those two fields and nothing else - no
verdict, no clause, no family, no split, and above all no source or retrieval material.
This script writes that projection so the assigner reads a file rather than being handed a
larger artefact and trusted to ignore most of it.

No query text is printed to the console.
"""

from __future__ import annotations

import argparse
import hashlib
import json
import subprocess
import sys
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent))
import _access_ledger  # noqa: E402


HARVESTS = Path("docs/eval/retrieval/harvests")
ACTOR = "Claude Opus 5 (SW-279 Phase 2 orchestrator)"


def main() -> int:
    ap = argparse.ArgumentParser(description=__doc__)
    ap.add_argument("--harvest", required=True)
    args = ap.parse_args()

    root = Path(subprocess.run(["git", "rev-parse", "--show-toplevel"], check=True,
                               stdout=subprocess.PIPE, text=True).stdout.strip())
    if Path.cwd().resolve() != root.resolve():
        print("run from the repository root", file=sys.stderr)
        return 2

    harvest = HARVESTS / args.harvest
    questions_path = harvest / "candidate-questions.jsonl"
    archive_path = harvest / "issue-text.jsonl"
    out_path = harvest / "stratum-input.jsonl"
    if out_path.exists():
        print(f"refusing to overwrite {out_path}", file=sys.stderr)
        return 2

    bodies = {}
    for line in archive_path.read_text(encoding="utf-8").splitlines():
        row = json.loads(line)
        bodies[int(row["issue_number"])] = row["body"]

    rows = []
    for line in questions_path.read_text(encoding="utf-8").splitlines():
        question = json.loads(line)
        number = int(question["issue_number"])
        rows.append({
            "issue_number": number,
            "Q": question["Q"],
            "body": bodies[number],
        })
    rows.sort(key=lambda row: int(row["issue_number"]))

    payload = b"".join(
        (json.dumps(row, ensure_ascii=False, separators=(",", ":")) + "\n").encode("utf-8")
        for row in rows
    )
    out_path.write_bytes(payload)

    _access_ledger.append(
        harvest / "access-ledger.jsonl",
        actor=ACTOR,
        command_tool_class="local projection of Q and opening body for the Section 5 stratum assigner",
        input_artifact=questions_path.as_posix(),
        input_sha256=_access_ledger.sha256_file(questions_path),
        output_artifact=out_path.as_posix(),
        output_sha256=hashlib.sha256(payload).hexdigest(),
        detail=(
            "Projected only issue_number, Q and the opening body. No verdict, clause, family, "
            "split, stratum, source or retrieval field is present in the assigner's input."
        ),
    )

    print(json.dumps({
        "rows": len(rows),
        "output": out_path.as_posix(),
        "output_sha256": hashlib.sha256(payload).hexdigest(),
    }, indent=2))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
