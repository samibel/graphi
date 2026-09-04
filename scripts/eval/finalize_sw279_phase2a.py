#!/usr/bin/env python3
"""Materialize the completed SW-279 Phase 2a candidate ledger and Q seal."""

from __future__ import annotations

import hashlib
import json
from collections import Counter
from datetime import datetime, timezone
from pathlib import Path


ROOT = Path("docs/eval/retrieval/harvests/sw-279-phase-2a")
MANIFEST_SHA256 = "b9f712af1bea40bbde437dee649a35346de023891839e8ae148138a94a8c4a17"
RULE_COMMIT = "a0a13a757c66e8d4f0747d4a68955fe95d072573"
RULE_SHA256 = "d9aea9863501d3d2827aa191275f689fc8afeda30ecb8dcbbb379d7339d85a2c"
SELECTOR = "Codex /root (SW-279 Phase 2a selector)"

# Every syntactically eligible issue not named here is an explicit candidate.
# The union is validated against semantic-review.jsonl before anything is written.
REJECTIONS: dict[int, list[str]] = {
    124: ["E2"],
    206: ["E1"],
    243: ["E1", "E2"],
    298: ["E1"],
    357: ["E1", "C3"],
    489: ["E4"],
    566: ["E2"],
    587: ["E2"],
    613: ["E1", "E4"],
    678: ["E1", "E4"],
    689: ["C2", "E4"],
    692: ["E2"],
    699: ["E5", "E2"],
    710: ["E4"],
    724: ["E1", "E4"],
    725: ["E1", "E4"],
    829: ["E1"],
    835: ["C3"],
    852: ["E4"],
    910: ["E4"],
    943: ["E1", "E4"],
    1025: ["E1", "E4"],
    1098: ["E1", "E4"],
    1102: ["C2", "E4"],
    1120: ["E1"],
    1141: ["C2", "E4"],
    1151: ["C3", "E4"],
    1167: ["E1", "C3"],
    1168: ["E4"],
    1186: ["E2", "E4"],
    1221: ["E1", "E4"],
    1236: ["E4"],
    1244: ["E5"],
    1289: ["E4"],
    1299: ["E1"],
    1335: ["C2", "E4"],
    1336: ["C2", "E4"],
    1381: ["E2"],
    1395: ["E1", "E4"],
    1416: ["E4"],
    1466: ["C2", "E4"],
    1480: ["E1"],
    1521: ["E2"],
    1531: ["C3"],
    1573: ["C2", "C3", "E4"],
    1628: ["C1", "C3"],
    1631: ["C3", "E1"],
    1651: ["E1"],
    1739: ["E2"],
    1749: ["C2", "E4"],
    1798: ["C2", "E4"],
    1811: ["C2"],
    1834: ["E1", "E4"],
    1859: ["E3", "E5"],
    1861: ["E1", "E4"],
    1894: ["C3"],
    1915: ["E1", "E4", "C3"],
    1923: ["E4"],
    1924: ["E4"],
    1947: ["E1", "E4"],
    1962: ["E1"],
    2007: ["E1", "E4"],
    2014: ["E1", "E4"],
    2068: ["E2"],
    2138: ["E1", "E4"],
    2141: ["E1", "E4", "C3"],
    2160: ["E4"],
    2184: ["E4"],
    2243: ["E2"],
    2249: ["E1", "E4"],
    2264: ["C2", "E4"],
    2270: ["C2"],
    2282: ["E2"],
}

REJECT_RATIONALES = {
    "C1": "The opening text asks for subjective application design rather than an explanation of an existing Cobra fact, so C1 does not hold.",
    "C2": "The opening text concerns Go, a shell, a terminal, or the reporter's application rather than Cobra's implementation, API, configuration, or repository documentation, so C2 does not hold.",
    "C3": "The derived Q needs an omitted screenshot, linked example, or undefined referenced material to determine the requested fact, so C3 does not hold.",
    "E1": "The opening text asserts an observed failure or actual-versus-expected discrepancy and asks for diagnosis or correction, so E1 applies.",
    "E2": "The opening text asks to add or change Cobra behavior, API, output, or documentation, so E2 applies.",
    "E3": "The opening text asks for a release action or version-policy outcome, so E3 applies.",
    "E4": "Answering the opening request requires the reporter's command tree, callbacks, application code, environment, shell, filesystem, or runtime state, so E4 applies.",
    "E5": "The opening text is project administration or a proposal rather than an information request about existing Cobra code, so E5 applies.",
}


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


def reject_rationale(clauses: list[str]) -> str:
    return REJECT_RATIONALES[clauses[0]]


def main() -> None:
    semantic_path = ROOT / "semantic-review.jsonl"
    mechanical_path = ROOT / "mechanical-candidate-ledger.jsonl"
    archive_path = ROOT / "issue-text.jsonl"
    decision_path = ROOT / "semantic-decisions.jsonl"
    ledger_path = ROOT / "candidate-ledger.jsonl"
    questions_path = ROOT / "candidate-questions.jsonl"
    seal_path = ROOT / "candidate-question-seal.json"
    summary_path = ROOT / "phase-2a-summary.json"
    attestation_path = ROOT / "selector-attestation.json"
    access_path = ROOT / "access-ledger.jsonl"
    outputs = (decision_path, ledger_path, questions_path, seal_path, summary_path, attestation_path)
    if any(path.exists() for path in outputs):
        raise SystemExit("refusing to overwrite a finalized Phase 2a artifact")

    semantic = read_jsonl(semantic_path)
    eligible_numbers = {int(row["issue_number"]) for row in semantic}
    unknown_rejections = set(REJECTIONS) - eligible_numbers
    if unknown_rejections:
        raise SystemExit(f"rejection decisions outside semantic review: {sorted(unknown_rejections)}")

    decided_at = now()
    semantic_decisions: list[dict[str, object]] = []
    decisions_by_number: dict[int, dict[str, object]] = {}
    for row in semantic:
        number = int(row["issue_number"])
        clauses = REJECTIONS.get(number)
        if clauses is None:
            decision = {
                "issue_number": number,
                "selector": SELECTOR,
                "verdict_at_utc": decided_at,
                "state": "candidate",
                "verdict": "candidate",
                "primary_deciding_clause": "C1-C5",
                "deciding_clauses": ["C1", "C2", "C3", "C4", "C5"],
                "rationale": "The title and opening body ask one standalone English question about existing Cobra behavior or API whose answer is independent of the reporter's program state.",
            }
        else:
            decision = {
                "issue_number": number,
                "selector": SELECTOR,
                "verdict_at_utc": decided_at,
                "state": "reject:not_candidate",
                "verdict": "reject",
                "primary_deciding_clause": clauses[0],
                "deciding_clauses": clauses,
                "rationale": reject_rationale(clauses),
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
    if len(questions) + len(REJECTIONS) != len(semantic):
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
        "semantic_reject_count": len(REJECTIONS),
        "rejects_by_primary_clause_exclusive": dict(sorted(primary_counts.items())),
        "reject_clause_mentions_nonexclusive": dict(sorted(clause_counts.items())),
        "issue_text_archive_sha256": archive_sha,
        "semantic_decisions_sha256": decision_sha,
        "candidate_ledger_sha256": ledger_sha,
        "candidate_questions_sha256": questions_sha,
        "sealed_at_utc": sealed_at,
    }
    summary_path.write_text(json.dumps(summary, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")

    attestation = {
        "schema": "sw-279-phase-2a-selector-attestation/v1",
        "actor": SELECTOR,
        "attested_at_utc": now(),
        "input_population_manifest_sha256": MANIFEST_SHA256,
        "output_candidate_ledger_sha256": ledger_sha,
        "statements": [
            "I used only issue number, author, creation time, title, and opening body for candidate classification.",
            "I did not inspect any GitHub comment, maintainer reply, reaction, label, closing event, attachment target, linked pull request, or external-link target for selection.",
            "I did not inspect the pinned Cobra source or perform candidate-directed source search.",
            "I did not run or inspect candidate or baseline retrieval output, ranks, scores, bundles, metrics, traces, or saved hit lists.",
            "I assigned no family, stratum, rubric, split, answerability verdict, judgement, or dataset row.",
            "I did not edit docs/eval/retrieval-targets.json or any dataset file.",
        ],
    }
    attestation_path.write_text(json.dumps(attestation, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")

    prior = read_jsonl(access_path)
    if [row.get("sequence") for row in prior] != [1, 2]:
        raise SystemExit("unexpected access-ledger state before classification event")
    event = {
        "sequence": 3,
        "actor": SELECTOR,
        "timestamp_utc": now(),
        "command_tool_class": "local deterministic Section 2 transform plus allowed-text C1-C5/E1-E5 human classification",
        "input_artifact": archive_path.as_posix(),
        "input_sha256": archive_sha,
        "output_artifact": ledger_path.as_posix(),
        "output_sha256": ledger_sha,
        "detail": "Complete-population candidate ledger and sealed T/Q artifact; no Cobra source, family, split, dataset, or retrieval access.",
    }
    with access_path.open("a", encoding="utf-8") as handle:
        handle.write(json.dumps(event, ensure_ascii=False, separators=(",", ":")) + "\n")

    print(json.dumps(summary, ensure_ascii=False, indent=2))


if __name__ == "__main__":
    main()
