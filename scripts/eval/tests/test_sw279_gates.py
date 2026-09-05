#!/usr/bin/env python3
"""Prove that SW-279's Phase 2 gates refuse when they should, by breaking things.

A gate that has only ever been run on data it passes is not evidence of anything - that was
finding M4 of the round-1 review, where the "no actor reviewed its own judgements" check
reported PASS on 23 rows it structurally could not fail. So every check here works the same
way: build a throwaway copy of the parts of this repository the SW-279 scripts read, break
exactly one thing, and assert the script refuses with a message naming what is wrong. Each
refusal test is paired with a positive control wherever a positive control is cheap, because
a gate that always fails is no better than one that never does.

Run directly (`python3 scripts/eval/tests/test_sw279_gates.py`) or through
`go test ./internal/eval/retrieval -run TestSW279_ScriptGates`.

Two of these need a read-only clone of spf13/cobra at the pin, because the finalizer verifies
the checkout HEAD before it will read a span. They SKIP visibly when it is absent.
"""

from __future__ import annotations

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
        subprocess.run(["git", "-c", "user.name=sw279-gate-test",
                        "-c", "user.email=sw279@example.invalid",
                        "commit", "-q", "-m", "sandbox"],
                       cwd=self.dir, check=True,
                       stdout=subprocess.DEVNULL, stderr=subprocess.STDOUT)

    def close(self) -> None:
        shutil.rmtree(self.dir, ignore_errors=True)

    def path(self, *parts: str) -> Path:
        return self.dir.joinpath(*parts)

    def harvest(self, *parts: str) -> Path:
        return self.path("docs/eval/retrieval/harvests", HARVEST, *parts)

    def review(self, *parts: str) -> Path:
        return self.path("docs/eval/retrieval/harvests", REVIEW, *parts)

    def run(self, script: str, *args: str) -> subprocess.CompletedProcess:
        return subprocess.run(
            [sys.executable, f"scripts/eval/{script}", *args],
            cwd=self.dir, stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True,
        )

    def rewrite_jsonl(self, path: Path, edit) -> None:
        """Apply `edit(row) -> row | None` to every row; a None result drops the row."""
        rows = [json.loads(line) for line in path.read_text(encoding="utf-8").splitlines() if line.strip()]
        kept = [edited for edited in (edit(row) for row in rows) if edited is not None]
        path.write_text(
            "".join(json.dumps(row, ensure_ascii=False, separators=(",", ":")) + "\n" for row in kept),
            encoding="utf-8")


class SandboxTest(unittest.TestCase):
    def setUp(self) -> None:
        self.box = Sandbox()
        self.addCleanup(self.box.close)


class UnresolvedRowsBlockAndAreNeverConverted(SandboxTest):
    """B1. Section 4: an unresolved candidate "is not silently treated as a reject: it blocks
    completion of Phase 2 and is reported". Section 8 lists "reinterpret an unresolved row as
    a reject" among the forbidden acts. An earlier finalizer did exactly that whenever the
    independent reviewer supplied a D-clause. If that conversion is ever reinstated, this test
    fails: the run would return 0 and the row would read `reject:not_answerable`."""

    def setUp(self) -> None:
        super().setUp()
        self.cobra = cobra_root()
        if self.cobra is None:
            self.skipTest("SKIP: no spf13/cobra clone at the pin; the finalizer cannot run")

    def finalize(self) -> subprocess.CompletedProcess:
        return self.box.run("finalize_sw279_answerability.py",
                            "--harvest", HARVEST, "--cobra-root", str(self.cobra))

    def test_an_unresolved_row_blocks_completion_and_keeps_its_verdict(self) -> None:
        # Restore the historical situation exactly: issue 1780 annotated `unresolved`, its
        # independent reviewer offering a positive D3 finding, and no re-annotation pass.
        for name in ("annotations-6.jsonl", "reviews-6.jsonl", "reannotation-plan.json",
                     "annotator-A6-attestation.json", "reviewer-R6-attestation.json"):
            self.box.harvest("answerability", name).unlink()
        self.box.harvest("answerability-ledger.jsonl").unlink()
        self.box.harvest("phase-2-outcome.json").unlink()

        result = self.finalize()

        self.assertEqual(result.returncode, 3, msg=result.stderr)
        self.assertIn("BLOCKED", result.stderr)
        self.assertIn("1780", result.stderr)
        self.assertFalse(self.box.harvest("answerability-ledger.jsonl").exists(),
                         "a blocked run must not claim the authoritative ledger name")
        blocked = self.box.harvest("answerability-ledger-blocked.jsonl")
        self.assertTrue(blocked.exists(), "a blocked run must still report, under a -blocked name")

        rows = {json.loads(line)["issue_number"]: json.loads(line)
                for line in blocked.read_text(encoding="utf-8").splitlines() if line.strip()}
        row = rows[1780]
        self.assertEqual(row["verdict"], "unresolved")
        self.assertEqual(row["state"], "unresolved")
        # The reviewer's D-clause is published on the row, and applied to nothing.
        self.assertIsNone(row["disqualifier"])
        self.assertEqual(row["reviewer_verdict"], "not_answerable")
        self.assertIn("D3", str(row["reviewer_note"]))

    def test_the_completing_run_reproduces_the_committed_ledger_byte_for_byte(self) -> None:
        """Positive control. The gate above must not be firing because the finalizer is broken."""
        committed = (REPO / "docs/eval/retrieval/harvests" / HARVEST / "answerability-ledger.jsonl").read_bytes()
        self.box.harvest("answerability-ledger.jsonl").unlink()
        self.box.harvest("phase-2-outcome.json").unlink()

        result = self.finalize()

        self.assertEqual(result.returncode, 0, msg=result.stderr)
        self.assertEqual(self.box.harvest("answerability-ledger.jsonl").read_bytes(), committed)


class TheIndependenceGuardFiresOnRejectionRows(SandboxTest):
    """M-c. AC-8 requires the annotator recorded per judgement. Rejections carry no judgements,
    so inferring the annotator from them recorded a filename - and the guard that compares
    annotator against reviewer could never fire on the 23 rows where a rejection was decided.
    Identity now comes from the batch plan and each actor's own attestation."""

    def setUp(self) -> None:
        super().setUp()
        self.cobra = cobra_root()
        if self.cobra is None:
            self.skipTest("SKIP: no spf13/cobra clone at the pin; the finalizer cannot run")
        self.box.harvest("answerability-ledger.jsonl").unlink()
        self.box.harvest("phase-2-outcome.json").unlink()

    def finalize(self) -> subprocess.CompletedProcess:
        return self.box.run("finalize_sw279_answerability.py",
                            "--harvest", HARVEST, "--cobra-root", str(self.cobra))

    def rejection_rows_in_batch_4(self) -> list[int]:
        rows = [json.loads(line) for line
                in self.box.harvest("answerability", "annotations-4.jsonl").read_text(encoding="utf-8").splitlines()
                if line.strip()]
        return [int(row["issue_number"]) for row in rows
                if row["verdict"] == "not_answerable" and not row["judgements"]]

    def test_one_actor_annotating_and_reviewing_a_rejection_is_refused(self) -> None:
        rejections = self.rejection_rows_in_batch_4()
        self.assertTrue(rejections, "batch 4 must contain a rejection row with no judgements")

        # Wire R4 to be the same actor as A4 - in the attestation, so that the plan itself
        # says one actor did both jobs and the mis-pairing check has nothing to complain about.
        annotator = json.loads(
            self.box.harvest("answerability", "annotator-A4-attestation.json").read_text(encoding="utf-8"))["actor"]
        reviewer_path = self.box.harvest("answerability", "reviewer-R4-attestation.json")
        reviewer = json.loads(reviewer_path.read_text(encoding="utf-8"))
        reviewer["actor"] = annotator
        reviewer_path.write_text(json.dumps(reviewer, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")
        self.box.rewrite_jsonl(self.box.harvest("answerability", "reviews-4.jsonl"),
                               lambda row: {**row, "reviewer": annotator})

        result = self.finalize()

        self.assertEqual(result.returncode, 2, msg=result.stdout)
        self.assertIn("reviewer and annotator are the same actor", result.stderr)
        # The point of the finding: the guard now names the rejection rows too.
        for number in rejections:
            self.assertIn(f"issue {number}: reviewer and annotator are the same actor", result.stderr)

    def test_a_reviewer_the_plan_did_not_pair_with_this_annotator_is_refused(self) -> None:
        self.box.rewrite_jsonl(
            self.box.harvest("answerability", "reviews-4.jsonl"),
            lambda row: {**row, "reviewer": "Claude Opus 5 (SW-279 answerability reviewer R2)"})
        result = self.finalize()
        self.assertEqual(result.returncode, 2, msg=result.stdout)
        self.assertIn("but the plan pairs", result.stderr)

    def test_a_judgement_claiming_another_annotator_is_refused(self) -> None:
        def relabel(row: dict) -> dict:
            if row["judgements"]:
                row = {**row, "judgements": [{**j, "annotator": "someone else"} for j in row["judgements"]]}
            return row

        self.box.rewrite_jsonl(self.box.harvest("answerability", "annotations-4.jsonl"), relabel)
        result = self.finalize()
        self.assertEqual(result.returncode, 2, msg=result.stdout)
        self.assertIn("records annotator", result.stderr)

    def test_a_rejection_whose_note_cites_a_file_absent_at_the_pin_is_refused(self) -> None:
        """m7. Rejection evidence used to be unchecked prose."""
        def break_citation(row: dict) -> dict:
            if row["verdict"] == "not_answerable":
                return {**row, "note": "D4 established at not_a_real_file.go:1-3."}
            return row

        self.box.rewrite_jsonl(self.box.harvest("answerability", "annotations-4.jsonl"), break_citation)
        self.box.rewrite_jsonl(self.box.harvest("answerability", "reviews-4.jsonl"),
                               lambda row: {**row, "note": "agreed"})
        result = self.finalize()
        self.assertEqual(result.returncode, 2, msg=result.stdout)
        self.assertIn("no such tracked file at the pin", result.stderr)


class TheFamilyLedgerRefusesPartialCoverage(SandboxTest):
    """M-d. `answered_a`/`answered_b` were computed and never compared against the population,
    so removing a reviewer's ruling produced a ledger built from 133 of 134 answers and the
    script returned normally."""

    def build(self) -> subprocess.CompletedProcess:
        return self.box.run("build_sw279_family_ledger.py", "--harvest", HARVEST, "--review", REVIEW)

    def test_a_reviewer_that_skipped_one_query_is_refused(self) -> None:
        self.box.review("family-ledger.jsonl").unlink()
        path = self.box.review("family-reviewer-B-codex.txt")
        lines = path.read_text(encoding="utf-8").splitlines()
        dropped = next(i for i, line in enumerate(lines) if line.strip().startswith("q-"))
        del lines[dropped]
        path.write_text("\n".join(lines) + "\n", encoding="utf-8")

        result = self.build()

        self.assertEqual(result.returncode, 3, msg=result.stdout)
        self.assertIn("1 unanswered", result.stderr)
        self.assertIn("refusing to write a ledger from partial coverage", result.stderr)
        self.assertFalse(self.box.review("family-ledger.jsonl").exists())

    def test_full_coverage_reproduces_the_committed_ledger_byte_for_byte(self) -> None:
        """Positive control for the gate above."""
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
        """Positive control: the two refusals above are not the script simply refusing always."""
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


class TheGraphQLQueryIsBoundToTheDeclaredSelectionSet(unittest.TestCase):
    """M-f. `SELECTION_SET` is what the access ledger certifies; the executed query used to be
    a separate literal carrying its own copy of the field list. Adding `labels` to one without
    the other would have recreated the §1 transport violation this whole story exists to
    prevent, while the ledger went on saying "and nothing else"."""

    @classmethod
    def setUpClass(cls) -> None:
        sys.path.insert(0, str(REPO / "scripts" / "eval"))
        import fetch_cobra_issue_text  # noqa: E402

        cls.mod = fetch_cobra_issue_text

    def test_the_shipped_query_matches_the_declared_selection_set(self) -> None:
        body = self.mod.assert_query_is_the_selection_set()
        self.assertEqual(body, self.mod.normalise(self.mod.SELECTION_SET + " " + self.mod.PAGE_INFO))

    def test_a_query_that_asks_for_labels_is_refused(self) -> None:
        overfetching = self.mod.QUERY.replace(
            "nodes {", "labels(first: 10) { nodes { name } }\n      nodes {", 1)
        with self.assertRaises(SystemExit) as caught:
            self.mod.assert_query_is_the_selection_set(overfetching)
        self.assertIn("does not match the declared selection set", str(caught.exception))

    def test_a_query_that_asks_for_one_extra_field_inside_nodes_is_refused(self) -> None:
        overfetching = self.mod.QUERY.replace("nodes { number", "nodes { number state", 1)
        with self.assertRaises(SystemExit) as caught:
            self.mod.assert_query_is_the_selection_set(overfetching)
        self.assertIn("does not match the declared selection set", str(caught.exception))

    def test_a_query_that_drops_a_permitted_field_is_also_refused(self) -> None:
        """The binding is equality, not containment: silently losing `createdAt` is what made
        the superseded harvest's population cutoff uncheckable."""
        narrower = self.mod.QUERY.replace(" createdAt", "", 1)
        with self.assertRaises(SystemExit) as caught:
            self.mod.assert_query_is_the_selection_set(narrower)
        self.assertIn("does not match the declared selection set", str(caught.exception))


if __name__ == "__main__":
    unittest.main(verbosity=2)
