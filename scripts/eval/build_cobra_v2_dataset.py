#!/usr/bin/env python3
"""Build cobra-v2.json from the carried cobra-v1 rows and the accepted new candidates.

Three things this script will not do, each because a written decision says so:

  * It does not touch `cobra-v1.json`. That file stays byte-identical - it is the
    frozen artifact SW-258, SW-263 and SW-264 measured against, and cb-05 continues
    to live in it in full.
  * It does not carry `cb-05` into v2. `decision-holdout-dev-overlap.md` withdraws it:
    both blind family reviewers, twice, over two different query lists, judged it the
    same task as `cb-11`, which has been in dev since SW-258. A holdout query whose
    twin has been tuned on is not a holdout query.
  * It does not recompute `cobra-family-b63d365b20f4ca64` after that withdrawal.
    Section 7: rejected and withdrawn members "remain in the family and split
    calculation" and "their removal must not cause a resplit". So `cb-11` carries a
    family id whose provenance-key list still names `cb-05`. That is intended, and it
    is the visible trace of the withdrawal rather than a tidied-away one.

An accepted new row is written only when the answerability ledger says `accept`, and
only with the spans that resolved at the pin.
"""

from __future__ import annotations

import argparse
import json
import subprocess
import sys
from collections import Counter
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent))
import _access_ledger  # noqa: E402


HARVESTS = Path("docs/eval/retrieval/harvests")
DATASETS = Path("internal/eval/retrieval/testdata/datasets")
DATASET_V1 = DATASETS / "cobra-v1.json"
DATASET_V2 = DATASETS / "cobra-v2.json"
PIN = "a0a6ae020bb3899ff0276067863e50523f897370"
WITHDRAWN = {"cb-05"}
ACTOR = "Claude Opus 5 (SW-279 Phase 2 orchestrator)"

EVIDENCE_CLASS = (
    "agent-annotated; cobra-v1 rows human-reviewed (SW-258), issue-derived rows agent-reviewed "
    "by an independent agent (SW-279)"
)


def main() -> int:
    ap = argparse.ArgumentParser(description=__doc__)
    ap.add_argument("--harvest", required=True)
    ap.add_argument("--review", default="sw-279-phase-2b2-family-review")
    args = ap.parse_args()

    root = Path(subprocess.run(["git", "rev-parse", "--show-toplevel"], check=True,
                               stdout=subprocess.PIPE, text=True).stdout.strip())
    if Path.cwd().resolve() != root.resolve():
        print("run from the repository root", file=sys.stderr)
        return 2
    if DATASET_V2.exists():
        print(f"refusing to overwrite {DATASET_V2}", file=sys.stderr)
        return 2

    harvest = HARVESTS / args.harvest
    review = HARVESTS / args.review

    v1_bytes = DATASET_V1.read_bytes()
    v1 = json.loads(v1_bytes.decode("utf-8"))

    family_of: dict[str, str] = {}
    for line in (review / "family-ledger.jsonl").read_text(encoding="utf-8").splitlines():
        row = json.loads(line)
        if row.get("record_type") != "family":
            continue
        for key in row["provenance_keys_sorted"]:
            family_of[str(key)] = str(row["family_id"])

    sealed = {int(json.loads(line)["issue_number"]): json.loads(line)
              for line in (harvest / "sealed-questions.jsonl").read_text(encoding="utf-8").splitlines()}
    ledger = [json.loads(line) for line in
              (harvest / "answerability-ledger.jsonl").read_text(encoding="utf-8").splitlines()]

    queries: list[dict[str, object]] = []

    for query in v1["queries"]:
        if query["id"] in WITHDRAWN:
            continue
        provenance = "dataset:cobra-v1:" + str(query["id"])
        family = family_of.get(provenance)
        if family is None:
            print(f"{query['id']}: no family in the Section 7 ledger", file=sys.stderr)
            return 2
        carried = dict(query)
        judgements = carried.pop("judgements")
        carried["family_id"] = family
        carried["provenance"] = provenance
        carried["judgements"] = judgements
        queries.append(carried)

    accepted = 0
    for row in ledger:
        if row["state"] != "accept":
            continue
        number = int(row["issue_number"])
        seal = sealed[number]
        judgements = []
        for judgement in row["judgements"]:
            if not judgement["anchor_resolves_at_pin"]:
                print(f"issue {number}: a span that did not resolve reached the dataset builder", file=sys.stderr)
                return 2
            judgements.append({
                "path": judgement["path"],
                "start_line": judgement["start_line"],
                "end_line": judgement["end_line"],
                "anchor": judgement["anchor"],
                "grade": judgement["grade"],
                "reason": judgement["reason"],
                "annotator": judgement["annotator"],
                "reviewer": judgement["reviewer"],
            })
        queries.append({
            "id": "ci-" + str(number),
            "stratum": seal["stratum"],
            "language": "en",
            "split": seal["provisional_split"],
            "query": seal["Q"],
            "family_id": seal["family_id"],
            "provenance": str(seal["provenance"]),
            "judgements": judgements,
        })
        accepted += 1

    dataset = {
        "schema_version": v1["schema_version"],
        "id": "cobra-v2",
        "repo": v1["repo"],
        "repo_sha": PIN,
        "language": v1["language"],
        "evidence_class": EVIDENCE_CLASS,
        "notes": (
            "cobra-v1's 40 queries minus cb-05, plus the SW-279 issue-derived questions. The "
            "issue-derived questions were written by Cobra users years before graphi existed and "
            "were selected by a rule frozen before any issue was fetched "
            "(docs/eval/retrieval/dataset-v2-inclusion-rule.md). cb-05 is withdrawn, not moved: it "
            "stays in cobra-v1.json, which is byte-unchanged. Its family id is not recomputed, so "
            "cb-11 still carries a family whose provenance keys name cb-05 - the visible trace of "
            "the withdrawal."
        ),
        "queries": queries,
    }
    if v1.get("relevant_min_grade"):
        dataset["relevant_min_grade"] = v1["relevant_min_grade"]

    payload = (json.dumps(dataset, ensure_ascii=False, indent=2) + "\n").encode("utf-8")
    DATASET_V2.write_bytes(payload)

    if DATASET_V1.read_bytes() != v1_bytes:
        print("cobra-v1.json changed during the build; that must never happen", file=sys.stderr)
        return 2

    summary = {
        "dataset": DATASET_V2.as_posix(),
        "sha256": _access_ledger.sha256_file(DATASET_V2),
        "queries": len(queries),
        "carried_from_v1": len(queries) - accepted,
        "withdrawn_from_v1": sorted(WITHDRAWN),
        "accepted_new": accepted,
        "by_split": dict(sorted(Counter(str(q["split"]) for q in queries).items())),
        "by_stratum": dict(sorted(Counter(str(q["stratum"]) for q in queries).items())),
        "answerable_by_split": dict(sorted(Counter(
            str(q["split"]) for q in queries if q["stratum"] != "no_hit").items())),
        "cobra_v1_sha256_unchanged": _access_ledger.sha256_file(DATASET_V1),
    }
    _access_ledger.append(
        harvest / "access-ledger.jsonl",
        actor=ACTOR,
        command_tool_class="local materialisation of the v2 dataset from the sealed questions and the answerability ledger",
        input_artifact=(harvest / "answerability-ledger.jsonl").as_posix(),
        input_sha256=_access_ledger.sha256_file(harvest / "answerability-ledger.jsonl"),
        output_artifact=DATASET_V2.as_posix(),
        output_sha256=str(summary["sha256"]),
        detail=(
            "Carried the cobra-v1 rows minus cb-05, added the accepted issue-derived rows with "
            "their reviewed spans, and stamped family_id and provenance on every row. "
            "cobra-v1.json was re-read after the write and is byte-unchanged. No retrieval access."
        ),
    )
    print(json.dumps(summary, ensure_ascii=False, indent=2))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
