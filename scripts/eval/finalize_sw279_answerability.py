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
  * a row whose reviewer is not the reviewer the batch plan paired with its annotator;
  * a judgement whose recorded annotator is not the actor the plan assigned to that file;
  * a candidate with no verdict at all;
  * an actor whose attestation does not describe the work the plan gave it - a different set of
    issue numbers, or an input or output digest that is not the committed bytes;
  * a re-annotation performed by an actor that already ruled on the row it is superseding;
  * a supersession of a row that was not `unresolved`, or a second supersession of any row.

**An `unresolved` row is never converted into anything.** Section 4: "An unresolved candidate
is not silently treated as a reject: it blocks completion of Phase 2 and is reported."
Section 8 lists "reinterpret an unresolved row as a reject" among the forbidden acts. An
earlier version of this script promoted `unresolved` to `not_answerable` whenever the
independent reviewer supplied a D-clause, on the argument that the conversion was
exclusionary and therefore safe. The rule does not distinguish directions, and the argument
is gone with the code. An unresolved row now blocks: the run writes its ledger and outcome
under `-blocked` names so the authoritative artefact names stay unclaimed, prints the
blocking issue numbers, and exits non-zero.

The only legitimate way out of `unresolved` is a fresh terminal determination by an actor
that has not seen the row, declared in `answerability/reannotation-plan.json` and delivered
as its own annotation and review files. Both passes are kept: the superseded verdict, note,
reviewer and reviewer note travel on the ledger row beside the fresh ones.

Annotator identity comes from the batch plan and the actor's own attestation, never from the
judgements: a `not_answerable` row carries no judgements at all, so inferring identity from
them made the "no actor reviews its own judgements" guard structurally unable to fire on
exactly the rows where a rejection decision was being made alone.

The final counts are the point of the whole story, so they are computed here from the
artefacts and not carried over from any plan: existing answerable queries from cobra-v1
minus the withdrawn cb-05, plus the new answerable candidates by their sealed split.
"""

from __future__ import annotations

import argparse
import hashlib
import json
import re
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


# A `path.ext:line` or `path.ext:start-end` reference inside a prose note.
#
# Round 1 made this an extension allowlist - go, md, mod, txt, yaml, yml, sh - on the argument
# that a narrow pattern cannot mistake a version string for a citation. The cost was silence
# in the wrong direction: `definitely-missing.json:999` matched nothing, so it was neither
# resolved nor reported, and a rejection note could point a reader at a location that does not
# exist while the run returned 0. An unresolvable citation must be a refusal, and a citation
# the parser cannot classify is exactly the case where that matters most.
#
# So the extension is no longer enumerated: any dotted, path-shaped token followed by `:line`
# is treated as a citation and must resolve at the pin. The extension must still begin with a
# letter, which is what keeps version strings (`v1.2:3`, `1.16:4`) and section references
# (`4.1:2`) out - they are not citation-shaped, rather than being citation-shaped and ignored.
CITATION = re.compile(
    r"\b([A-Za-z0-9_][A-Za-z0-9_./-]*\.[A-Za-z][A-Za-z0-9_+-]{0,9}):(\d+)(?:\s*-\s*(\d+))?"
)


def check_note_citations(
    note: str, tracked_by_basename: dict[str, list[str]], tracked: set[str], cobra: Path
) -> tuple[list[dict[str, object]], list[str]]:
    """Resolve every `file:line` citation in a prose note against the pinned tree.

    Rejection evidence lives in prose, not in spans, so until SW-279 review round 1 nothing
    checked it at all: a reject needed only to name a D-clause. This does not verify that the
    cited bytes SAY what the note claims - no mechanical check can - but it does verify that
    every location the note points a reader at exists at the pin and contains the cited lines.
    A citation to a file that is not in the pinned tree, or to a line past its end, is now a
    refusal rather than something a reader has to discover.
    """
    checked: list[dict[str, object]] = []
    failures: list[str] = []
    for match in CITATION.finditer(note or ""):
        cited, start = match.group(1), int(match.group(2))
        end = int(match.group(3) or match.group(2))
        # Notes often use a bare basename for a file that lives in a subdirectory. Accept it
        # only when exactly one tracked file has that basename, so the resolution cannot be
        # a guess between candidates.
        resolved = cited if cited in tracked else None
        if resolved is None:
            candidates = tracked_by_basename.get(cited.rsplit("/", 1)[-1], [])
            resolved = candidates[0] if len(candidates) == 1 else None
        if resolved is None:
            failures.append(f"{match.group(0)}: no such tracked file at the pin")
            continue
        lines = len((cobra / resolved).read_text(encoding="utf-8", errors="replace").splitlines())
        if not (1 <= start <= end <= lines):
            failures.append(f"{match.group(0)}: {resolved} has {lines} lines at the pin")
            continue
        checked.append({"cited_as": match.group(0), "resolved_path": resolved,
                        "start_line": start, "end_line": end})
    return checked, failures


def read_jsonl(path: Path) -> list[dict[str, object]]:
    return [json.loads(line) for line in path.read_text(encoding="utf-8").splitlines() if line.strip()]


def sha256_file(path: Path) -> str:
    return hashlib.sha256(path.read_bytes()).hexdigest()


class Refuse(Exception):
    """A structural defect in the plan/attestation wiring. The run stops; nothing is written."""


def attested_input_digest(attestation: dict[str, object]) -> tuple[str | None, bool]:
    """Return (digest, is_first_person) for whichever input digest an attestation carries.

    The five first-pass annotator attestations predate the schema field, so their input
    digest is the orchestrator-recorded one back-filled under a name that says who recorded
    it. Either is a digest that must resolve; only one of them is a first-person claim, and
    this keeps the difference visible instead of flattening it.
    """
    first_person = attestation.get("input_sha256")
    if first_person is not None:
        return str(first_person), True
    recorded = attestation.get("input_artifact_orchestrator_recorded")
    if isinstance(recorded, dict) and recorded.get("sha256") is not None:
        return str(recorded["sha256"]), False
    return None, False


def load_passes(harvest: Path) -> tuple[dict[str, dict[str, object]], dict[int, str]]:
    """Bind each annotation/review file to the actors the plan assigned to it, and check that
    the binding is one the artefacts actually support.

    Returns (passes, superseded_by):
      * `passes` is keyed by the annotations file name and carries the annotator's and
        reviewer's attested actor names plus the batch number;
      * `superseded_by` maps an issue number to the annotations file that is declared to
        supersede its first-pass annotation.

    Identity is read from the plan and from each actor's own attestation, so a row's
    annotator is recorded whether or not that row happens to carry judgements.

    Round 1 stopped there, and that was not enough. The slot name in the plan was the only
    thing tying an attestation to a batch: nothing checked that the attesting actor had been
    assigned those issue numbers, that the bytes it attested to are the committed bytes, or -
    for a re-annotation - that the superseding actor had not already ruled on the very row it
    was brought in to settle. Renaming the re-annotation plan's `annotator_slot` from A6 to A4
    therefore returned 0 and attributed #1780 to the actor whose `unresolved` verdict the
    fresh pass exists to replace, which is the one thing §4's "an actor that has not seen the
    row" forbids. Four things are checked here now, before any verdict is read:

      * the actor's own `assigned_issue_numbers` / `reviewed_issue_numbers` must be exactly
        the issue numbers in the batch input the plan gave it;
      * the input digest it attested (or, for the five first-pass annotators, the labelled
        orchestrator-recorded one) must be the digest of that committed input file;
      * the output digest it attested must be the digest of the committed output file, and a
        reviewer's `annotator_file_sha256` must be the digest of the annotations it reviewed;
      * a superseding actor must not appear as the annotator or the reviewer of any other
        batch covering a row it is superseding, and the superseding annotator and reviewer
        must not be the same actor.
    """
    answerability = harvest / "answerability"
    plans: list[tuple[dict[str, object], bool]] = []
    plan_path = answerability / "batch-plan.json"
    plans.extend((batch, False) for batch in json.loads(plan_path.read_text(encoding="utf-8"))["batches"])
    reannotation_path = answerability / "reannotation-plan.json"
    if reannotation_path.exists():
        plans.extend((batch, True)
                     for batch in json.loads(reannotation_path.read_text(encoding="utf-8"))["batches"])

    passes: dict[str, dict[str, object]] = {}
    superseded_by: dict[int, str] = {}
    # batch number -> (issue numbers it covers, annotator actor, reviewer actor)
    coverage: dict[int, tuple[set[int], str, str]] = {}
    supersessions: list[tuple[int, set[int], str, str]] = []

    for batch, is_reannotation in plans:
        number = int(batch["batch"])
        annotations_path = Path(str(batch["annotations_output"]))
        reviews_path = Path(str(batch["reviews_output"]))
        input_path = Path(str(batch["input"]))
        annotator_slot = str(batch["annotator_slot"])
        reviewer_slot = str(batch["reviewer_slot"])
        annotator_attestation = answerability / f"annotator-{annotator_slot}-attestation.json"
        reviewer_attestation = answerability / f"reviewer-{reviewer_slot}-attestation.json"
        for path in (annotator_attestation, reviewer_attestation):
            if not path.exists():
                raise Refuse(f"missing attestation for a planned actor: {path}")
        for path in (input_path, annotations_path, reviews_path):
            if not path.exists():
                raise Refuse(f"batch {number}: the plan names {path}, which does not exist")

        annotator = json.loads(annotator_attestation.read_text(encoding="utf-8"))
        reviewer = json.loads(reviewer_attestation.read_text(encoding="utf-8"))
        annotator_actor = str(annotator["actor"])
        reviewer_actor = str(reviewer["actor"])

        assigned = {int(row["issue_number"]) for row in read_jsonl(input_path)}
        input_digest = sha256_file(input_path)
        annotations_digest = sha256_file(annotations_path)
        reviews_digest = sha256_file(reviews_path)

        # The plan's own declared input digest, where it carries one, is held to the same bar.
        planned_input_digest = batch.get("input_sha256")
        if planned_input_digest is not None and str(planned_input_digest) != input_digest:
            raise Refuse(
                f"batch {number}: the plan declares input_sha256 {planned_input_digest} for "
                f"{input_path}, whose committed bytes hash to {input_digest}"
            )

        for role, slot, attestation, key, expected in (
            ("annotator", annotator_slot, annotator, "assigned_issue_numbers", assigned),
            ("reviewer", reviewer_slot, reviewer, "reviewed_issue_numbers", assigned),
        ):
            claimed = attestation.get(key)
            if claimed is None:
                raise Refuse(
                    f"batch {number}: {role} {slot}'s attestation carries no {key}, so nothing "
                    "ties this actor to the rows the plan gave it"
                )
            if {int(n) for n in claimed} != expected:
                raise Refuse(
                    f"batch {number}: {role} {slot} attests to {sorted(int(n) for n in claimed)}, "
                    f"but the plan's input {input_path} contains {sorted(expected)}. A slot name "
                    "is not an assignment; the actor's own statement of what it was given has to "
                    "be the rows it was actually given."
                )

        annotator_input, first_person = attested_input_digest(annotator)
        if annotator_input is None:
            raise Refuse(
                f"batch {number}: annotator {annotator_slot}'s attestation records no input "
                "digest, first-person or orchestrator-recorded, so the bytes it read are not "
                "evidenced anywhere"
            )
        checks = [
            (f"annotator {annotator_slot} input"
             + ("" if first_person else " (orchestrator-recorded)"), annotator_input, input_digest),
            (f"annotator {annotator_slot} output", str(annotator["output_sha256"]), annotations_digest),
            (f"reviewer {reviewer_slot} output", str(reviewer["output_sha256"]), reviews_digest),
            (f"reviewer {reviewer_slot} input (the annotations it reviewed)",
             str(reviewer["annotator_file_sha256"]), annotations_digest),
        ]
        reviewer_input = reviewer.get("input_sha256")
        if reviewer_input is not None:
            checks.append((f"reviewer {reviewer_slot} batch input", str(reviewer_input), input_digest))
        for label, attested, actual in checks:
            if attested != actual:
                raise Refuse(
                    f"batch {number}: {label} is attested as {attested}, but the committed bytes "
                    f"hash to {actual}"
                )

        coverage[number] = (assigned, annotator_actor, reviewer_actor)
        superseded = {int(n) for n in batch.get("supersedes_annotation_for", [])}
        if superseded:
            if not is_reannotation:
                raise Refuse(
                    f"batch {number} supersedes {sorted(superseded)} but is not a re-annotation "
                    "batch; only the re-annotation plan may declare a supersession"
                )
            if not superseded <= assigned:
                raise Refuse(
                    f"batch {number} supersedes {sorted(superseded - assigned)}, which is not in "
                    "its own input"
                )
            supersessions.append((number, superseded, annotator_actor, reviewer_actor))

        passes[annotations_path.name] = {
            "batch": number,
            "annotator_slot": annotator_slot,
            "reviewer_slot": reviewer_slot,
            "annotator": annotator_actor,
            "reviewer": reviewer_actor,
            "reviews_file": reviews_path.name,
            "is_reannotation": is_reannotation,
        }
        for issue in sorted(superseded):
            if issue in superseded_by:
                raise Refuse(
                    f"issue {issue} is declared superseded by two batches ({superseded_by[issue]} "
                    f"and {annotations_path.name}). Exactly one re-roll per row, ever: if the "
                    "second pass also returned `unresolved`, Phase 2 is "
                    "blocked and reported, and there is no pass three. An unbounded re-roll "
                    "channel empties Section 4's blocking clause - see projects/graphi/stories/"
                    "SW-279/decision-unresolved-reannotation.md."
                )
            superseded_by[issue] = annotations_path.name

    # Freshness. §4 allows only "a fresh terminal determination by an actor that has not seen
    # the row"; an actor that already annotated or reviewed that row has seen it, so it cannot
    # be the actor that settles it, whatever slot name the plan gives it.
    for number, superseded, annotator_actor, reviewer_actor in supersessions:
        if annotator_actor == reviewer_actor:
            raise Refuse(
                f"batch {number}: the superseding annotator and reviewer are the same actor "
                f"{annotator_actor!r}"
            )
        for issue in sorted(superseded):
            for other, (issues, prior_annotator, prior_reviewer) in sorted(coverage.items()):
                if other == number or issue not in issues:
                    continue
                for role, actor in (("annotator", annotator_actor), ("reviewer", reviewer_actor)):
                    if actor in (prior_annotator, prior_reviewer):
                        raise Refuse(
                            f"batch {number} supersedes issue {issue}, but its {role} "
                            f"{actor!r} already ruled on that row in batch {other}. Section 4 "
                            "allows only a fresh terminal determination by an actor that has "
                            "not seen the row: an actor cannot supersede its own judgement, "
                            "and re-pointing a slot name at an actor that has does not make "
                            "the pass fresh."
                        )

    return passes, superseded_by


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
    tracked_by_basename: dict[str, list[str]] = {}
    for path in sorted(tracked):
        tracked_by_basename.setdefault(path.rsplit("/", 1)[-1], []).append(path)

    harvest = HARVESTS / args.harvest
    sealed = {int(row["issue_number"]): row for row in read_jsonl(harvest / "sealed-questions.jsonl")}

    try:
        passes, superseded_by = load_passes(harvest)
    except Refuse as refusal:
        print(str(refusal), file=sys.stderr)
        return 2

    annotations: dict[int, dict[str, object]] = {}
    annotator_of: dict[int, str] = {}
    planned_reviewer_of: dict[int, str] = {}
    pass_of: dict[int, dict[str, object]] = {}
    superseded_annotation: dict[int, dict[str, object]] = {}
    for path in sorted((harvest / "answerability").glob("annotations-*.jsonl")):
        info = passes.get(path.name)
        if info is None:
            print(f"{path.name} is not assigned to any actor by the batch plan", file=sys.stderr)
            return 2
        for row in read_jsonl(path):
            number = int(row["issue_number"])
            if number in annotations:
                # A second annotation of the same candidate is allowed only where the
                # re-annotation plan declares this file as the one that supersedes it, and
                # the earlier verdict is kept rather than dropped.
                if superseded_by.get(number) != path.name:
                    print(f"issue {number}: annotated twice ({path.name})", file=sys.stderr)
                    return 2
                # Only an `unresolved` row may be re-rolled, and only once. Both bounds come
                # from projects/graphi/stories/SW-279/decision-unresolved-reannotation.md,
                # which permitted the fresh-pass route and then found it had no bound at all:
                # the finalizer accepted a second annotation whatever the first-pass verdict
                # was, so an operator could re-roll an `accept` or a `reject` whose outcome
                # they disliked, and §4's "an unresolved row blocks completion" would never
                # bite. An unresolved row is by construction one with no outcome to dislike;
                # an accepted or rejected row is permanently non-re-rollable; and if a second
                # pass also returns `unresolved`, Phase 2 blocks and reports - there is no
                # pass three.
                # The cap is checked before the eligibility clause, because on a third
                # annotation the "first-pass" verdict has already been replaced by the second
                # pass's, and reporting that as an ineligible supersession would name the
                # wrong defect.
                if number in superseded_annotation:
                    print(
                        f"issue {number}: a third annotation ({path.name}). Exactly one "
                        "re-roll per row, ever. If the second pass also returns `unresolved`, "
                        "Phase 2 blocks and is reported; it is not re-run until it stops.",
                        file=sys.stderr,
                    )
                    return 2
                previous = str(annotations[number]["verdict"])
                if previous != "unresolved":
                    print(
                        f"issue {number}: {path.name} supersedes a first-pass verdict of "
                        f"{previous!r}. Only an `unresolved` row may be re-annotated: allowing "
                        "an accept or a reject to be re-rolled is 're-roll the rows whose "
                        "answer you dislike', and it empties the blocking clause the "
                        "re-annotation route exists to serve.",
                        file=sys.stderr,
                    )
                    return 2
                superseded_annotation[number] = {
                    "annotator": annotator_of[number],
                    "annotations_file": str(pass_of[number]["annotations_file"]),
                    "verdict": str(annotations[number]["verdict"]),
                    "disqualifier": annotations[number].get("disqualifier"),
                    "note": annotations[number].get("note"),
                }
            annotations[number] = row
            annotator_of[number] = str(info["annotator"])
            planned_reviewer_of[number] = str(info["reviewer"])
            pass_of[number] = dict(info, annotations_file=path.name)

    unused = sorted(number for number in superseded_by if number not in superseded_annotation)
    if unused:
        print(
            f"the re-annotation plan declares a supersession for {unused} but no first-pass "
            "annotation was superseded; a supersession that supersedes nothing is a mis-wired plan",
            file=sys.stderr,
        )
        return 2

    reviews: dict[int, dict[str, object]] = {}
    superseded_review: dict[int, dict[str, object]] = {}
    for path in sorted((harvest / "answerability").glob("reviews-*.jsonl")):
        for row in read_jsonl(path):
            number = int(row["issue_number"])
            if number in reviews:
                if str(pass_of.get(number, {}).get("reviews_file")) != path.name:
                    print(f"issue {number}: reviewed twice ({path.name})", file=sys.stderr)
                    return 2
                superseded_review[number] = {
                    "reviewer": reviews[number].get("reviewer"),
                    "reviewer_verdict": reviews[number].get("reviewer_verdict"),
                    "agrees": reviews[number].get("agrees"),
                    "note": reviews[number].get("note"),
                }
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

        # NOTHING converts an unresolved row. Section 4 says an unresolved candidate "is not
        # silently treated as a reject: it blocks completion of Phase 2 and is reported", and
        # Section 8 lists "reinterpret an unresolved row as a reject" among the acts the
        # selector, family reviewers and source annotators are forbidden to perform. The
        # reviewer's own view is recorded on the row - published, not applied - and the row
        # stays `unresolved` until an actor that has not seen it produces a first-person
        # terminal determination under the re-annotation plan.
        reviewer_verdict = str(review.get("reviewer_verdict") or "")
        if verdict == "unresolved":
            unresolved_reviewer_view = reviewer_verdict or None
        else:
            unresolved_reviewer_view = None

        if not reviewer:
            problems.append(f"issue {number}: no independent reviewer recorded")
        if reviewer and annotator and reviewer == annotator:
            problems.append(f"issue {number}: reviewer and annotator are the same actor {reviewer!r}")
        planned_reviewer = planned_reviewer_of[number]
        if reviewer and reviewer != planned_reviewer:
            problems.append(
                f"issue {number}: reviewed by {reviewer!r}, but the plan pairs "
                f"{annotator!r} with {planned_reviewer!r}"
            )
        for judgement in annotation.get("judgements", []):
            recorded = str(judgement.get("annotator") or "")
            if recorded and recorded != annotator:
                problems.append(
                    f"issue {number}: a judgement records annotator {recorded!r}, but the plan "
                    f"assigns {annotator!r} to {pass_of[number]['annotations_file']}"
                )
                break

        # The reviewer's grade governs. Section 6 makes grade inflation a violation and
        # requires an independent reviewer per judgement, so a grade the reviewer read the
        # bytes and disagreed with is not the grade that reaches the dataset. Both are kept
        # so the disagreement stays visible rather than being absorbed.
        reviewed_grade: dict[tuple[str, int, int], int] = {}
        for check in review.get("span_checks", []):
            try:
                key = (str(check["path"]), int(check["start_line"]), int(check["end_line"]))
            except (KeyError, TypeError, ValueError):
                continue
            if check.get("grade_as_reviewed") is None:
                continue
            reviewed_grade[key] = int(check["grade_as_reviewed"])

        checked: list[dict[str, object]] = []
        grade3_go = 0
        regrades = 0
        for judgement in annotation.get("judgements", []):
            path = str(judgement["path"])
            start = int(judgement["start_line"])
            end = int(judgement["end_line"])
            anchor = str(judgement["anchor"])
            annotated_grade = int(judgement["grade"])
            grade = reviewed_grade.get((path, start, end), annotated_grade)
            if grade != annotated_grade:
                regrades += 1
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
                "grade": grade,
                "grade_as_annotated": annotated_grade,
                "grade_as_reviewed": reviewed_grade.get((path, start, end)),
                "grade_regraded_by_reviewer": grade != annotated_grade,
                "reason": judgement.get("reason"),
                "annotator": judgement.get("annotator"), "reviewer": reviewer,
                "anchor_resolves_at_pin": resolved,
                "resolution_note": detail or "resolved at the pin",
            })

        # A2 is checked against the REVIEWED grades, so a row that only reached A2 on a
        # grade the reviewer took away fails here rather than reaching the dataset.
        if verdict == "answerable" and grade3_go == 0:
            problems.append(
                f"issue {number}: answerable with no grade-3 .go span surviving review (A2); "
                f"{regrades} span(s) were regraded by the reviewer"
            )
        cited_locations: list[dict[str, object]] = []
        if verdict == "not_answerable":
            disqualifier = str(annotation.get("disqualifier") or "")
            if disqualifier not in D_CLAUSES:
                problems.append(f"issue {number}: not_answerable without a D1-D5 disqualifier")
            cited_locations, citation_failures = check_note_citations(
                str(annotation.get("note") or "") + "\n" + str(review.get("note") or ""),
                tracked_by_basename, tracked, cobra,
            )
            for failure in citation_failures:
                problems.append(f"issue {number}: rejection evidence cites {failure}")
            if not cited_locations:
                problems.append(
                    f"issue {number}: not_answerable with no pinned-source citation in either the "
                    "annotator's or the reviewer's note; a D-clause needs positive evidence, not a label"
                )

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
            "annotator_slot": pass_of[number]["annotator_slot"],
            "annotations_file": pass_of[number]["annotations_file"],
            "reviewer": reviewer,
            "reviewer_slot": pass_of[number]["reviewer_slot"],
            "reviewer_agrees": review.get("agrees"),
            "reviewer_verdict": review.get("reviewer_verdict"),
            "annotator_verdict": str(annotation["verdict"]),
            "reviewer_view_on_unresolved_row": unresolved_reviewer_view,
            "annotator_note": annotation.get("note"),
            "reviewer_note": review.get("note"),
            "spans_regraded_by_reviewer": regrades,
            "rejection_evidence_citations": cited_locations,
            "rejection_evidence_citations_checked": (
                "every file:line reference in the rejection notes resolves to a tracked file at "
                "the pin with the cited lines inside it; whether those bytes say what the note "
                "claims is a human judgement this check does not make"
            ) if verdict == "not_answerable" else None,
            "superseded_annotation": superseded_annotation.get(number),
            "superseded_review": superseded_review.get(number),
            "reannotated": number in superseded_annotation,
            "judgements": checked,
        })

    if problems:
        print("\n".join(problems), file=sys.stderr)
        print(f"\n{len(problems)} answerability violations; refusing to write the outcome", file=sys.stderr)
        return 2

    # Phase 2 is complete only if no candidate is still unresolved. A blocked run still
    # writes everything - the block has to be reported, and the ledger is the report - but it
    # writes under `-blocked` names, so the authoritative artefact names stay unclaimed and a
    # later completing run does not have to fight a refusal to overwrite. The first attempt
    # at this story wrote the authoritative ledger on a blocked run, then had to re-run and
    # leave a dangling digest in the access ledger; that is the failure this naming avoids.
    blocked = [int(row["issue_number"]) for row in ledger_rows if row["state"] == "unresolved"]
    suffix = "-blocked" if blocked else ""
    ledger_path = harvest / f"answerability-ledger{suffix}.jsonl"
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
    unresolved = blocked
    regraded = [int(row["issue_number"]) for row in ledger_rows if row.get("spans_regraded_by_reviewer")]
    reannotated = [int(row["issue_number"]) for row in ledger_rows if row.get("reannotated")]
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
        "rejection_evidence_citations_resolved": sum(
            len(row.get("rejection_evidence_citations") or []) for row in ledger_rows
        ),
        "rejection_evidence_check_limit": (
            "every file:line reference in every rejection note resolves to a tracked file at the "
            "pin containing the cited lines, and a reject with no such citation is refused. "
            "Whether the cited bytes support the claim made about them is a human judgement and "
            "is NOT machine-checked, unlike the graded spans."
        ),
        "unresolved_issue_numbers": unresolved,
        "unresolved_blocks_completion": bool(unresolved),
        "unresolved_conversions_performed": (
            "none, in either direction: Section 4 forbids treating an unresolved row as a "
            "reject and Section 8 lists reinterpreting one among the forbidden acts, so this "
            "script converts nothing. An unresolved row blocks and is reported."
        ),
        "reannotated_issue_numbers": reannotated,
        "reviewer_disagreement_issue_numbers": disagreements,
        "reviewer_regraded_issue_numbers": regraded,
        "reviewer_regraded_span_count": sum(int(row.get("spans_regraded_by_reviewer", 0)) for row in ledger_rows),
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
    outcome_path = harvest / f"phase-2-outcome{suffix}.json"
    if outcome_path.exists():
        print(f"refusing to overwrite {outcome_path}", file=sys.stderr)
        return 2
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
            "independent reviewer distinct from the annotator on every row - the annotator taken "
            "from the batch plan and that actor's attestation, so the check also covers rows with "
            "no judgements. No unresolved row was converted in either direction. No retrieval access."
            + (f" BLOCKED: {len(blocked)} unresolved row(s) {blocked}; written under -blocked names."
               if blocked else "")
        ),
    )

    print(json.dumps(outcome, ensure_ascii=False, indent=2))
    if blocked:
        print(
            f"\nPhase 2 is BLOCKED: {len(blocked)} unresolved row(s) {blocked}. Section 4: an "
            "unresolved candidate is not silently treated as a reject; it blocks completion and "
            "is reported. Nothing here converts it. The only way forward is a fresh terminal "
            "determination by an actor that has not seen the row, declared in "
            f"answerability/reannotation-plan.json. Written to {ledger_path} and {outcome_path}.",
            file=sys.stderr,
        )
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
