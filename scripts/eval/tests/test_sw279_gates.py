#!/usr/bin/env python3
"""Prove that SW-279's Phase 2 gates refuse when they should, by breaking things.

A gate that has only ever been run on data it passes is not evidence of anything - that was
finding M4 of the round-1 review, where the "no actor reviewed its own judgements" check
reported PASS on 23 rows it structurally could not fail. So the refusal cases here all work
the same way: build a throwaway copy of the parts of this repository the SW-279 scripts read,
break exactly one thing, and assert the script refuses with a message naming what is wrong.

**The suite is NOT all refusals, and saying so was itself a defect.** It has two kinds of
case, and the round-1 commit message described all of them as refusals:

  * REFUSAL cases break one thing and assert the script stops. There are 43.
  * POSITIVE CONTROL cases change nothing and assert the script still produces the committed
    artefact byte for byte, or accept a legitimate variation, because a gate that always fails
    looks identical to one that works. There are 8, named in `POSITIVE_CONTROLS` below.

51 cases in total. `declared()` re-derives both counts from the module at run time, and the
last line of a run states them, so the Go wrapper asserts against a contract rather than
against the shape of unittest's console output.

Run directly (`python3 scripts/eval/tests/test_sw279_gates.py`) or through
`go test ./internal/eval/retrieval -run TestSW279_ScriptGates`.

Fifteen of these need a read-only clone of spf13/cobra at the pin, because the finalizer
verifies the checkout HEAD before it will read a span. They SKIP visibly when it is absent -
and a skip is a NON-RESULT, not a pass: the Go wrapper knows which cases may skip and why,
asserts the total case count, and refuses to report a partial run as green.
"""

from __future__ import annotations

import hashlib
import json
import os
import shutil
import subprocess
import sys
import tempfile
import unittest
from pathlib import Path

REPO = Path(__file__).resolve().parents[3]
HARVEST = "sw-279-phase-2a2"
REVIEW = "sw-279-phase-2b2-family-review"
PIN = "a0a6ae020bb3899ff0276067863e50523f897370"

# The one row that was ever `unresolved`, and the pass that settled it.
CONTESTED = 1780

# What a throwaway copy needs: the scripts, the two harvest directories, the frozen rule and
# the v1 dataset the yield arithmetic reads.
COPY_TREES = (
    "scripts/eval",
    f"docs/eval/retrieval/harvests/{HARVEST}",
    f"docs/eval/retrieval/harvests/{REVIEW}",
)
COPY_FILES = (
    "docs/eval/retrieval/dataset-v2-inclusion-rule.md",
    "internal/eval/retrieval/testdata/datasets/cobra-v1.json",
)

# Cases that assert a script still WORKS, rather than that it refuses. Everything else in the
# module is a refusal case. Keep this list honest: a refusal suite with no positive control is
# indistinguishable from a suite of always-failing gates, and a suite that calls its positive
# controls refusals is misreporting its own evidence.
POSITIVE_CONTROLS = frozenset({
    "test_the_completing_run_reproduces_the_committed_ledger_byte_for_byte",
    "test_building_the_historical_batch_reproduces_its_input_byte_for_byte",
    "test_full_coverage_reproduces_the_committed_ledger_byte_for_byte",
    "test_a_seal_in_the_right_order_reproduces_the_sealed_questions_byte_for_byte",
    "test_either_invocation_order_produces_the_same_attestation_digests",
    "test_no_committed_ledger_row_pins_a_stale_attestation_digest",
    "test_the_shipped_query_matches_the_declared_selection_set",
    "test_a_whitespace_reformatted_query_is_still_accepted",
})


def cobra_root() -> Path | None:
    root = Path(os.environ.get("GRAPHI_CORPUS_COBRA")
                or Path.home() / ".cache" / "graphi" / "corpus" / "cobra")
    if not (root / "command.go").exists():
        return None
    head = subprocess.run(["git", "-C", str(root), "rev-parse", "HEAD"],
                          stdout=subprocess.PIPE, text=True)
    if head.returncode != 0 or head.stdout.strip().lower() != PIN:
        return None
    return root


def sha256_file(path: Path) -> str:
    return hashlib.sha256(path.read_bytes()).hexdigest()


class Sandbox:
    """A repo-shaped throwaway copy. Nothing here can touch the real repository."""

    def __init__(self) -> None:
        self.dir = Path(tempfile.mkdtemp(prefix="sw279-gate-"))
        for tree in COPY_TREES:
            shutil.copytree(REPO / tree, self.dir / tree,
                            ignore=shutil.ignore_patterns("__pycache__"))
        for name in COPY_FILES:
            (self.dir / name).parent.mkdir(parents=True, exist_ok=True)
            shutil.copy2(REPO / name, self.dir / name)
        # A real commit, because the seal records the repository HEAD it was taken at.
        subprocess.run(["git", "init", "-q"], cwd=self.dir, check=True)
        subprocess.run(["git", "add", "-A"], cwd=self.dir, check=True,
                       stdout=subprocess.DEVNULL, stderr=subprocess.STDOUT)
        subprocess.run(["git", "commit", "-q", "-m", "sandbox"], cwd=self.dir, check=True,
                       env={**os.environ,
                            "GIT_AUTHOR_NAME": "sw279-gate-test",
                            "GIT_AUTHOR_EMAIL": "sw279@example.invalid",
                            "GIT_COMMITTER_NAME": "sw279-gate-test",
                            "GIT_COMMITTER_EMAIL": "sw279@example.invalid"},
                       stdout=subprocess.DEVNULL, stderr=subprocess.STDOUT)

    def close(self) -> None:
        shutil.rmtree(self.dir, ignore_errors=True)

    def path(self, *parts: str) -> Path:
        return self.dir.joinpath(*parts)

    def harvest(self, *parts: str) -> Path:
        return self.path("docs/eval/retrieval/harvests", HARVEST, *parts)

    def answerability(self, *parts: str) -> Path:
        return self.harvest("answerability", *parts)

    def review(self, *parts: str) -> Path:
        return self.path("docs/eval/retrieval/harvests", REVIEW, *parts)

    def run(self, script: str, *args: str) -> subprocess.CompletedProcess:
        return subprocess.run(
            [sys.executable, f"scripts/eval/{script}", *args],
            cwd=self.dir, stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True,
        )

    def read_json(self, path: Path) -> dict:
        return json.loads(path.read_text(encoding="utf-8"))

    def write_json(self, path: Path, value: dict) -> None:
        path.write_text(json.dumps(value, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")

    def rewrite_jsonl(self, path: Path, edit) -> None:
        """Apply `edit(row) -> row | None` to every row; a None result drops the row."""
        rows = [json.loads(line) for line in path.read_text(encoding="utf-8").splitlines() if line.strip()]
        kept = [edited for edited in (edit(row) for row in rows) if edited is not None]
        path.write_text(
            "".join(json.dumps(row, ensure_ascii=False, separators=(",", ":")) + "\n" for row in kept),
            encoding="utf-8")

    def plans(self) -> list[dict]:
        batches = self.read_json(self.answerability("batch-plan.json"))["batches"]
        reannotation = self.answerability("reannotation-plan.json")
        if reannotation.exists():
            batches = batches + self.read_json(reannotation)["batches"]
        return batches

    def reattest(self) -> None:
        """Recompute every attested digest and assignment from the files as they now stand.

        The finalizer refuses when an attestation does not describe the committed bytes, which
        is the point of those checks - and it means a test that mutates an annotation file
        would otherwise trip the digest gate instead of the gate it means to exercise. This
        restores internal consistency after a deliberate mutation, so each case fails for its
        own reason.
        """
        for batch in self.plans():
            input_path = self.dir / str(batch["input"])
            annotations = self.dir / str(batch["annotations_output"])
            reviews = self.dir / str(batch["reviews_output"])
            numbers = sorted({int(json.loads(line)["issue_number"])
                              for line in input_path.read_text(encoding="utf-8").splitlines()
                              if line.strip()})
            annotator_path = self.answerability(f"annotator-{batch['annotator_slot']}-attestation.json")
            reviewer_path = self.answerability(f"reviewer-{batch['reviewer_slot']}-attestation.json")
            annotator = self.read_json(annotator_path)
            annotator["assigned_issue_numbers"] = numbers
            annotator["output_sha256"] = sha256_file(annotations)
            if annotator.get("input_sha256") is not None:
                annotator["input_sha256"] = sha256_file(input_path)
            if isinstance(annotator.get("input_artifact_orchestrator_recorded"), dict):
                annotator["input_artifact_orchestrator_recorded"]["sha256"] = sha256_file(input_path)
            self.write_json(annotator_path, annotator)

            reviewer = self.read_json(reviewer_path)
            reviewer["reviewed_issue_numbers"] = numbers
            reviewer["output_sha256"] = sha256_file(reviews)
            reviewer["annotator_file_sha256"] = sha256_file(annotations)
            if reviewer.get("input_sha256") is not None:
                reviewer["input_sha256"] = sha256_file(input_path)
            self.write_json(reviewer_path, reviewer)

            if "input_sha256" in batch:
                plan_path = (self.answerability("reannotation-plan.json")
                             if "supersedes_annotation_for" in batch
                             else self.answerability("batch-plan.json"))
                plan = self.read_json(plan_path)
                for entry in plan["batches"]:
                    if int(entry["batch"]) == int(batch["batch"]):
                        entry["input_sha256"] = sha256_file(input_path)
                self.write_json(plan_path, plan)

    def restore_pre_reannotation_state(self) -> None:
        """Put the harvest back as it stood before #1780 was re-annotated."""
        for name in ("annotations-6.jsonl", "reviews-6.jsonl", "batch-6-input.jsonl",
                     "reannotation-plan.json", "annotator-A6-attestation.json",
                     "reviewer-R6-attestation.json"):
            self.answerability(name).unlink(missing_ok=True)
        self.harvest("answerability-ledger.jsonl").unlink(missing_ok=True)
        self.harvest("phase-2-outcome.json").unlink(missing_ok=True)


class SandboxTest(unittest.TestCase):
    def setUp(self) -> None:
        self.box = Sandbox()
        self.addCleanup(self.box.close)


class CobraTest(SandboxTest):
    """Cases that drive the answerability finalizer, which verifies the pinned checkout."""

    def setUp(self) -> None:
        super().setUp()
        self.cobra = cobra_root()
        if self.cobra is None:
            self.skipTest("SKIP: no spf13/cobra clone at the pin; the finalizer cannot run")

    def finalize(self) -> subprocess.CompletedProcess:
        return self.box.run("finalize_sw279_answerability.py",
                            "--harvest", HARVEST, "--cobra-root", str(self.cobra))

    def clear_outputs(self) -> None:
        self.box.harvest("answerability-ledger.jsonl").unlink(missing_ok=True)
        self.box.harvest("phase-2-outcome.json").unlink(missing_ok=True)


class UnresolvedRowsBlockAndAreNeverConverted(CobraTest):
    """B1. Section 4: an unresolved candidate "is not silently treated as a reject: it blocks
    completion of Phase 2 and is reported". Section 8 lists "reinterpret an unresolved row as
    a reject" among the forbidden acts. An earlier finalizer did exactly that whenever the
    independent reviewer supplied a D-clause. If that conversion is ever reinstated, this test
    fails: the run would return 0 and the row would read `reject:not_answerable`."""

    def test_an_unresolved_row_blocks_completion_and_keeps_its_verdict(self) -> None:
        # Restore the historical situation exactly: issue 1780 annotated `unresolved`, its
        # independent reviewer offering a positive D3 finding, and no re-annotation pass.
        self.box.restore_pre_reannotation_state()

        result = self.finalize()

        self.assertEqual(result.returncode, 3, msg=result.stderr)
        self.assertIn("BLOCKED", result.stderr)
        self.assertIn(str(CONTESTED), result.stderr)
        self.assertFalse(self.box.harvest("answerability-ledger.jsonl").exists(),
                         "a blocked run must not claim the authoritative ledger name")
        blocked = self.box.harvest("answerability-ledger-blocked.jsonl")
        self.assertTrue(blocked.exists(), "a blocked run must still report, under a -blocked name")

        rows = {json.loads(line)["issue_number"]: json.loads(line)
                for line in blocked.read_text(encoding="utf-8").splitlines() if line.strip()}
        row = rows[CONTESTED]
        self.assertEqual(row["verdict"], "unresolved")
        self.assertEqual(row["state"], "unresolved")
        # The reviewer's D-clause is published on the row, and applied to nothing.
        self.assertIsNone(row["disqualifier"])
        self.assertEqual(row["reviewer_verdict"], "not_answerable")
        self.assertIn("D3", str(row["reviewer_note"]))

    def test_the_completing_run_reproduces_the_committed_ledger_byte_for_byte(self) -> None:
        """Positive control. The gate above must not be firing because the finalizer is broken."""
        committed = (REPO / "docs/eval/retrieval/harvests" / HARVEST / "answerability-ledger.jsonl").read_bytes()
        self.clear_outputs()

        result = self.finalize()

        self.assertEqual(result.returncode, 0, msg=result.stderr)
        self.assertEqual(self.box.harvest("answerability-ledger.jsonl").read_bytes(), committed)


class TheIndependenceGuardFiresOnRejectionRows(CobraTest):
    """M-c. AC-8 requires the annotator recorded per judgement. Rejections carry no judgements,
    so inferring the annotator from them recorded a filename - and the guard that compares
    annotator against reviewer could never fire on the 23 rows where a rejection was decided.
    Identity now comes from the batch plan and each actor's own attestation."""

    def setUp(self) -> None:
        super().setUp()
        self.clear_outputs()

    def rejection_rows_in_batch_4(self) -> list[int]:
        rows = [json.loads(line) for line
                in self.box.answerability("annotations-4.jsonl").read_text(encoding="utf-8").splitlines()
                if line.strip()]
        return [int(row["issue_number"]) for row in rows
                if row["verdict"] == "not_answerable" and not row["judgements"]]

    def test_one_actor_annotating_and_reviewing_a_rejection_is_refused(self) -> None:
        rejections = self.rejection_rows_in_batch_4()
        self.assertTrue(rejections, "batch 4 must contain a rejection row with no judgements")

        # Wire R4 to be the same actor as A4 - in the attestation, so that the plan itself
        # says one actor did both jobs and the mis-pairing check has nothing to complain about.
        annotator = self.box.read_json(
            self.box.answerability("annotator-A4-attestation.json"))["actor"]
        reviewer_path = self.box.answerability("reviewer-R4-attestation.json")
        reviewer = self.box.read_json(reviewer_path)
        reviewer["actor"] = annotator
        self.box.write_json(reviewer_path, reviewer)
        self.box.rewrite_jsonl(self.box.answerability("reviews-4.jsonl"),
                               lambda row: {**row, "reviewer": annotator})
        self.box.reattest()

        result = self.finalize()

        self.assertEqual(result.returncode, 2, msg=result.stdout)
        self.assertIn("reviewer and annotator are the same actor", result.stderr)
        # The point of the finding: the guard now names the rejection rows too.
        for number in rejections:
            self.assertIn(f"issue {number}: reviewer and annotator are the same actor", result.stderr)

    def test_a_reviewer_the_plan_did_not_pair_with_this_annotator_is_refused(self) -> None:
        self.box.rewrite_jsonl(
            self.box.answerability("reviews-4.jsonl"),
            lambda row: {**row, "reviewer": "Claude Opus 5 (SW-279 answerability reviewer R2)"})
        self.box.reattest()
        result = self.finalize()
        self.assertEqual(result.returncode, 2, msg=result.stdout)
        self.assertIn("but the plan pairs", result.stderr)

    def test_a_judgement_claiming_another_annotator_is_refused(self) -> None:
        def relabel(row: dict) -> dict:
            if row["judgements"]:
                row = {**row, "judgements": [{**j, "annotator": "someone else"} for j in row["judgements"]]}
            return row

        self.box.rewrite_jsonl(self.box.answerability("annotations-4.jsonl"), relabel)
        self.box.reattest()
        result = self.finalize()
        self.assertEqual(result.returncode, 2, msg=result.stdout)
        self.assertIn("records annotator", result.stderr)

    def test_a_rejection_whose_note_cites_a_file_absent_at_the_pin_is_refused(self) -> None:
        """m7. Rejection evidence used to be unchecked prose."""
        def break_citation(row: dict) -> dict:
            if row["verdict"] == "not_answerable":
                return {**row, "note": "D4 established at not_a_real_file.go:1-3."}
            return row

        self.box.rewrite_jsonl(self.box.answerability("annotations-4.jsonl"), break_citation)
        self.box.rewrite_jsonl(self.box.answerability("reviews-4.jsonl"),
                               lambda row: {**row, "note": "agreed"})
        self.box.reattest()
        result = self.finalize()
        self.assertEqual(result.returncode, 2, msg=result.stdout)
        self.assertIn("no such tracked file at the pin", result.stderr)

    def test_a_rejection_citing_a_path_outside_the_old_extension_allowlist_is_refused(self) -> None:
        """m7, round 2. The citation parser recognised only go/md/mod/txt/yaml/yml/sh, so a
        citation with any other extension matched nothing at all: it was not resolved, and its
        failure to resolve was not reported either. `definitely-missing.json:999` sailed
        through and the run returned 0. Silently ignoring a citation a reader will follow is
        the failure mode, so the parser now recognises any path-shaped token and refuses when
        it does not resolve at the pin."""
        def break_citation(row: dict) -> dict:
            if row["verdict"] == "not_answerable":
                return {**row, "note": "D4 established at definitely-missing.json:999."}
            return row

        self.box.rewrite_jsonl(self.box.answerability("annotations-4.jsonl"), break_citation)
        self.box.rewrite_jsonl(self.box.answerability("reviews-4.jsonl"),
                               lambda row: {**row, "note": "agreed"})
        self.box.reattest()
        result = self.finalize()
        self.assertEqual(result.returncode, 2, msg=result.stdout)
        self.assertIn("definitely-missing.json:999", result.stderr)
        self.assertIn("no such tracked file at the pin", result.stderr)


class TheReannotationPassMustBeFreshAndAttested(CobraTest):
    """N1 (round 2). Identity used to come from the plan's slot NAME and the attestation's
    actor STRING, and nothing checked that the two described the same piece of work. Renaming
    the re-annotation plan's `annotator_slot` from A6 to A4 therefore returned 0 and attributed
    #1780 to the actor whose `unresolved` verdict the fresh pass exists to replace - the one
    thing Section 4's "an actor that has not seen the row" forbids."""

    def setUp(self) -> None:
        super().setUp()
        self.clear_outputs()

    def plan(self) -> dict:
        return self.box.read_json(self.box.answerability("reannotation-plan.json"))

    def repoint(self, **slots: str) -> None:
        plan = self.plan()
        plan["batches"][0].update(slots)
        self.box.write_json(self.box.answerability("reannotation-plan.json"), plan)

    def clone_attestation(self, source: str, target: str, actor: str) -> None:
        """Copy an attestation to a new slot, keeping only its actor identity."""
        record = self.box.read_json(self.box.answerability(source))
        record["actor"] = actor
        self.box.write_json(self.box.answerability(target), record)

    def test_pointing_the_superseding_slot_at_an_actor_the_plan_did_not_assign_is_refused(self) -> None:
        """The exploit as performed: `annotator_slot: A6` -> `A4`, nothing else touched."""
        self.repoint(annotator_slot="A4")
        result = self.finalize()
        self.assertEqual(result.returncode, 2, msg=result.stdout)
        self.assertIn("A slot name is not an assignment", result.stderr)
        self.assertFalse(self.box.harvest("answerability-ledger.jsonl").exists())

    def test_a_superseding_annotator_that_already_annotated_the_row_is_refused(self) -> None:
        """The same exploit with the paperwork made consistent, so only freshness can catch it:
        a slot A7 whose assignment and digests are batch 6's, carrying A4's actor identity."""
        a4 = self.box.read_json(self.box.answerability("annotator-A4-attestation.json"))["actor"]
        self.clone_attestation("annotator-A6-attestation.json",
                               "annotator-A7-attestation.json", a4)
        self.repoint(annotator_slot="A7")
        result = self.finalize()
        self.assertEqual(result.returncode, 2, msg=result.stdout)
        self.assertIn("already ruled on that row in batch 4", result.stderr)
        self.assertIn("an actor cannot supersede its own judgement", result.stderr)

    def test_a_superseding_reviewer_that_already_reviewed_the_row_is_refused(self) -> None:
        r4 = self.box.read_json(self.box.answerability("reviewer-R4-attestation.json"))["actor"]
        self.clone_attestation("reviewer-R6-attestation.json",
                               "reviewer-R7-attestation.json", r4)
        self.repoint(reviewer_slot="R7")
        result = self.finalize()
        self.assertEqual(result.returncode, 2, msg=result.stdout)
        self.assertIn("already ruled on that row in batch 4", result.stderr)

    def test_an_attested_output_digest_that_is_not_the_committed_bytes_is_refused(self) -> None:
        path = self.box.answerability("annotator-A6-attestation.json")
        record = self.box.read_json(path)
        record["output_sha256"] = "0" * 64
        self.box.write_json(path, record)
        result = self.finalize()
        self.assertEqual(result.returncode, 2, msg=result.stdout)
        self.assertIn("is attested as " + "0" * 64, result.stderr)
        self.assertIn("committed bytes hash to", result.stderr)

    def test_an_attested_input_digest_that_is_not_the_committed_bytes_is_refused(self) -> None:
        path = self.box.answerability("annotator-A6-attestation.json")
        record = self.box.read_json(path)
        record["input_sha256"] = "1" * 64
        self.box.write_json(path, record)
        result = self.finalize()
        self.assertEqual(result.returncode, 2, msg=result.stdout)
        self.assertIn("annotator A6 input", result.stderr)
        self.assertIn("committed bytes hash to", result.stderr)


class TheFinalizerBoundsTheReRollChannel(CobraTest):
    """`projects/graphi/stories/SW-279/decision-unresolved-reannotation.md`. The fresh-pass
    route out of `unresolved` is permitted, and as implemented it had no bound at all: the
    finalizer honoured a second annotation whatever the first-pass verdict was, so an operator
    could re-roll an `accept` or a `reject` whose answer they disliked and Section 4's
    "an unresolved row blocks completion" would never bite."""

    def setUp(self) -> None:
        super().setUp()
        self.clear_outputs()

    def make_the_contested_row_a_reject(self) -> None:
        """Give #1780 a terminal first-pass verdict, so the supersession has an outcome to
        overturn rather than a blocking state to settle."""
        def settle(row: dict) -> dict:
            if int(row["issue_number"]) == CONTESTED:
                return {**row, "verdict": "not_answerable", "disqualifier": "D3",
                        "note": "D3: the answer is not in the pinned source; see command.go:1-3.",
                        "judgements": []}
            return row

        self.box.rewrite_jsonl(self.box.answerability("annotations-4.jsonl"), settle)
        self.box.reattest()

    def test_superseding_a_row_that_is_not_unresolved_is_refused(self) -> None:
        self.make_the_contested_row_a_reject()
        result = self.finalize()
        self.assertEqual(result.returncode, 2, msg=result.stdout)
        self.assertIn("supersedes a first-pass verdict of 'not_answerable'", result.stderr)
        self.assertIn("re-roll the rows whose answer you dislike", result.stderr)

    def test_a_second_supersession_of_the_same_row_is_refused(self) -> None:
        """Exactly one re-roll per row, ever. A second re-annotation batch for the same issue
        is refused at the plan, before any of its output is read."""
        for name, source in (("batch-7-input.jsonl", "batch-6-input.jsonl"),
                             ("annotations-7.jsonl", "annotations-6.jsonl"),
                             ("reviews-7.jsonl", "reviews-6.jsonl")):
            shutil.copy2(self.box.answerability(source), self.box.answerability(name))
        for slot, source in (("annotator-A7-attestation.json", "annotator-A6-attestation.json"),
                             ("reviewer-R7-attestation.json", "reviewer-R6-attestation.json")):
            record = self.box.read_json(self.box.answerability(source))
            record["actor"] = record["actor"].replace("A6", "A7").replace("R6", "R7")
            self.box.write_json(self.box.answerability(slot), record)
        plan_path = self.box.answerability("reannotation-plan.json")
        plan = self.box.read_json(plan_path)
        second = dict(plan["batches"][0])
        second.update({
            "batch": 7,
            "input": second["input"].replace("batch-6-input", "batch-7-input"),
            "annotator_slot": "A7",
            "reviewer_slot": "R7",
            "annotations_output": second["annotations_output"].replace("annotations-6", "annotations-7"),
            "reviews_output": second["reviews_output"].replace("reviews-6", "reviews-7"),
        })
        plan["batches"].append(second)
        self.box.write_json(plan_path, plan)
        self.box.reattest()

        result = self.finalize()

        self.assertEqual(result.returncode, 2, msg=result.stdout)
        self.assertIn("is declared superseded by two batches", result.stderr)
        self.assertIn("Exactly one re-roll per row, ever", result.stderr)

    def test_a_third_annotation_of_the_same_row_is_refused(self) -> None:
        """The same cap from the other direction: one plan, one superseding file, but that
        file carrying the row twice."""
        rows = self.box.answerability("annotations-6.jsonl").read_text(encoding="utf-8")
        self.box.answerability("annotations-6.jsonl").write_text(rows + rows, encoding="utf-8")
        self.box.reattest()
        result = self.finalize()
        self.assertEqual(result.returncode, 2, msg=result.stdout)
        self.assertIn("a third annotation", result.stderr)


class TheReannotationBuilderBoundsTheReRollChannel(SandboxTest):
    """The same three bounds at the other end: the builder validated only that the requested
    numbers were in the sealed set, and never read the existing annotations, so a re-annotation
    batch could be built for a row that already had an outcome."""

    def build(self, issues: str, batch: str = "6") -> subprocess.CompletedProcess:
        return self.box.run(
            "build_sw279_reannotation_batch.py",
            "--harvest", HARVEST, "--batch", batch, "--issues", issues,
            "--annotator-slot", "A6", "--reviewer-slot", "R6",
            "--reason", "gate test")

    def first_settled_row(self) -> int:
        for line in self.box.answerability("annotations-4.jsonl").read_text(encoding="utf-8").splitlines():
            row = json.loads(line)
            if row["verdict"] != "unresolved":
                return int(row["issue_number"])
        raise AssertionError("batch 4 must contain a settled row")

    def test_re_annotating_a_row_that_is_not_unresolved_is_refused(self) -> None:
        settled = self.first_settled_row()
        self.box.restore_pre_reannotation_state()
        result = self.build(str(settled))
        self.assertEqual(result.returncode, 2, msg=result.stdout)
        self.assertIn(f"issue {settled}: current verdict is", result.stderr)
        self.assertIn("only an `unresolved` row may be re-annotated", result.stderr)
        self.assertFalse(self.box.answerability("reannotation-plan.json").exists())

    def test_re_annotating_a_subset_of_the_unresolved_set_is_refused(self) -> None:
        """The operator may not choose which unresolved rows to re-roll. A second unresolved
        row is introduced so that the historical single-row set becomes a genuine choice."""
        self.box.restore_pre_reannotation_state()
        settled = self.first_settled_row()
        self.box.rewrite_jsonl(
            self.box.answerability("annotations-4.jsonl"),
            lambda row: ({**row, "verdict": "unresolved", "disqualifier": None, "judgements": []}
                         if int(row["issue_number"]) == settled else row))
        result = self.build(str(CONTESTED))
        self.assertEqual(result.returncode, 2, msg=result.stdout)
        self.assertIn("the unresolved set is", result.stderr)
        self.assertIn("The whole eligible set is re-annotated, or none of it", result.stderr)
        self.assertFalse(self.box.answerability("reannotation-plan.json").exists())

    def test_a_second_re_annotation_of_a_row_already_re_annotated_is_refused(self) -> None:
        """The committed harvest already carries one re-annotation of #1780. Removing only the
        plan - the accidental bound that used to be the sole obstacle - must not open a second
        round."""
        self.box.answerability("reannotation-plan.json").unlink()
        self.box.answerability("batch-6-input.jsonl").unlink()
        result = self.build(str(CONTESTED), batch="7")
        self.assertEqual(result.returncode, 2, msg=result.stdout)
        self.assertIn("already carry a second annotation", result.stderr)
        self.assertIn("there is no pass three", result.stderr)

    def test_re_annotating_after_a_retrieval_run_consumed_the_dataset_is_refused(self) -> None:
        """The Phase 2 report asserted that re-annotation after retrieval output exists is
        prevented, and no code checked it: a harvest carrying a run whose dataset id is
        `cobra-v2` produced a batch with rc=0. Re-labelling a row once a number depends on it
        is choosing the label that moves the number, which no per-row bound reaches."""
        self.box.restore_pre_reannotation_state()
        run = self.box.path("docs/eval/retrieval/runs/2026-09-05-gate-local")
        run.mkdir(parents=True)
        self.box.write_json(run / "dataset.json", {"schema_version": 1, "id": "cobra-v2"})

        result = self.build(str(CONTESTED))

        self.assertEqual(result.returncode, 2, msg=result.stdout)
        self.assertIn("dataset.json references cobra-v2", result.stderr)
        self.assertIn("Section 8 step 6 has run for this dataset", result.stderr)
        self.assertFalse(self.box.answerability("reannotation-plan.json").exists())
        self.assertFalse(self.box.answerability("batch-6-input.jsonl").exists())

    def test_building_the_historical_batch_reproduces_its_input_byte_for_byte(self) -> None:
        """Positive control: the four refusals above are not the builder refusing always."""
        committed = (REPO / "docs/eval/retrieval/harvests" / HARVEST
                     / "answerability" / "batch-6-input.jsonl").read_bytes()
        self.box.restore_pre_reannotation_state()
        result = self.build(str(CONTESTED))
        self.assertEqual(result.returncode, 0, msg=result.stderr)
        self.assertEqual(self.box.answerability("batch-6-input.jsonl").read_bytes(), committed)


class TheFamilyLedgerRefusesPartialCoverageAndBadGrammar(SandboxTest):
    """M-d. `answered_a`/`answered_b` were computed and never compared against the population,
    so removing a reviewer's ruling produced a ledger built from 133 of 134 answers and the
    script returned normally. N4 (round 2): coverage was then checked before the answer itself
    was, so an id was marked answered whatever its right-hand side said."""

    def build(self) -> subprocess.CompletedProcess:
        return self.box.run("build_sw279_family_ledger.py", "--harvest", HARVEST, "--review", REVIEW)

    def first_answer_line(self, path: Path) -> tuple[list[str], int]:
        lines = path.read_text(encoding="utf-8").splitlines()
        return lines, next(i for i, line in enumerate(lines) if line.strip().startswith("q-"))

    def test_a_reviewer_that_skipped_one_query_is_refused(self) -> None:
        self.box.review("family-ledger.jsonl").unlink()
        path = self.box.review("family-reviewer-B-codex.txt")
        lines, dropped = self.first_answer_line(path)
        del lines[dropped]
        path.write_text("\n".join(lines) + "\n", encoding="utf-8")

        result = self.build()

        self.assertEqual(result.returncode, 3, msg=result.stdout)
        self.assertIn("1 unanswered", result.stderr)
        self.assertIn("refusing to write a ledger from partial coverage", result.stderr)
        self.assertFalse(self.box.review("family-ledger.jsonl").exists())

    def test_an_unrecognised_reviewer_answer_is_refused(self) -> None:
        """N4, the exploit as performed: one `NONE` becomes `GARBAGE`. It used to be read as an
        explicit "related to nothing", which is not what the reviewer said and silently loses a
        same-task join - and a lost join is how a family ends up split across dev and holdout."""
        self.box.review("family-ledger.jsonl").unlink()
        path = self.box.review("family-reviewer-B-codex.txt")
        lines, index = self.first_answer_line(path)
        target = next(i for i, line in enumerate(lines) if line.strip().endswith("NONE"))
        lines[target] = lines[target].replace("NONE", "GARBAGE")
        path.write_text("\n".join(lines) + "\n", encoding="utf-8")

        result = self.build()

        self.assertEqual(result.returncode, 3, msg=result.stdout)
        self.assertIn("is not a recognised answer", result.stderr)
        self.assertIn("unrecognised reviewer answer", result.stderr)
        self.assertFalse(self.box.review("family-ledger.jsonl").exists())

    def test_a_reviewer_answer_naming_an_id_outside_the_blind_list_is_refused(self) -> None:
        """The other half of the grammar: a well-formed id that is not in the population was
        dropped without comment, so a mistyped join looked exactly like no join at all."""
        self.box.review("family-ledger.jsonl").unlink()
        path = self.box.review("family-reviewer-B-codex.txt")
        lines, index = self.first_answer_line(path)
        target = next(i for i, line in enumerate(lines) if line.strip().endswith("NONE"))
        lines[target] = lines[target].replace("NONE", "q-0000000000")
        path.write_text("\n".join(lines) + "\n", encoding="utf-8")

        result = self.build()

        self.assertEqual(result.returncode, 3, msg=result.stdout)
        self.assertIn("q-0000000000, which is not in blind-queries.txt", result.stderr)
        self.assertFalse(self.box.review("family-ledger.jsonl").exists())

    def test_an_answer_spelling_an_id_in_the_wrong_case_is_refused(self) -> None:
        """Round 3. The right-hand-side grammar was validated case-insensitively while the
        extraction that builds the pairs matched lowercase only, so `Q-CF047FF0B9` validated,
        produced no pair, and returned rc=0 with a ledger missing that join and nothing on
        stderr. A dropped join is how one family ends up split across dev and holdout, which
        is the single thing Section 7's machinery exists to prevent."""
        self.box.review("family-ledger.jsonl").unlink()
        path = self.box.review("family-reviewer-B-codex.txt")
        lines, _ = self.first_answer_line(path)
        target = next(i for i, line in enumerate(lines)
                      if line.strip().startswith("q-") and "->" in line
                      and not line.strip().endswith("NONE"))
        left, arrow, right = lines[target].partition("->")
        lines[target] = left + arrow + right.upper()
        path.write_text("\n".join(lines) + "\n", encoding="utf-8")

        result = self.build()

        self.assertEqual(result.returncode, 3, msg=result.stdout)
        self.assertIn("is not written in the canonical spelling", result.stderr)
        self.assertIn("unrecognised reviewer answer", result.stderr)
        self.assertFalse(self.box.review("family-ledger.jsonl").exists())

    def test_full_coverage_reproduces_the_committed_ledger_byte_for_byte(self) -> None:
        """Positive control for the gates above."""
        committed = (REPO / "docs/eval/retrieval/harvests" / REVIEW / "family-ledger.jsonl").read_bytes()
        self.box.review("family-ledger.jsonl").unlink()
        result = self.build()
        self.assertEqual(result.returncode, 0, msg=result.stderr)
        self.assertEqual(self.box.review("family-ledger.jsonl").read_bytes(), committed)


class TheSealRefusesOutOfOrderAndUnattested(SandboxTest):
    """B2 / m6. Section 8 puts the seal at step 3 and answerability at step 4. The seal used to
    check neither, and wrote "source access before this seal: none for any provisional query"
    unconditionally - a string, not a check. It was run in a clone with all five annotation
    files present and returned 0."""

    def unseal(self) -> None:
        self.box.harvest("sealed-questions.jsonl").unlink()
        self.box.harvest("phase-2-seal.json").unlink()

    def clear_answerability(self) -> None:
        for path in sorted(self.box.harvest("answerability").glob("*")):
            if path.name.startswith(("annotations-", "reviews-", "annotator-", "reviewer-")):
                path.unlink()
        self.box.harvest("answerability-ledger.jsonl").unlink()
        self.box.harvest("phase-2-outcome.json").unlink()

    def seal(self) -> subprocess.CompletedProcess:
        return self.box.run("seal_sw279_phase2.py", "--harvest", HARVEST, "--review", REVIEW)

    def test_sealing_after_the_answers_are_in_is_refused(self) -> None:
        self.unseal()
        result = self.seal()
        self.assertEqual(result.returncode, 2, msg=result.stdout)
        self.assertIn("step-4 output already exists", result.stderr)
        self.assertIn("annotations-1.jsonl", result.stderr)
        self.assertFalse(self.box.harvest("sealed-questions.jsonl").exists())

    def test_sealing_without_a_prerequisite_attestation_is_refused(self) -> None:
        self.unseal()
        self.clear_answerability()
        self.box.harvest("stratum-assigner-attestation.json").unlink()
        result = self.seal()
        self.assertEqual(result.returncode, 2, msg=result.stdout)
        self.assertIn("stratum-assigner-attestation.json", result.stderr)
        self.assertIn("must have attested before it is sealed", result.stderr)
        self.assertFalse(self.box.harvest("sealed-questions.jsonl").exists())

    def test_sealing_without_a_family_reviewer_attestation_is_refused(self) -> None:
        self.unseal()
        self.clear_answerability()
        self.box.review("family-reviewer-B-attestation-of-record.json").unlink()
        result = self.seal()
        self.assertEqual(result.returncode, 2, msg=result.stdout)
        self.assertIn("family-reviewer-B-attestation-of-record.json", result.stderr)

    def test_a_seal_in_the_right_order_reproduces_the_sealed_questions_byte_for_byte(self) -> None:
        """Positive control: the refusals above are not the script simply refusing always."""
        committed = (REPO / "docs/eval/retrieval/harvests" / HARVEST / "sealed-questions.jsonl").read_bytes()
        self.unseal()
        self.clear_answerability()
        result = self.seal()
        self.assertEqual(result.returncode, 0, msg=result.stderr)
        self.assertEqual(self.box.harvest("sealed-questions.jsonl").read_bytes(), committed)

    def test_a_changed_frozen_rule_is_refused(self) -> None:
        self.unseal()
        self.clear_answerability()
        rule = self.box.path("docs/eval/retrieval/dataset-v2-inclusion-rule.md")
        rule.write_bytes(rule.read_bytes() + b"\n")
        result = self.seal()
        self.assertEqual(result.returncode, 2, msg=result.stdout)
        self.assertIn("frozen rule bytes have changed", result.stderr)


class TheLedgerBackfillIsInvocationOrderIndependent(SandboxTest):
    """N2 (round 2). The back-fill snapshotted the already-recorded outputs, then possibly
    rewrote an attestation, then skipped rows that already existed. Running it once without
    `--annotate-attestations` and once with returned 0 both times and left a committed ledger
    row pinning an attestation digest that no longer resolved."""

    LEDGER = ("docs/eval/retrieval/harvests", HARVEST, "access-ledger.jsonl")

    def backfill(self, *args: str) -> subprocess.CompletedProcess:
        return self.box.run("backfill_sw279_answerability_ledger_rows.py",
                            "--harvest", HARVEST, *args)

    def rewind(self) -> None:
        """Undo the round-1 back-fill: drop its ledger rows and its attestation annotations."""
        ledger = self.box.path(*self.LEDGER)
        kept = [line for line in ledger.read_text(encoding="utf-8").splitlines()
                if line.strip() and not json.loads(line).get("attestation")]
        ledger.write_text("\n".join(kept) + "\n", encoding="utf-8")
        for path in sorted(self.box.harvest("answerability").glob("annotator-*-attestation.json")):
            record = self.box.read_json(path)
            if record.pop("input_artifact_orchestrator_recorded", None) is not None:
                self.box.write_json(path, record)

    def stale_rows(self) -> list[int]:
        stale = []
        for line in self.box.path(*self.LEDGER).read_text(encoding="utf-8").splitlines():
            if not line.strip():
                continue
            row = json.loads(line)
            if not row.get("attestation") or not row.get("attestation_sha256"):
                continue
            path = self.box.path(str(row["attestation"]))
            if not path.exists() or sha256_file(path) != row["attestation_sha256"]:
                stale.append(int(row["sequence"]))
        return stale

    def test_annotating_an_attestation_a_ledger_row_already_pins_is_refused(self) -> None:
        """The exploit as performed: the plain run first, then the annotating one."""
        self.rewind()
        first = self.backfill()
        self.assertEqual(first.returncode, 0, msg=first.stderr)
        self.assertEqual(self.stale_rows(), [], "the first run must leave a consistent record")

        second = self.backfill("--annotate-attestations")

        self.assertEqual(second.returncode, 2, msg=second.stdout)
        self.assertIn("is already pinned by ledger sequence", second.stderr)
        self.assertIn("the record lying about itself", second.stderr)
        self.assertEqual(self.stale_rows(), [],
                         "a refused run must not have edited an attestation on its way out")

    def test_either_invocation_order_produces_the_same_attestation_digests(self) -> None:
        """Positive control: the refusal above is order-dependence being reported, not the
        back-fill having become unusable. The safe order still completes, twice, and every
        digest it writes still resolves."""
        self.rewind()
        first = self.backfill("--annotate-attestations")
        self.assertEqual(first.returncode, 0, msg=first.stderr)
        second = self.backfill()
        self.assertEqual(second.returncode, 0, msg=second.stderr)
        self.assertIn("0 ledger rows appended", second.stdout)
        self.assertEqual(self.stale_rows(), [])

    def test_no_committed_ledger_row_pins_a_stale_attestation_digest(self) -> None:
        """Positive control over the real repository, not the sandbox: whatever order the
        back-fill was actually invoked in when this harvest was built, the committed record
        must not contain the defect the gate above describes."""
        harvest = REPO / "docs/eval/retrieval/harvests" / HARVEST
        checked = 0
        for line in (harvest / "access-ledger.jsonl").read_text(encoding="utf-8").splitlines():
            if not line.strip():
                continue
            row = json.loads(line)
            if not row.get("attestation") or not row.get("attestation_sha256"):
                continue
            checked += 1
            path = REPO / str(row["attestation"])
            self.assertTrue(path.exists(), f"sequence {row['sequence']} pins a missing file")
            self.assertEqual(sha256_file(path), row["attestation_sha256"],
                             f"sequence {row['sequence']} pins a stale digest for {path}")
        self.assertEqual(checked, 12, "all twelve answerability attestations must be pinned")


class TheGraphQLOperationIsBoundToTheDeclaredSelectionSet(unittest.TestCase):
    """M-f, and N5 in round 2. `SELECTION_SET` is what the access ledger certifies. Round 1
    bound the executed `issues(...)` body to it - but read only that one selection body and
    ignored the rest of the document, so a sibling `issue(number: 1) { labels { ... } }` under
    the same `repository` passed the check while transporting labels. Round 3 then found the
    parser walking past every argument list without comparing it, so
    `repository(owner: "kubernetes", name: "kubernetes")` passed a check that had already
    approved every field name in the document. Arguments and variable bindings are compared
    now, so the whole operation AND its target are validated."""

    @classmethod
    def setUpClass(cls) -> None:
        sys.path.insert(0, str(REPO / "scripts" / "eval"))
        import fetch_cobra_issue_text  # noqa: E402

        cls.mod = fetch_cobra_issue_text

    def refuses(self, query: str) -> str:
        with self.assertRaises(SystemExit) as caught:
            self.mod.assert_query_is_the_selection_set(query)
        return str(caught.exception)

    def test_the_shipped_query_matches_the_declared_selection_set(self) -> None:
        """Positive control."""
        body = self.mod.assert_query_is_the_selection_set()
        self.assertEqual(body, self.mod.normalise(self.mod.SELECTION_SET + " " + self.mod.PAGE_INFO))

    def test_a_query_that_asks_for_labels_is_refused(self) -> None:
        overfetching = self.mod.QUERY.replace(
            "nodes {", "labels(first: 10) { nodes { name } }\n      nodes {", 1)
        self.assertIn("does not match the declared selection set", self.refuses(overfetching))

    def test_a_query_that_asks_for_one_extra_field_inside_nodes_is_refused(self) -> None:
        overfetching = self.mod.QUERY.replace("nodes { number", "nodes { number state", 1)
        self.assertIn("does not match the declared selection set", self.refuses(overfetching))

    def test_a_query_that_drops_a_permitted_field_is_also_refused(self) -> None:
        """The binding is equality, not containment: silently losing `createdAt` is what made
        the superseded harvest's population cutoff uncheckable."""
        narrower = self.mod.QUERY.replace(" createdAt", "", 1)
        self.assertIn("does not match the declared selection set", self.refuses(narrower))

    def test_a_sibling_selection_transporting_labels_is_refused(self) -> None:
        """N5, the exploit as performed. Nothing inside `issues(...)` changes, so a check that
        reads only that body sees the declared selection set and approves - while the response
        carries every label of issue 1."""
        smuggled = self.mod.QUERY.replace(
            "  repository(owner: $owner, name: $name) {",
            "  repository(owner: $owner, name: $name) {\n"
            "    issue(number: 1) { labels(first: 100) { nodes { name } } }", 1)
        message = self.refuses(smuggled)
        self.assertIn("repository selects 2 fields (issue, issues)", message)

    def test_a_sibling_selection_at_the_operation_root_is_refused(self) -> None:
        smuggled = self.mod.QUERY.replace(
            "query($owner: String!, $name: String!, $cursor: String) {",
            "query($owner: String!, $name: String!, $cursor: String) {\n  viewer { login }", 1)
        self.assertIn("the operation root selects 2 fields", self.refuses(smuggled))

    def test_an_aliased_field_is_refused(self) -> None:
        """An alias renames the field on the wire, so a check that compares field names is
        looking at the alias and not at what is being requested."""
        aliased = self.mod.QUERY.replace("    issues(", "    alias: issues(", 1)
        self.assertIn("an alias hides the field actually being requested", self.refuses(aliased))

    def test_a_fragment_smuggling_a_field_is_refused(self) -> None:
        spread = self.mod.QUERY.replace(
            "nodes { number createdAt title body author { login } }", "nodes { ...F }", 1)
        spread += ("\nfragment F on Issue { number createdAt title body author { login } "
                   "labels(first: 10) { nodes { name } } }")
        self.assertIn("a fragment definition can carry any field list", self.refuses(spread))

    def test_an_inline_fragment_is_refused(self) -> None:
        inline = self.mod.QUERY.replace(
            "nodes { number", "nodes { ... on Issue { labels(first: 1) { nodes { name } } } number", 1)
        self.assertIn("fragment spread or inline fragment", self.refuses(inline))

    def test_a_second_operation_in_the_document_is_refused(self) -> None:
        two = self.mod.QUERY + (
            "\nquery Extra($owner: String!, $name: String!) { repository(owner: $owner, "
            "name: $name) { issue(number: 1) { labels(first: 1) { nodes { name } } } } }")
        self.assertIn("more than one definition in this document", self.refuses(two))

    def test_a_directive_is_refused(self) -> None:
        directed = self.mod.QUERY.replace("nodes {", "nodes @include(if: true) {", 1)
        self.assertIn("a directive can change what is returned", self.refuses(directed))

    def test_a_mutation_is_refused(self) -> None:
        self.assertIn("this is a mutation, not a query",
                      self.refuses(self.mod.QUERY.replace("query(", "mutation(", 1)))

    def test_a_changed_repository_owner_is_refused(self) -> None:
        """Round 3, the exploit as performed. `skip_balanced` walked past every argument list
        without comparing it, so every field name in the document was the declared one and the
        fetch would have written kubernetes/kubernetes issue bodies into a file whose metadata
        says spf13/cobra."""
        elsewhere = self.mod.QUERY.replace("owner: $owner", 'owner: "kubernetes"', 1)
        self.assertIn("repository's arguments are not the declared ones", self.refuses(elsewhere))

    def test_a_changed_repository_name_is_refused(self) -> None:
        elsewhere = self.mod.QUERY.replace("name: $name", 'name: "kubernetes"', 1)
        self.assertIn("repository's arguments are not the declared ones", self.refuses(elsewhere))

    def test_an_added_argument_is_refused(self) -> None:
        """Equality, not containment: an argument nobody declared changes what comes back."""
        added = self.mod.QUERY.replace("first: 50", "first: 50\n      filterBy: {}", 1)
        self.assertIn("issues's arguments are not the declared ones", self.refuses(added))

    def test_a_variable_default_naming_another_repository_is_refused(self) -> None:
        """A default value is an argument by another route: it decides what is fetched when
        the binding is absent, and it lives in the operation's variable definitions rather
        than in any field's argument list."""
        defaulted = self.mod.QUERY.replace(
            "$owner: String!", '$owner: String = "kubernetes"', 1)
        self.assertIn("variable definitions are not the declared ones", self.refuses(defaulted))

    def test_bindings_naming_another_repository_are_refused(self) -> None:
        """The other half of pinning the target. The operation asks for `repository(owner:
        $owner, name: $name)`, so an operation that is exactly the declared one still reads
        whatever repository the bindings sent alongside it name."""
        with self.assertRaises(SystemExit) as caught:
            self.mod.assert_variables_pin_the_repository(
                {"owner": "kubernetes", "name": "kubernetes"})
        self.assertIn("the repository variables are not the declared ones", str(caught.exception))

    def test_a_whitespace_reformatted_query_is_still_accepted(self) -> None:
        """Positive control for the argument checks: whitespace and commas are insignificant
        to GraphQL and are insignificant here, so the gate refuses changes to what is asked
        for and not changes to how it is typed. Without this, an argument check that always
        refused would look exactly like one that works."""
        reformatted = " ".join(self.mod.QUERY.split())
        self.assertNotEqual(reformatted, self.mod.QUERY)
        body = self.mod.assert_query_is_the_selection_set(reformatted)
        self.assertEqual(body, self.mod.normalise(self.mod.SELECTION_SET + " " + self.mod.PAGE_INFO))
        self.mod.assert_variables_pin_the_repository(self.mod.VARIABLES)


def declared() -> tuple[list[str], list[str], list[str]]:
    """Return (all case ids, refusal case ids, positive-control case ids).

    The inventory is derived from the module rather than written down twice, and the
    `POSITIVE_CONTROLS` names are checked against it, so a renamed or deleted case is a hard
    error here instead of a number in a docstring quietly going wrong.
    """
    loader = unittest.TestLoader()
    suite = loader.loadTestsFromModule(sys.modules[__name__])
    ids: list[str] = []
    stack = [suite]
    while stack:
        item = stack.pop()
        if isinstance(item, unittest.TestSuite):
            stack.extend(list(item))
        else:
            ids.append(item.id())
    ids.sort()
    names = {case.rsplit(".", 1)[-1] for case in ids}
    unknown = sorted(POSITIVE_CONTROLS - names)
    if unknown:
        raise SystemExit(f"POSITIVE_CONTROLS names cases that do not exist: {unknown}")
    controls = [case for case in ids if case.rsplit(".", 1)[-1] in POSITIVE_CONTROLS]
    refusals = [case for case in ids if case.rsplit(".", 1)[-1] not in POSITIVE_CONTROLS]
    return ids, refusals, controls


def main() -> int:
    ids, refusals, controls = declared()
    loader = unittest.TestLoader()
    suite = loader.loadTestsFromModule(sys.modules[__name__])
    result = unittest.TextTestRunner(verbosity=2).run(suite)

    skipped = sorted(case.id() for case, _ in result.skipped)
    failed = sorted(case.id() for case, _ in result.failures + result.errors)
    ran_ok = [case for case in ids if case not in set(skipped) and case not in set(failed)]
    ok_controls = [case for case in ran_ok if case in set(controls)]

    # A single machine-readable line, so the Go wrapper asserts on a contract rather than on
    # the shape of unittest's console output. A skipped case is reported here by name: the
    # wrapper's job is to refuse to call a partial run a pass.
    print(f"SW279-GATES declared={len(ids)} refusals={len(refusals)} "
          f"positive_controls={len(controls)} ran={result.testsRun} ok={len(ran_ok)} "
          f"skipped={len(skipped)} failed={len(failed)} "
          f"refusals_ok={len(ran_ok) - len(ok_controls)} positive_controls_ok={len(ok_controls)}")
    for case in skipped:
        print(f"SW279-GATES-SKIPPED {case}")
    return 0 if result.wasSuccessful() else 1


if __name__ == "__main__":
    raise SystemExit(main())
