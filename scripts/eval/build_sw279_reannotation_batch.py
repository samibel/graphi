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
note and no disqualifier. It is not a claim that the re-annotator cannot tell it is
re-annotating: the actor is told which pass it is performing, and a single-row batch in a plan
whose other batches hold eighteen or nineteen rows is itself legible. What the input withholds
is the earlier verdict, its direction and its clause.

**The route this script opens is bounded, because an unbounded one is "re-roll the rows whose
answer you dislike".** `projects/graphi/stories/SW-279/decision-unresolved-reannotation.md`
permitted the fresh-pass route and then found the implementation had no bound at all: this
script validated only that the requested numbers were in the sealed set, never reading the
existing annotations, so a batch could be built for an `accept` or a `reject` row. Two
refusals close that, and neither leaves the operator any discretion:

  * **only `unresolved` is eligible** - the current verdict of every requested issue is read
    out of the committed `annotations-*.jsonl`, and anything else is refused. An accepted or
    rejected row is permanently non-re-rollable; an unresolved row is by construction one with
    no outcome to dislike;
  * **the whole eligible set, or none** - the unresolved set is computed here, and `--issues`
    must be exactly it. The operator may not take a subset, so seeing which rows are
    unresolved confers no choice.

The third bound - exactly one re-roll per row, ever - lives in the finalizer, which is where a
second supersession would have to be honoured.

A fourth bound closes the channel entirely once the dataset has been used: if any file under
`docs/eval/retrieval/runs/` names `cobra-v2`, Section 8 step 6 has happened and no
re-annotation is built at all. Re-labelling a row after a retrieval number exists for it is
choosing the label that moves the number, and no per-row bound reaches that. The Phase 2
report claimed this control existed before any code implemented it; it exists now.

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

# Section 8's step 6 - run retrieval against the frozen dataset - and where its output lands.
# `DATASET_ID` is the id `build_cobra_v2_dataset.py` writes into the dataset this harvest's
# answerability ledger produces; a run that has consumed it names it in its own files.
RUNS = Path("docs/eval/retrieval/runs")
DATASET_ID = "cobra-v2"


def runs_that_consumed_the_dataset() -> list[str]:
    """Every file under `runs/` that names the dataset this harvest builds.

    Read as bytes and searched literally: the point is to find the id wherever a run records
    it - `dataset.json`'s `id`, a `run.json` reference, a README - without depending on any
    one run format staying the shape it is today.
    """
    if not RUNS.is_dir():
        return []
    needle = DATASET_ID.encode("utf-8")
    return sorted(path.as_posix() for path in RUNS.rglob("*")
                  if path.is_file() and needle in path.read_bytes())


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

    # Once retrieval has been run against the dataset, re-annotating it is choosing the
    # answer after seeing the score. The Phase 2 report asserted this was prevented while
    # nothing checked it, which is a worse defect than the gap itself: an unbacked claim in a
    # document whose whole purpose is to be checkable. It is checked here now.
    consumed = runs_that_consumed_the_dataset()
    if consumed:
        for path in consumed[:10]:
            print(f"{path} references {DATASET_ID}", file=sys.stderr)
        if len(consumed) > 10:
            print(f"... and {len(consumed) - 10} more", file=sys.stderr)
        print(
            f"{len(consumed)} file(s) under {RUNS.as_posix()} reference {DATASET_ID}, so "
            "Section 8 step 6 has run for this dataset. Re-annotating an answerability row "
            "after a retrieval number exists for it is choosing the label that moves the "
            "number, which no bound on the re-roll channel can undo. Refusing to build a "
            "re-annotation batch.",
            file=sys.stderr,
        )
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

    # Eligibility, read out of the committed annotations rather than taken from the operator.
    # A row that already has an outcome may never be re-rolled, whatever that outcome is.
    verdicts: dict[int, tuple[str, str]] = {}
    duplicates: list[int] = []
    for annotations_path in sorted(out_dir.glob("annotations-*.jsonl")):
        for line in annotations_path.read_text(encoding="utf-8").splitlines():
            if not line.strip():
                continue
            row = json.loads(line)
            number = int(row["issue_number"])
            if number in verdicts:
                duplicates.append(number)
            verdicts[number] = (str(row["verdict"]), annotations_path.name)
    if not verdicts:
        print(f"no annotations under {out_dir}; there is nothing to re-annotate", file=sys.stderr)
        return 2
    if duplicates:
        # Already re-annotated once. Exactly one re-roll per row, ever.
        print(
            f"issue(s) {sorted(set(duplicates))} already carry a second annotation; a row may be "
            "re-annotated exactly once. If the second pass also returned `unresolved`, Phase 2 is "
            "blocked and reported - there is no pass three.",
            file=sys.stderr,
        )
        return 2

    ineligible = {n: verdicts[n] for n in wanted if verdicts.get(n, ("absent", ""))[0] != "unresolved"}
    if ineligible:
        for number, (verdict, where) in sorted(ineligible.items()):
            print(f"issue {number}: current verdict is {verdict!r} ({where})", file=sys.stderr)
        print(
            "only an `unresolved` row may be re-annotated. Re-rolling a row that already has an "
            "outcome is 're-roll the rows whose answer you dislike', which empties Section 4's "
            "blocking clause; see projects/graphi/stories/SW-279/"
            "decision-unresolved-reannotation.md.",
            file=sys.stderr,
        )
        return 2

    eligible = sorted(n for n, (verdict, _) in verdicts.items() if verdict == "unresolved")
    if sorted(wanted) != eligible:
        print(
            f"--issues is {sorted(wanted)}, but the unresolved set is {eligible}. The whole "
            "eligible set is re-annotated, or none of it: letting the operator pick a subset "
            "restores exactly the discretion the eligibility rule removes.",
            file=sys.stderr,
        )
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
