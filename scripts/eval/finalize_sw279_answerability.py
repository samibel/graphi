#!/usr/bin/env python3
"""Validate the SW-279 answerability annotations against the pin and report the yield.

Section 8 step 4 produces per-candidate answerability verdicts and graded spans; this
script is the check Section 4 describes a reviewer performing: "verifying the checkout SHA,
resolving every span and anchor, checking the span against the frozen rubric, and examining
every rejection for positive D-clause evidence."

It refuses rather than reports when the artefacts do not support a verdict:
  * a span whose file, line range or anchor does not resolve at the pin;
  * an `answerable` row without a reviewed grade-3 span in a tracked `.go` file;
  * a `not_answerable` row without a named D-clause;
  * a row with no independent reviewer, or reviewed by its own annotator;
  * a candidate with no verdict at all.

An `unresolved` row does not fail the script - Section 4 says it blocks completion of Phase
2 and is reported - but it is counted, listed, and marked as blocking in the outcome file.

The final counts are the point of the whole story, so they are computed here from the
artefacts and not carried over from any plan: existing answerable queries from cobra-v1
minus the withdrawn cb-05, plus the new answerable candidates by their sealed split.
"""

from __future__ import annotations

import argparse
import hashlib
import json
import subprocess
import sys
from collections import Counter
from datetime import datetime, timezone
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent))
import _access_ledger  # noqa: E402


HARVESTS = Path("docs/eval/retrieval/harvests")
DATASET_V1 = Path("internal/eval/retrieval/testdata/datasets/cobra-v1.json")
PIN = "a0a6ae020bb3899ff0276067863e50523f897370"
WITHDRAWN_FROM_V2 = {"cb-05"}
ACTOR = "Claude Opus 5 (SW-279 Phase 2 orchestrator)"
D_CLAUSES = {"D1", "D2", "D3", "D4", "D5"}
AC2_MINIMUM = 30


def read_jsonl(path: Path) -> list[dict[str, object]]:
    return [json.loads(line) for line in path.read_text(encoding="utf-8").splitlines() if line.strip()]


def main() -> int:
    ap = argparse.ArgumentParser(description=__doc__)
    ap.add_argument("--harvest", required=True)
    ap.add_argument("--cobra-root", required=True, help="path to the pinned cobra checkout")
    args = ap.parse_args()

    root = Path(subprocess.run(["git", "rev-parse", "--show-toplevel"], check=True,
                               stdout=subprocess.PIPE, text=True).stdout.strip())
    if Path.cwd().resolve() != root.resolve():
        print("run from the repository root", file=sys.stderr)
        return 2

    cobra = Path(args.cobra_root).expanduser().resolve()
    head = subprocess.run(["git", "-C", str(cobra), "rev-parse", "HEAD"], check=True,
                          stdout=subprocess.PIPE, text=True).stdout.strip()
    if head.lower() != PIN:
        print(f"cobra checkout is at {head}, not the pin {PIN}", file=sys.stderr)
        return 2
    tracked = set(subprocess.run(["git", "-C", str(cobra), "ls-tree", "-r", "--name-only", PIN],
                                 check=True, stdout=subprocess.PIPE, text=True).stdout.splitlines())

    harvest = HARVESTS / args.harvest
    sealed = {int(row["issue_number"]): row for row in read_jsonl(harvest / "sealed-questions.jsonl")}

    annotations: dict[int, dict[str, object]] = {}
    annotator_of: dict[int, str] = {}
    for path in sorted((harvest / "answerability").glob("annotations-*.jsonl")):
        for row in read_jsonl(path):
            number = int(row["issue_number"])
            if number in annotations:
                print(f"issue {number}: annotated twice ({path.name})", file=sys.stderr)
                return 2
            annotations[number] = row
            names = {str(j.get("annotator", "")) for j in row.get("judgements", [])}
            annotator_of[number] = sorted(names)[0] if names else path.stem

    reviews: dict[int, dict[str, object]] = {}
    for path in sorted((harvest / "answerability").glob("reviews-*.jsonl")):
        for row in read_jsonl(path):
            number = int(row["issue_number"])
            if number in reviews:
                print(f"issue {number}: reviewed twice ({path.name})", file=sys.stderr)
                return 2
            reviews[number] = row

    missing_annotation = sorted(set(sealed) - set(annotations))
    missing_review = sorted(set(sealed) - set(reviews))
    stray = sorted((set(annotations) | set(reviews)) - set(sealed))
    if missing_annotation or missing_review or stray:
        print(json.dumps({
            "sealed_candidates": len(sealed),
            "missing_annotation": missing_annotation,
            "missing_review": missing_review,
            "outside_the_sealed_set": stray,
        }, indent=2), file=sys.stderr)
        print("every sealed candidate needs one annotation and one independent review", file=sys.stderr)
        return 2

    ledger_rows: list[dict[str, object]] = []
    problems: list[str] = []
    for number in sorted(sealed):
        seal = sealed[number]
        annotation = annotations[number]
        review = reviews[number]
        verdict = str(annotation["verdict"])
        reviewer = str(review.get("reviewer", "")).strip()
        annotator = annotator_of[number]

        if verdict not in {"answerable", "not_answerable", "unresolved"}:
            problems.append(f"issue {number}: illegal verdict {verdict!r}")
            continue
        if not reviewer:
            problems.append(f"issue {number}: no independent reviewer recorded")
        if reviewer and annotator and reviewer == annotator:
            problems.append(f"issue {number}: reviewer and annotator are the same actor {reviewer!r}")

        checked: list[dict[str, object]] = []
        grade3_go = 0
        for judgement in annotation.get("judgements", []):
            path = str(judgement["path"])
            start = int(judgement["start_line"])
            end = int(judgement["end_line"])
            anchor = str(judgement["anchor"])
            grade = int(judgement["grade"])
            resolved = True
            detail = ""
            if path not in tracked:
                resolved, detail = False, "path is not tracked at the pin"
            else:
                lines = (cobra / path).read_text(encoding="utf-8", errors="replace").splitlines()
                if not (1 <= start <= end <= len(lines)):
                    resolved, detail = False, f"line range outside the file (1..{len(lines)})"
                elif anchor not in "\n".join(lines[start - 1:end]):
                    resolved, detail = False, "anchor does not occur inside the cited range"
            if not resolved:
                problems.append(f"issue {number}: span {path}:{start}-{end}: {detail}")
            if resolved and grade == 3 and path.endswith(".go"):
                grade3_go += 1
            checked.append({
                "path": path, "start_line": start, "end_line": end, "anchor": anchor,
                "grade": grade, "reason": judgement.get("reason"),
                "annotator": judgement.get("annotator"), "reviewer": reviewer,
                "anchor_resolves_at_pin": resolved,
                "resolution_note": detail or "resolved at the pin",
            })

        if verdict == "answerable" and grade3_go == 0:
            problems.append(f"issue {number}: answerable with no resolving grade-3 .go span (A2)")
        if verdict == "not_answerable":
            disqualifier = str(annotation.get("disqualifier") or "")
            if disqualifier not in D_CLAUSES:
                problems.append(f"issue {number}: not_answerable without a D1-D5 disqualifier")

        ledger_rows.append({
            "issue_number": number,
            "provenance": seal["provenance"],
            "stratum": seal["stratum"],
            "family_id": seal["family_id"],
            "provisional_split": seal["provisional_split"],
            "rubric_sha256": seal["rubric"]["rubric_sha256"],
            "verdict": verdict,
            "state": {
                "answerable": "accept",
                "not_answerable": "reject:not_answerable",
                "unresolved": "unresolved",
            }[verdict],
            "disqualifier": annotation.get("disqualifier"),
            "annotator": annotator,
            "reviewer": reviewer,
            "reviewer_agrees": review.get("agrees"),
            "reviewer_verdict": review.get("reviewer_verdict"),
            "annotator_note": annotation.get("note"),
            "reviewer_note": review.get("note"),
            "judgements": checked,
        })

    if problems:
        print("\n".join(problems), file=sys.stderr)
        print(f"\n{len(problems)} answerability violations; refusing to write the outcome", file=sys.stderr)
        return 2

    ledger_path = harvest / "answerability-ledger.jsonl"
    if ledger_path.exists():
        print(f"refusing to overwrite {ledger_path}", file=sys.stderr)
        return 2
    payload = b"".join(
        (json.dumps(row, ensure_ascii=False, separators=(",", ":")) + "\n").encode("utf-8")
        for row in ledger_rows
    )
    ledger_path.write_bytes(payload)

    v1 = json.loads(DATASET_V1.read_text(encoding="utf-8"))
    existing = Counter()
    for query in v1["queries"]:
        if query["id"] in WITHDRAWN_FROM_V2 or query["stratum"] == "no_hit":
            continue
        existing[query["split"]] += 1

    new_answerable = Counter()
    by_state = Counter(str(row["state"]) for row in ledger_rows)
    disqualifiers = Counter(str(row["disqualifier"]) for row in ledger_rows if row["disqualifier"])
    unresolved = [int(row["issue_number"]) for row in ledger_rows if row["state"] == "unresolved"]
    disagreements = [int(row["issue_number"]) for row in ledger_rows if row.get("reviewer_agrees") is False]
    for row in ledger_rows:
        if row["state"] == "accept":
            new_answerable[str(row["provisional_split"])] += 1

    dev_total = existing["dev"] + new_answerable["dev"]
    holdout_total = existing["holdout"] + new_answerable["holdout"]
    outcome = {
        "schema": "sw-279-phase-2-outcome/v1",
        "computed_at_utc": datetime.now(timezone.utc).isoformat(timespec="microseconds").replace("+00:00", "Z"),
        "harvest": args.harvest,
        "cobra_pin_verified": head,
        "sealed_candidates": len(sealed),
        "terminal_states": dict(sorted(by_state.items())),
        "not_answerable_by_disqualifier": dict(sorted(disqualifiers.items())),
        "unresolved_issue_numbers": unresolved,
        "unresolved_blocks_completion": bool(unresolved),
        "reviewer_disagreement_issue_numbers": disagreements,
        "existing_answerable_carried_into_v2": dict(sorted(existing.items())),
        "withdrawn_from_v2": sorted(WITHDRAWN_FROM_V2),
        "new_answerable_by_split": dict(sorted(new_answerable.items())),
        "final_answerable_dev": dev_total,
        "final_answerable_holdout": holdout_total,
        "ac2_minimum_per_split": AC2_MINIMUM,
        "ac2_dev_met": dev_total >= AC2_MINIMUM,
        "ac2_holdout_met": holdout_total >= AC2_MINIMUM,
        "ac2_dev_shortfall": max(0, AC2_MINIMUM - dev_total),
        "ac2_holdout_shortfall": max(0, AC2_MINIMUM - holdout_total),
        "answerability_ledger_sha256": hashlib.sha256(payload).hexdigest(),
    }
    outcome_path = harvest / "phase-2-outcome.json"
    outcome_path.write_text(json.dumps(outcome, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")

    _access_ledger.append(
        harvest / "access-ledger.jsonl",
        actor=ACTOR,
        command_tool_class="local validation of answerability spans against the pinned checkout, and yield arithmetic",
        input_artifact=(harvest / "sealed-questions.jsonl").as_posix(),
        input_sha256=_access_ledger.sha256_file(harvest / "sealed-questions.jsonl"),
        output_artifact=ledger_path.as_posix(),
        output_sha256=outcome["answerability_ledger_sha256"],
        detail=(
            f"Verified the cobra checkout HEAD is {head}, resolved every cited span and anchor "
            "against the tracked files at that commit, required a grade-3 .go span for every "
            "answerable row and a positive D-clause for every not_answerable row, and required an "
            "independent reviewer distinct from the annotator on every row. No retrieval access."
        ),
    )

    print(json.dumps(outcome, ensure_ascii=False, indent=2))
    if unresolved:
        print(f"\n{len(unresolved)} unresolved rows block completion of Phase 2 (Section 4)", file=sys.stderr)
        return 3
    if not (outcome["ac2_dev_met"] and outcome["ac2_holdout_met"]):
        print(
            "\nAC-10: the yield is short of 30 answerable per split under the frozen rule. "
            "Stop and publish the ledger and the exact shortfall. Do not loosen the rule.",
            file=sys.stderr,
        )
        return 4
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
