#!/usr/bin/env python3
"""Seal question, stratum, rubric, family and provisional split for SW-279 Phase 2.

Section 8's permitted order puts this at step 3: it must complete before any source
annotator opens the pinned checkout, and nothing it seals may change afterwards.

The Section 6 answer-mode table and the universal pass/fail paragraph are parsed out of the
frozen rule's own bytes rather than retyped here, so a rubric can never drift from the rule
it claims to implement. If the rule file changed, the parse fails and so does the seal.

Inputs:
  * `<harvest>/candidate-questions.jsonl` — provenance, T and the derived Q;
  * `<harvest>/stratum-assignments.jsonl` — one row per candidate: issue_number, stratum,
    first_applicable_rule, rationale, assigned_by. Section 5 requires the chosen stratum and
    the first applicable numbered rule to be recorded, before source access.
  * `sw-279-phase-2b-family-review/family-ledger.jsonl` — family ids and provisional splits.

Output: `<harvest>/sealed-questions.jsonl` and `<harvest>/phase-2-seal.json`.
No query text is printed to the console.
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
DEFAULT_REVIEW = "sw-279-phase-2b2-family-review"
RULE_PATH = Path("docs/eval/retrieval/dataset-v2-inclusion-rule.md")
RULE_SHA256 = "d9aea9863501d3d2827aa191275f689fc8afeda30ecb8dcbbb379d7339d85a2c"
RULE_COMMIT = "a0a13a757c66e8d4f0747d4a68955fe95d072573"
RUBRIC_VERSION = "cobra-issue-direct-answer/v1"
ACTOR = "Claude Opus 5 (SW-279 Phase 2 orchestrator)"

LEGAL_STRATA = {"config_docs", "architecture_flow", "nl_behaviour"}
STRATUM_RULES = {"config_docs": 1, "architecture_flow": 2, "nl_behaviour": 3}


def parse_rule_section_6(rule_text: str) -> tuple[dict[str, str], str]:
    """Extract the answer-mode table and the universal pass/fail paragraph from the rule."""
    section = rule_text.split("## 6. Answer rubric and judgement grades", 1)
    if len(section) != 2:
        raise SystemExit("frozen rule: Section 6 heading not found")
    body = section[1].split("\n## ", 1)[0]

    modes: dict[str, str] = {}
    for line in body.splitlines():
        if not line.startswith("| `"):
            continue
        cells = [cell.strip() for cell in line.strip().strip("|").split("|")]
        if len(cells) != 2:
            continue
        tokens = re.findall(r"`([a-z]+)`", cells[0])
        for token in tokens:
            modes[token] = cells[1]
    if not modes:
        raise SystemExit("frozen rule: Section 6 answer-mode table not parsed")

    start = body.index("For every mode, a passing answer must")
    end = body.index("Source judgements use the existing four grades")
    passfail = " ".join(body[start:end].split())
    return modes, passfail


def rubric_record(query: str, modes: dict[str, str], passfail: str) -> dict[str, object]:
    first = query.split(" ")[0].casefold().strip("?")
    if first not in modes:
        raise SystemExit(f"no Section 6 answer mode for first token {first!r}")
    record = {
        "rubric_version": RUBRIC_VERSION,
        "Q": query,
        "answer_mode": modes[first],
        "universal_pass_fail": passfail,
    }
    canonical = json.dumps(record, ensure_ascii=False, sort_keys=True, separators=(",", ":")).encode("utf-8")
    record["rubric_sha256"] = hashlib.sha256(canonical).hexdigest()
    return record


def main() -> int:
    ap = argparse.ArgumentParser(description=__doc__)
    ap.add_argument("--harvest", required=True)
    ap.add_argument("--review", default=DEFAULT_REVIEW,
                    help=f"family-review directory under docs/eval/retrieval/harvests/ (default: {DEFAULT_REVIEW})")
    args = ap.parse_args()
    review = HARVESTS / args.review

    root = Path(subprocess.run(["git", "rev-parse", "--show-toplevel"], check=True,
                               stdout=subprocess.PIPE, text=True).stdout.strip())
    if Path.cwd().resolve() != root.resolve():
        print("run from the repository root", file=sys.stderr)
        return 2

    rule_bytes = RULE_PATH.read_bytes()
    if hashlib.sha256(rule_bytes).hexdigest() != RULE_SHA256:
        print("the frozen rule bytes have changed; refusing to seal", file=sys.stderr)
        return 2
    modes, passfail = parse_rule_section_6(rule_bytes.decode("utf-8"))

    harvest = HARVESTS / args.harvest
    questions_path = harvest / "candidate-questions.jsonl"
    strata_path = harvest / "stratum-assignments.jsonl"
    family_path = review / "family-ledger.jsonl"
    sealed_path = harvest / "sealed-questions.jsonl"
    seal_path = harvest / "phase-2-seal.json"
    for path in (sealed_path, seal_path):
        if path.exists():
            print(f"refusing to overwrite a sealed artifact: {path}", file=sys.stderr)
            return 2
    for path in (questions_path, strata_path, family_path):
        if not path.exists():
            print(f"missing input: {path}", file=sys.stderr)
            return 2

    questions = [json.loads(line) for line in questions_path.read_text(encoding="utf-8").splitlines()]
    strata = {int(json.loads(line)["issue_number"]): json.loads(line)
              for line in strata_path.read_text(encoding="utf-8").splitlines()}

    families: dict[str, dict[str, object]] = {}
    prov_to_family: dict[str, dict[str, object]] = {}
    for line in family_path.read_text(encoding="utf-8").splitlines():
        row = json.loads(line)
        if row.get("record_type") != "family":
            continue
        families[str(row["family_id"])] = row
        for key in row["provenance_keys_sorted"]:
            prov_to_family[str(key)] = row

    if sorted(strata) != sorted(int(q["issue_number"]) for q in questions):
        print("stratum assignments do not cover the candidate set one-for-one", file=sys.stderr)
        return 2

    sealed: list[dict[str, object]] = []
    for question in questions:
        number = int(question["issue_number"])
        provenance = str(question["provenance"])
        stratum_row = strata[number]
        stratum = str(stratum_row["stratum"])
        if stratum not in LEGAL_STRATA:
            print(f"issue {number}: stratum {stratum!r} is not an issue-derived stratum", file=sys.stderr)
            return 2
        rule_number = int(stratum_row["first_applicable_rule"])
        if STRATUM_RULES[stratum] != rule_number:
            print(f"issue {number}: stratum {stratum} does not match rule {rule_number}", file=sys.stderr)
            return 2
        family = prov_to_family.get(provenance)
        if family is None:
            print(f"issue {number}: {provenance} is in no family", file=sys.stderr)
            return 2
        split = family.get("provisional_split")
        if split not in {"dev", "holdout"}:
            print(f"issue {number}: family {family['family_id']} has no provisional split", file=sys.stderr)
            return 2
        sealed.append({
            "issue_number": number,
            "provenance": provenance,
            "T": question["T"],
            "Q": question["Q"],
            "title_sha256": question["title_sha256"],
            "stratum": stratum,
            "stratum_first_applicable_rule": rule_number,
            "stratum_rationale": stratum_row["rationale"],
            "stratum_assigned_by": stratum_row["assigned_by"],
            "family_id": family["family_id"],
            "family_size": family["size"],
            "provisional_split": split,
            "split_source": family["split_source"],
            "rubric": rubric_record(str(question["Q"]), modes, passfail),
        })

    sealed.sort(key=lambda row: int(row["issue_number"]))
    payload = b"".join(
        (json.dumps(row, ensure_ascii=False, separators=(",", ":")) + "\n").encode("utf-8")
        for row in sealed
    )
    sealed_path.write_bytes(payload)
    sealed_sha = hashlib.sha256(payload).hexdigest()

    head = subprocess.run(["git", "rev-parse", "HEAD"], check=True, stdout=subprocess.PIPE,
                          text=True).stdout.strip()
    sealed_at = datetime.now(timezone.utc).isoformat(timespec="microseconds").replace("+00:00", "Z")
    seal = {
        "schema": "sw-279-phase-2-seal/v1",
        "sealed_at_utc": sealed_at,
        "sealed_by": ACTOR,
        "repository_commit_at_seal": head,
        "phase_1_rule_commit": RULE_COMMIT,
        "phase_1_rule_sha256": RULE_SHA256,
        "harvest": args.harvest,
        "candidate_questions_sha256": _access_ledger.sha256_file(questions_path),
        "stratum_assignments_sha256": _access_ledger.sha256_file(strata_path),
        "family_ledger_sha256": _access_ledger.sha256_file(family_path),
        "sealed_questions_file": sealed_path.name,
        "sealed_questions_sha256": sealed_sha,
        "sealed_count": len(sealed),
        "sealed_by_stratum": dict(sorted(Counter(str(r["stratum"]) for r in sealed).items())),
        "sealed_by_provisional_split": dict(sorted(Counter(str(r["provisional_split"]) for r in sealed).items())),
        "scope": (
            "question text, stratum, rubric, family and provisional split for every candidate. "
            "Answerability, grade-3 spans and dataset membership are NOT sealed here: Section 8 "
            "step 4 comes after this seal."
        ),
        "source_access_before_this_seal": "none for any provisional query",
    }
    seal_path.write_text(json.dumps(seal, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")

    _access_ledger.append(
        harvest / "access-ledger.jsonl",
        actor=ACTOR,
        command_tool_class="local Section 3/5/6/7 seal of question, stratum, rubric, family and provisional split",
        input_artifact=questions_path.as_posix(),
        input_sha256=seal["candidate_questions_sha256"],
        output_artifact=sealed_path.as_posix(),
        output_sha256=sealed_sha,
        detail=(
            "Sealed before any source annotator opened the pinned checkout. The Section 6 answer "
            "modes and universal pass/fail text were parsed from the frozen rule's own bytes, not "
            "retyped. No source, retrieval, answerability or judgement input was consulted."
        ),
        timestamp_utc=sealed_at,
    )

    print(json.dumps(seal, ensure_ascii=False, indent=2))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
