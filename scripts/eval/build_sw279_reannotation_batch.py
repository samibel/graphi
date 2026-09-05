#!/usr/bin/env python3
"""Build the re-annotation batch for a candidate that has no terminal state.

Round 1 of the SW-279 review found that `finalize_sw279_answerability.py` converted an
annotator's `unresolved` verdict into `not_answerable` when the independent reviewer
supplied a D-clause. Section 4 of the frozen rule forbids that outright - "An unresolved
candidate is not silently treated as a reject: it blocks completion of Phase 2 and is
reported" - and Section 8 lists "reinterpret an unresolved row as a reject" among the
forbidden acts. The conversion is gone.

Removing it leaves the affected candidate with no terminal state, and the rule's exhaustive
list of ways a candidate may finish - (a) reviewed grade-3 evidence satisfying A1-A4, (b) a
positive D1-D5 finding, (c) `unresolved` - offers exactly one legitimate route out: a fresh
annotation pass by an actor that has not touched the row, producing (a) or (b) first-person.
If that pass also lands on `unresolved`, Phase 2 is blocked and reported. Nothing here can
change that; this script only prepares the input.

The input carries exactly what a first-pass batch input carries - issue number, sealed `Q`,
sealed stratum, sealed rubric - taken from `sealed-questions.jsonl`, so the re-annotator sees
the sealed question and nothing the original annotator did not see. It carries no verdict, no
note, no disqualifier and no hint that the row was ever contested.

Outputs `<harvest>/answerability/batch-<n>-input.jsonl` and
`<harvest>/answerability/reannotation-plan.json`. It refuses to overwrite either. The seal-era
`batch-plan.json` is not touched: it records what was planned before the seal, and this pass
was not.
"""

from __future__ import annotations

import argparse
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
    ap.add_argument("--batch", type=int, required=True, help="the new batch number, e.g. 6")
    ap.add_argument("--issues", required=True, help="comma-separated issue numbers to re-annotate")
    ap.add_argument("--annotator-slot", required=True)
    ap.add_argument("--reviewer-slot", required=True)
    ap.add_argument("--reason", required=True)
    args = ap.parse_args()

    root = Path(subprocess.run(["git", "rev-parse", "--show-toplevel"], check=True,
                               stdout=subprocess.PIPE, text=True).stdout.strip())
    if Path.cwd().resolve() != root.resolve():
        print("run from the repository root", file=sys.stderr)
        return 2

    harvest = HARVESTS / args.harvest
    out_dir = harvest / "answerability"
    sealed_path = harvest / "sealed-questions.jsonl"
    sealed = {int(json.loads(line)["issue_number"]): json.loads(line)
              for line in sealed_path.read_text(encoding="utf-8").splitlines() if line.strip()}

    wanted = [int(part) for part in args.issues.split(",") if part.strip()]
    missing = [n for n in wanted if n not in sealed]
    if missing:
        print(f"not in the sealed set: {missing}", file=sys.stderr)
        return 2

    input_path = out_dir / f"batch-{args.batch}-input.jsonl"
    plan_path = out_dir / "reannotation-plan.json"
    for path in (input_path, plan_path):
        if path.exists():
            print(f"refusing to overwrite {path}", file=sys.stderr)
            return 2

    payload = b"".join(
        (json.dumps({
            "issue_number": number,
            "Q": sealed[number]["Q"],
            "stratum": sealed[number]["stratum"],
            "rubric": sealed[number]["rubric"],
        }, ensure_ascii=False, separators=(",", ":")) + "\n").encode("utf-8")
        for number in sorted(wanted)
    )
    input_path.write_bytes(payload)

    plan = {
        "schema": "sw-279-answerability-reannotation-plan/v1",
        "harvest": args.harvest,
        "reason": args.reason,
        "rule_basis": (
            "Section 4: every candidate must finish with (a) reviewed grade-3 evidence satisfying "
            "A1-A4, (b) a positive D1-D5 finding, or (c) unresolved, and an unresolved row blocks "
            "completion of Phase 2 rather than being reinterpreted as a reject (Section 8)."
        ),
        "batches": [{
            "batch": args.batch,
            "input": input_path.as_posix(),
            "issue_numbers": sorted(wanted),
            "supersedes_annotation_for": sorted(wanted),
            "annotator_slot": args.annotator_slot,
            "reviewer_slot": args.reviewer_slot,
            "annotations_output": (out_dir / f"annotations-{args.batch}.jsonl").as_posix(),
            "reviews_output": (out_dir / f"reviews-{args.batch}.jsonl").as_posix(),
            "input_sha256": _access_ledger.sha256_file(input_path),
            "retention": (
                "The original annotation and the original review are retained unedited in their "
                "own files and are carried onto the ledger row alongside the fresh pass."
            ),
        }],
    }
    plan_path.write_text(json.dumps(plan, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")

    _access_ledger.append(
        harvest / "access-ledger.jsonl",
        actor=ACTOR,
        command_tool_class="local extraction of sealed question/stratum/rubric rows into a re-annotation batch input",
        input_artifact=sealed_path.as_posix(),
        input_sha256=_access_ledger.sha256_file(sealed_path),
        output_artifact=input_path.as_posix(),
        output_sha256=plan["batches"][0]["input_sha256"],
        detail=(
            f"Re-annotation batch {args.batch} for {sorted(wanted)}. {args.reason} The input carries "
            "only the sealed Q, stratum and rubric, exactly as a first-pass batch input does, and no "
            "trace of the earlier verdict, note or disqualifier. No retrieval access, no source access."
        ),
    )

    print(json.dumps(plan, ensure_ascii=False, indent=2))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
