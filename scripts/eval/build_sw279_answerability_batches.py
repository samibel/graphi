#!/usr/bin/env python3
"""Split the sealed candidates into annotator batches, and pair each with a reviewer.

Batching is by ascending issue number into contiguous blocks, so the assignment is a
function of the sealed order and not of anything about a question's content or expected
difficulty. Section 8 forbids preferring or deferring a question because its answer seems
easy or hard; a content-driven batching would be a soft form of exactly that.

Each batch input carries only what Section 4 and Section 6 say the annotator needs: the
question, the sealed stratum, and the sealed rubric with its digest. It carries no split,
no family, no provenance beyond the issue number, and nothing about any other candidate.

The reviewer for batch i is the annotator slot for a different batch, so no actor ever
reviews its own judgements - Section 4 requires an independent reviewer per judgement.
"""

from __future__ import annotations

import argparse
import json
import subprocess
import sys
from pathlib import Path


HARVESTS = Path("docs/eval/retrieval/harvests")


def main() -> int:
    ap = argparse.ArgumentParser(description=__doc__)
    ap.add_argument("--harvest", required=True)
    ap.add_argument("--batches", type=int, default=5)
    args = ap.parse_args()

    root = Path(subprocess.run(["git", "rev-parse", "--show-toplevel"], check=True,
                               stdout=subprocess.PIPE, text=True).stdout.strip())
    if Path.cwd().resolve() != root.resolve():
        print("run from the repository root", file=sys.stderr)
        return 2

    harvest = HARVESTS / args.harvest
    sealed = [json.loads(line) for line in (harvest / "sealed-questions.jsonl").read_text(encoding="utf-8").splitlines()]
    sealed.sort(key=lambda row: int(row["issue_number"]))

    out_dir = harvest / "answerability"
    out_dir.mkdir(exist_ok=True)

    n = args.batches
    size = (len(sealed) + n - 1) // n
    plan = []
    for i in range(n):
        chunk = sealed[i * size:(i + 1) * size]
        if not chunk:
            continue
        path = out_dir / f"batch-{i + 1}-input.jsonl"
        if path.exists():
            print(f"refusing to overwrite {path}", file=sys.stderr)
            return 2
        payload = b"".join(
            (json.dumps({
                "issue_number": int(row["issue_number"]),
                "Q": row["Q"],
                "stratum": row["stratum"],
                "rubric": row["rubric"],
            }, ensure_ascii=False, separators=(",", ":")) + "\n").encode("utf-8")
            for row in chunk
        )
        path.write_bytes(payload)
        plan.append({
            "batch": i + 1,
            "input": path.as_posix(),
            "count": len(chunk),
            "first_issue": int(chunk[0]["issue_number"]),
            "last_issue": int(chunk[-1]["issue_number"]),
            "annotator_slot": f"A{i + 1}",
            # the reviewer is its own actor in its own session, never an annotator
            "reviewer_slot": f"R{i + 1}",
            "annotations_output": (out_dir / f"annotations-{i + 1}.jsonl").as_posix(),
            "reviews_output": (out_dir / f"reviews-{i + 1}.jsonl").as_posix(),
        })

    (out_dir / "batch-plan.json").write_text(
        json.dumps({
            "schema": "sw-279-answerability-batch-plan/v1",
            "harvest": args.harvest,
            "sealed_count": len(sealed),
            "batching_rule": "ascending issue number, contiguous blocks; not content-driven",
            "reviewer_rule": "each batch is reviewed by a separate actor R<i> in its own session; no actor reviews its own judgements",
            "batches": plan,
        }, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")
    print(json.dumps(plan, ensure_ascii=False, indent=2))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
