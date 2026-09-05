#!/usr/bin/env python3
"""Close the SW-279 Phase 2b access-ledger gap for the two blind family reviewers.

`projects/graphi/stories/SW-279/decision-holdout-dev-overlap.md` Q3 item 6: "Neither family
reviewer has a ledger row or an attestation. ... This gap must close before Phase 2 proceeds;
the family merge that drives this decision currently has no ledger entry."

Two honest limits are written into the artefacts rather than papered over:

1. **There is no access timestamp for either reviewer.** No session id, wall-clock, or
   transcript was retained. The ledger rows therefore carry `timestamp_utc` = the time this
   row was written, and an explicit `access_timestamp_utc: null` beside it, so no reader
   mistakes one for the other.

2. **Neither reviewer left a first-person attestation, and one cannot be manufactured for
   them.** Section 8 asks for "attestations from each actor". What this script writes is an
   *attestation of record*: a statement, by the orchestrator, of what the repository can and
   cannot evidence about each reviewer's conduct. It is labelled as such in its schema name
   and in its own text. The Section 8 gap is narrowed, not closed, and the Phase 2 report
   says so.

Every digest is recomputed from the files on disk; nothing is asserted from memory.
"""

from __future__ import annotations

import json
import subprocess
import sys
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent))
import _access_ledger  # noqa: E402


HARVESTS = Path("docs/eval/retrieval/harvests")
REVIEW = HARVESTS / "sw-279-phase-2b-family-review"
LEDGER = HARVESTS / "sw-279-phase-2a2" / "access-ledger.jsonl"
RECORDER = "Claude Opus 5 (SW-279 Phase 2 orchestrator)"

# Recorded in decision-holdout-dev-overlap.md. The brief's scratchpad path was rewritten for
# publication and a header note in the file records it, so the bytes the reviewers read and the
# bytes in the repository differ and both digests are carried.
BRIEF_SHA256_AS_DELIVERED = "f3abb01582e933a0762b084d95d4f80a90e381cc24dc9998950af5805dd187c2"
BRIEF_SHA256_AS_PUBLISHED = "eea10105e5c8342af16571cf9bb1bff6f1726686c266b3f5275fe52918acb426"
BLIND_SHA256 = "93f9f792945f8ec851b54f4e71bf482ba3cc5eb1cde144494c51d6375c17dc81"

REVIEWERS = [
    {
        "slot": "A",
        "actor": "pi CLI, minimax/MiniMax-M3 (SW-279 Phase 2b family reviewer A)",
        "output": REVIEW / "family-reviewer-A-pi-minimax-m3.txt",
        "expected_output_sha256": "b6f2a20cd77850c634a838f972c6ae879c73d49c9a32e0f3c8b32b46418d88c0",
    },
    {
        "slot": "B",
        "actor": "Codex CLI (SW-279 Phase 2b family reviewer B)",
        "output": REVIEW / "family-reviewer-B-codex.txt",
        "expected_output_sha256": "2e58382a4745467d5f3700160c389a0f82733fa6dbdeff8b5e5705a20735387a",
    },
]

EVIDENCED = [
    "The reviewer's input was blind-queries.txt, which contains only `<opaque-id><TAB><query text>`. "
    "Every one of its 106 ids recomputes as sha256('sw279-blind-v1\\n' + text)[:10], so the id encodes "
    "the text and nothing else: no issue number, no dataset id, no stratum, no split, no provenance, "
    "no answer span. This is checkable from repository bytes.",
    "The reviewer's recorded output references only those opaque ids. It cites no file path, no line "
    "range, no symbol location, no rank, no score, and no split.",
    "The two reviewers' pair sets differ substantially (16 pairs for A, 13 for B, 8 shared), their "
    "stated own-closures are independently correct and reached from different partitions, and their "
    "prose and difficult-pair lists are unrelated. That is circumstantial evidence of independent work.",
    "Neither reviewer held any GitHub API response: the brief supplied the query text directly and "
    "the reviewers had no repository access for the task.",
]

NOT_EVIDENCED = [
    "Isolation is not proven from repository bytes. There is no session id, no timestamp, and no "
    "transcript for either reviewer. The evidence is the brief's instruction plus the divergence of "
    "the two outputs.",
    "No first-person attestation exists from either reviewer, and this record is not one. The brief "
    "did not ask for an attestation, and a later session cannot attest to what an earlier session did.",
    "Blindness to provenance leaked by inference for one class of row. blind-queries.txt contains "
    "bare symbols and an off-topic noise band that cannot be issue-title-derived under Section 2's "
    "first-token rule, and reviewer A named the noise band explicitly. So a reviewer could infer that "
    "some rows were pre-existing. A reviewer could NOT infer any row's split, which is the only leak "
    "that would poison a merge.",
]


def main() -> int:
    root = Path(subprocess.run(["git", "rev-parse", "--show-toplevel"], check=True,
                               stdout=subprocess.PIPE, text=True).stdout.strip())
    if Path.cwd().resolve() != root.resolve():
        print("run from the repository root", file=sys.stderr)
        return 2

    blind = REVIEW / "blind-queries.txt"
    brief = REVIEW / "family-reviewer-brief.txt"
    blind_sha = _access_ledger.sha256_file(blind)
    brief_sha = _access_ledger.sha256_file(brief)
    if blind_sha != BLIND_SHA256:
        print(f"blind-queries.txt digest changed: {blind_sha}", file=sys.stderr)
        return 2
    if brief_sha != BRIEF_SHA256_AS_PUBLISHED:
        print(f"family-reviewer-brief.txt digest changed: {brief_sha}", file=sys.stderr)
        return 2

    existing = [json.loads(line) for line in LEDGER.read_text(encoding="utf-8").splitlines() if line.strip()]
    already = {row["output_artifact"] for row in existing}

    written = []
    for spec in REVIEWERS:
        output: Path = spec["output"]
        out_sha = _access_ledger.sha256_file(output)
        if out_sha != spec["expected_output_sha256"]:
            print(f"reviewer {spec['slot']} output digest changed: {out_sha}", file=sys.stderr)
            return 2

        attestation_path = REVIEW / f"family-reviewer-{spec['slot']}-attestation-of-record.json"
        attestation = {
            "schema": "sw-279-family-reviewer-attestation-of-record/v1",
            "not_a_first_person_attestation": True,
            "what_this_is": (
                "A statement by the SW-279 orchestrator of what the repository can and cannot "
                "evidence about this reviewer's conduct. Section 8 of the frozen rule asks for "
                "'attestations from each actor'. This is not that. The reviewer's session no longer "
                "exists and a later session cannot attest to what an earlier one did, so the gap is "
                "narrowed by evidence rather than closed by a signature."
            ),
            "reviewer_slot": spec["slot"],
            "reviewer_actor": spec["actor"],
            "recorded_by": RECORDER,
            "recorded_at_utc": _access_ledger.now_utc(),
            "access_timestamp_utc": None,
            "access_timestamp_note": "No session timestamp was retained for this reviewer.",
            "input_blind_queries_file": blind.as_posix(),
            "input_blind_queries_sha256": blind_sha,
            "input_brief_file": brief.as_posix(),
            "input_brief_sha256_as_published": brief_sha,
            "input_brief_sha256_as_delivered": BRIEF_SHA256_AS_DELIVERED,
            "input_brief_difference": (
                "One edit: the blinded-query list's session-scratchpad path was rewritten to the "
                "repository path of the same bytes. A header note in the published file records it."
            ),
            "output_file": output.as_posix(),
            "output_sha256": out_sha,
            "evidenced_from_repository_bytes": EVIDENCED,
            "not_evidenced": NOT_EVIDENCED,
        }
        attestation_path.write_text(json.dumps(attestation, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")

        if output.as_posix() in already:
            continue
        row = _access_ledger.append(
            LEDGER,
            actor=spec["actor"],
            command_tool_class="blind all-pairs family review over opaque query ids; no repository, source, or retrieval access",
            input_artifact=blind.as_posix(),
            input_sha256=blind_sha,
            output_artifact=output.as_posix(),
            output_sha256=out_sha,
            detail=(
                f"Phase 2b family reviewer {spec['slot']}. Input was 106 lines of "
                "`<opaque-id><TAB><query text>` carrying no provenance, stratum, split, source or "
                "answer span. Brief sha256 as delivered "
                f"{BRIEF_SHA256_AS_DELIVERED}, as published {brief_sha} (the scratchpad path was "
                "rewritten for publication; a header note in the file records it). The reviewer's "
                "input set was the SUPERSEDED Phase 2a candidate set; whether that review still "
                "stands is decided by the candidate-set diff in the Phase 2 report. This row is "
                "back-filled: the access predates it and has no recorded timestamp, so "
                "timestamp_utc is when this row was written, not when the review happened."
            ),
            access_timestamp_utc=None,
            access_timestamp_note="No session timestamp was retained for this reviewer; timestamp_utc is the row-write time.",
            row_is_backfilled=True,
            first_person_attestation_exists=False,
            attestation_of_record=attestation_path.as_posix(),
        )
        written.append(row["sequence"])

    print(json.dumps({"appended_sequences": written, "ledger_rows": len(existing) + len(written)}, indent=2))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
