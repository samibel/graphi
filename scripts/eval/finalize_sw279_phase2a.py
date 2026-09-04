#!/usr/bin/env python3
"""Materialize the completed SW-279 Phase 2a candidate ledger and Q seal.

The first cut of this script carried the 139-row semantic verdict inline, as a dict literal
edited into the source by the same actor that had held the raw API envelopes. The verdict
now arrives as a file written by an isolated classifier that never touched the network, and
this script only validates and materialises it. Every check below exists so that a
malformed or partial classification fails loudly instead of silently shrinking the ledger.
"""

from __future__ import annotations

import hashlib
import json
import sys
from collections import Counter
from datetime import datetime, timezone
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent))
import _access_ledger  # noqa: E402


ROOT = Path("docs/eval/retrieval/harvests/sw-279-phase-2a2")
MANIFEST_SHA256 = "2c35bf714abc32bc9074dfe75df7f5f36ba4d19958de9ca2eea596b353c74de4"
RULE_COMMIT = "a0a13a757c66e8d4f0747d4a68955fe95d072573"
RULE_SHA256 = "d9aea9863501d3d2827aa191275f689fc8afeda30ecb8dcbbb379d7339d85a2c"
SELECTOR = "Claude Opus 5 (SW-279 Phase 2a isolated semantic classifier)"
SELECTOR_ROLE = "labelled solo substitute for an independent selector"
MATERIALIZER = "Claude Opus 5 (SW-279 Phase 2 orchestrator)"

LEGAL_CLAUSES = {"C1", "C2", "C3", "C4", "C5", "E1", "E2", "E3", "E4", "E5"}
CANDIDATE_CLAUSES = ["C1", "C2", "C3", "C4", "C5"]


def sha256(data: bytes) -> str:
    return hashlib.sha256(data).hexdigest()


def now() -> str:
    return datetime.now(timezone.utc).isoformat(timespec="microseconds").replace("+00:00", "Z")


def read_jsonl(path: Path) -> list[dict[str, object]]:
    return [json.loads(line) for line in path.read_text(encoding="utf-8").splitlines()]


def jsonl_bytes(rows: list[dict[str, object]]) -> bytes:
    return b"".join(
        (json.dumps(row, ensure_ascii=False, separators=(",", ":")) + "\n").encode("utf-8")
        for row in rows
    )


def main() -> None:
    semantic_path = ROOT / "semantic-review.jsonl"
    classification_path = ROOT / "semantic-classification.jsonl"
    attestation_path = ROOT / "semantic-classifier-attestation.json"
    mechanical_path = ROOT / "mechanical-candidate-ledger.jsonl"
    archive_path = ROOT / "issue-text.jsonl"
    decision_path = ROOT / "semantic-decisions.jsonl"
    ledger_path = ROOT / "candidate-ledger.jsonl"
    questions_path = ROOT / "candidate-questions.jsonl"
    seal_path = ROOT / "candidate-question-seal.json"
    summary_path = ROOT / "phase-2a-summary.json"
    access_path = ROOT / "access-ledger.jsonl"
    outputs = (decision_path, ledger_path, questions_path, seal_path, summary_path)
    if any(path.exists() for path in outputs):
        raise SystemExit("refusing to overwrite a finalized Phase 2a artifact")
    if not classification_path.exists():
        raise SystemExit(f"no semantic classification at {classification_path}")
    if not attestation_path.exists():
        raise SystemExit(f"no classifier attestation at {attestation_path}")

    semantic = read_jsonl(semantic_path)
    classification = read_jsonl(classification_path)

    # The classification must cover the syntactically eligible rows exactly: same numbers,
    # same order, no additions, no omissions. Section 9 has no state for a dropped row.
    eligible_numbers = [int(row["issue_number"]) for row in semantic]
    classified_numbers = [int(row["issue_number"]) for row in classification]
    if classified_numbers != eligible_numbers:
        missing = sorted(set(eligible_numbers) - set(classified_numbers))
        extra = sorted(set(classified_numbers) - set(eligible_numbers))
        raise SystemExit(
            "classification does not match the syntactically eligible set one-for-one and in order; "
            f"missing={missing} unexpected={extra}"
        )

    decided_at = now()
    semantic_decisions: list[dict[str, object]] = []
    decisions_by_number: dict[int, dict[str, object]] = {}
    reject_count = 0
    boundary_cases: list[int] = []
    for row in classification:
        number = int(row["issue_number"])
        verdict = str(row["verdict"])
        clauses = [str(clause) for clause in row["deciding_clauses"]]
        rationale = str(row["rationale"])
        if verdict not in {"candidate", "reject"}:
            raise SystemExit(f"issue {number}: illegal verdict {verdict!r}")
        illegal = [clause for clause in clauses if clause not in LEGAL_CLAUSES]
        if illegal:
            raise SystemExit(f"issue {number}: illegal deciding clauses {illegal}")
        if not clauses:
            raise SystemExit(f"issue {number}: a verdict with no deciding clause is a Section 3 violation")
        if not rationale.strip():
            raise SystemExit(f"issue {number}: empty rationale")
        if row.get("boundary_case"):
            boundary_cases.append(number)
        if verdict == "candidate":
            if clauses != CANDIDATE_CLAUSES:
                raise SystemExit(f"issue {number}: a candidate must record {CANDIDATE_CLAUSES}, got {clauses}")
            state = "candidate"
        else:
            reject_count += 1
            state = "reject:not_candidate"
        decision = {
            "issue_number": number,
            "selector": SELECTOR,
            "selector_role": SELECTOR_ROLE,
            "verdict_at_utc": decided_at,
            "state": state,
            "verdict": verdict,
            "primary_deciding_clause": clauses[0],
            "deciding_clauses": clauses,
            "rationale": rationale,
            "boundary_case": bool(row.get("boundary_case", False)),
        }
        semantic_decisions.append(decision)
        decisions_by_number[number] = decision

    decision_path.write_bytes(jsonl_bytes(semantic_decisions))

    mechanical = read_jsonl(mechanical_path)
    final_rows: list[dict[str, object]] = []
    questions: list[dict[str, object]] = []
    for row in mechanical:
        number = int(row["issue_number"])
        if row["state"] == "pending_semantic_classification":
            decision = decisions_by_number[number]
            row.update({key: value for key, value in decision.items() if key != "issue_number"})
        else:
            clauses = list(row["deciding_clauses"])
            row["primary_deciding_clause"] = clauses[0]
        final_rows.append(row)
        if row["state"] == "candidate":
            questions.append({
                "issue_number": number,
                "provenance": f"github:spf13/cobra#{number}",
                "population_manifest_sha256": MANIFEST_SHA256,
                "title_sha256": row["title_sha256"],
                "T": row["T"],
                "Q": row["Q"],
                "transformation": "dataset-v2-inclusion-rule.md Section 2",
            })

    manifest_numbers = [int(line) for line in (ROOT / "issue-numbers.txt").read_text().splitlines()]
    if [int(row["issue_number"]) for row in final_rows] != manifest_numbers:
        raise SystemExit("final ledger does not match manifest one-for-one and in order")
    if len(decisions_by_number) != len(semantic):
        raise SystemExit("semantic decision count mismatch")
    if len(questions) + reject_count != len(semantic):
        raise SystemExit("semantic candidate/reject partition mismatch")

    ledger_bytes = jsonl_bytes(final_rows)
    question_bytes = jsonl_bytes(questions)
    ledger_path.write_bytes(ledger_bytes)
    questions_path.write_bytes(question_bytes)
    ledger_sha = sha256(ledger_bytes)
    questions_sha = sha256(question_bytes)
    archive_sha = sha256(archive_path.read_bytes())
    decision_sha = sha256(decision_path.read_bytes())

    primary_counts: Counter[str] = Counter()
    clause_counts: Counter[str] = Counter()
    state_counts: Counter[str] = Counter(str(row["state"]) for row in final_rows)
    for row in final_rows:
        if row["state"] == "reject:not_candidate":
            primary_counts[str(row["primary_deciding_clause"])] += 1
            clause_counts.update(str(clause) for clause in row["deciding_clauses"])

    # The selector attestation is written by the isolated classifier itself, in its own
    # words. This script does not compose one on its behalf; it only records its digest.
    attestation_sha = sha256(attestation_path.read_bytes())

    sealed_at = now()
    seal = {
        "schema": "sw-279-candidate-question-seal/v1",
        "sealed_at_utc": sealed_at,
        "sealed_by": SELECTOR,
        "phase_1_rule_commit": RULE_COMMIT,
        "phase_1_rule_sha256": RULE_SHA256,
        "population_manifest_sha256": MANIFEST_SHA256,
        "candidate_ledger_file": ledger_path.name,
        "candidate_ledger_sha256": ledger_sha,
        "candidate_questions_file": questions_path.name,
        "candidate_questions_sha256": questions_sha,
        "candidate_count": len(questions),
        "scope": "T and mechanically derived Q only; no stratum, rubric, family, split, answerability, judgement, or dataset assignment",
    }
    seal_path.write_text(json.dumps(seal, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")

    summary = {
        "schema": "sw-279-phase-2a-summary/v1",
        "population_size": len(final_rows),
        "population_manifest_sha256": MANIFEST_SHA256,
        "candidate_count": len(questions),
        "reject_not_candidate_count": state_counts["reject:not_candidate"],
        "unresolved_count": state_counts["unresolved"],
        "syntactically_eligible_count": len(semantic),
        "mechanical_reject_count": len(final_rows) - len(semantic),
        "semantic_reject_count": reject_count,
        "semantic_boundary_case_count": len(boundary_cases),
        "semantic_boundary_case_issues": boundary_cases,
        "rejects_by_primary_clause_exclusive": dict(sorted(primary_counts.items())),
        "reject_clause_mentions_nonexclusive": dict(sorted(clause_counts.items())),
        "issue_text_archive_sha256": archive_sha,
        "semantic_decisions_sha256": decision_sha,
        "candidate_ledger_sha256": ledger_sha,
        "candidate_questions_sha256": questions_sha,
        "sealed_at_utc": sealed_at,
        "semantic_classifier": SELECTOR,
        "semantic_classifier_role": SELECTOR_ROLE,
        "semantic_classifier_attestation_sha256": attestation_sha,
    }
    summary_path.write_text(json.dumps(summary, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")

    _access_ledger.append(
        access_path,
        actor=MATERIALIZER,
        command_tool_class="local materialisation of the isolated classifier's verdict into the complete-population ledger",
        input_artifact=classification_path.as_posix(),
        input_sha256=sha256(classification_path.read_bytes()),
        output_artifact=ledger_path.as_posix(),
        output_sha256=ledger_sha,
        detail=(
            "Validated the 139-row semantic classification against the syntactically eligible set "
            "one-for-one and in order, then materialised the complete-population candidate ledger "
            "and the sealed T/Q artifact. No network, source, family, split, dataset or retrieval "
            "access. The semantic verdicts were made by " + SELECTOR + " (" + SELECTOR_ROLE +
            "), whose attestation is semantic-classifier-attestation.json, sha256 " +
            attestation_sha + "."
        ),
    )

    print(json.dumps(summary, ensure_ascii=False, indent=2))


if __name__ == "__main__":
    main()
