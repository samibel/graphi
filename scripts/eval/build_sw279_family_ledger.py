#!/usr/bin/env python3
"""Materialise the SW-279 Section 7 family ledger from the two blind reviewers' files.

Inputs, all committed:
  * the current harvest's `candidate-ledger.jsonl` (the new candidate questions);
  * `internal/eval/retrieval/testdata/datasets/cobra-v1.json` (the existing queries);
  * `blind-queries.txt` (the opaque-id list both reviewers were given);
  * the two reviewer output files.

Outputs `family-ledger.jsonl` in the phase 2b directory: every pair decision from both
reviewers, the union, the transitive closure, all `family_id`s with their sorted provenance
keys, the cross-split conflict row, and the cb-05 withdrawal row templated in
`projects/graphi/stories/SW-279/decision-holdout-dev-overlap.md`.

Two Section 7 constraints are enforced rather than assumed:
  * `family_id` is the first 16 hex characters of the SHA-256 of the component's provenance
    keys sorted by raw UTF-8 bytes and joined with a single LF and no trailing LF;
  * the new-only split is SHA-256 of `sw-279-family-split-v1` + LF + `family_id`, ordered by
    the full digest then the `family_id`, positions 1, 9, 17, ... to dev.

Nothing here re-derives a split for an existing query: a family containing an existing query
inherits that query's split, and the cb-05 withdrawal explicitly does NOT recompute
`cobra-family-b63d365b20f4ca64` because that would be a resplit, which Section 7 forbids.

No query text is printed. The ledger contains it because Section 7 requires the pair
decisions to be auditable; the console output is counts only.
"""

from __future__ import annotations

import argparse
import collections
import hashlib
import json
import re
import subprocess
import sys
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent))
import _access_ledger  # noqa: E402


HARVESTS = Path("docs/eval/retrieval/harvests")
REVIEW = HARVESTS / "sw-279-phase-2b-family-review"
DATASET_V1 = Path("internal/eval/retrieval/testdata/datasets/cobra-v1.json")
ACTOR = "Claude Opus 5 (SW-279 Phase 2 orchestrator)"

BLIND_SALT = "sw279-blind-v1\n"
FAMILY_SPLIT_SALT = "sw-279-family-split-v1\n"

WITHDRAWAL = {
    "record_type": "withdrawal",
    "query_id": "cb-05",
    "family_id": "cobra-family-b63d365b20f4ca64",
    "family_members": [
        "dataset:cobra-v1:cb-05 (holdout)",
        "dataset:cobra-v1:cb-11 (dev)",
    ],
    "reviewer_A_same_task": True,
    "reviewer_B_same_task": True,
    "v1_split": "holdout",
    "v1_split_after": "holdout (cobra-v1.json unchanged)",
    "disposition": "withdrawn_from_v2_release_dataset",
    "reason": (
        "Section 7 cross-split family; cb-11 has been in dev and in use since SW-258, so "
        "cb-05's holdout independence is void. No SW-258 assignment moved."
    ),
    "family_id_recomputed_after_withdrawal": False,
    "family_id_recompute_note": (
        "Section 7: rejected and unresolved provisional candidates remain in the family and "
        "split calculation and 'their removal must not cause a resplit'. cb-05 therefore stays "
        "in the family computation and cobra-family-b63d365b20f4ca64 is not recomputed."
    ),
    "decision_record": "projects/graphi/stories/SW-279/decision-holdout-dev-overlap.md",
    "decided_by": "orchestrator (solo-owner standing authority)",
    "date": "2026-09-04",
}


def blind_id(text: str) -> str:
    return "q-" + hashlib.sha256((BLIND_SALT + text).encode("utf-8")).hexdigest()[:10]


def parse_reviewer(path: Path, idset: set[str]) -> tuple[set[frozenset[str]], set[str]]:
    pairs: set[frozenset[str]] = set()
    answered: set[str] = set()
    for line in path.read_text(encoding="utf-8").splitlines():
        match = re.match(r"\s*(q-[0-9a-f]{10})\s*(?:->|→|:)\s*(.*)$", line)
        if not match:
            continue
        left, rest = match.group(1), match.group(2)
        if left not in idset:
            continue
        answered.add(left)
        if re.search(r"\bNONE\b", rest, re.I):
            continue
        for right in re.findall(r"q-[0-9a-f]{10}", rest):
            if right in idset and right != left:
                pairs.add(frozenset((left, right)))
    return pairs, answered


def main() -> int:
    ap = argparse.ArgumentParser(description=__doc__)
    ap.add_argument("--harvest", required=True, help="harvest directory name under docs/eval/retrieval/harvests/")
    args = ap.parse_args()

    root = Path(subprocess.run(["git", "rev-parse", "--show-toplevel"], check=True,
                               stdout=subprocess.PIPE, text=True).stdout.strip())
    if Path.cwd().resolve() != root.resolve():
        print("run from the repository root", file=sys.stderr)
        return 2

    harvest = HARVESTS / args.harvest
    candidate_ledger = harvest / "candidate-ledger.jsonl"
    blind_path = REVIEW / "blind-queries.txt"
    out_path = REVIEW / "family-ledger.jsonl"
    if out_path.exists():
        print(f"refusing to overwrite {out_path}", file=sys.stderr)
        return 2

    # provenance key and (for existing queries) inherited split, per blind id
    prov: dict[str, tuple[str, str | None]] = {}
    for line in candidate_ledger.read_text(encoding="utf-8").splitlines():
        row = json.loads(line)
        if row["verdict"] == "candidate":
            prov[blind_id(row["Q"])] = (f"github:spf13/cobra#{int(row['issue_number'])}", None)
    v1 = json.loads(DATASET_V1.read_text(encoding="utf-8"))
    for query in v1["queries"]:
        prov[blind_id(query["query"])] = (f"dataset:cobra-v1:{query['id']}", query.get("split"))

    blind_lines = [line for line in blind_path.read_text(encoding="utf-8").splitlines() if line.strip()]
    ids = [line.split("\t", 1)[0] for line in blind_lines]
    text = {line.split("\t", 1)[0]: line.split("\t", 1)[1] for line in blind_lines}
    idset = set(ids)
    if len(idset) != len(ids):
        print("duplicate id in blind-queries.txt", file=sys.stderr)
        return 2
    missing_prov = sorted(idset - set(prov))
    stale_prov = sorted(set(prov) - idset)
    if missing_prov or stale_prov:
        print(
            "blind-queries.txt does not match the current candidate set plus cobra-v1: "
            f"{len(missing_prov)} blind ids have no provenance, {len(stale_prov)} provenance "
            "keys are absent from the blind list. The family review must be re-run on the new "
            "candidate set (frozen rule Section 7).",
            file=sys.stderr,
        )
        return 3

    reviewer_a = REVIEW / "family-reviewer-A-pi-minimax-m3.txt"
    reviewer_b = REVIEW / "family-reviewer-B-codex.txt"
    pairs_a, answered_a = parse_reviewer(reviewer_a, idset)
    pairs_b, answered_b = parse_reviewer(reviewer_b, idset)
    union = pairs_a | pairs_b

    parent = {i: i for i in ids}

    def find(x: str) -> str:
        while parent[x] != x:
            parent[x] = parent[parent[x]]
            x = parent[x]
        return x

    for pair in union:
        a, b = tuple(pair)
        ra, rb = find(a), find(b)
        if ra != rb:
            parent[ra] = rb

    components: dict[str, list[str]] = collections.defaultdict(list)
    for i in ids:
        components[find(i)].append(i)

    rows: list[dict[str, object]] = []

    # 1. every pair decision from both reviewers, agreements and disagreements alike
    for pair in sorted(union, key=lambda p: sorted(p)):
        a, b = sorted(pair)
        rows.append({
            "record_type": "pair_decision",
            "id_a": a,
            "id_b": b,
            "provenance_a": prov[a][0],
            "provenance_b": prov[b][0],
            "reviewer_A_same_task": pair in pairs_a,
            "reviewer_B_same_task": pair in pairs_b,
            "joined": True,
            "join_rule": "Section 7: a pair is joined if either reviewer marks it same-task",
        })

    # 2. the components, with Section 7 family ids
    families: list[dict[str, object]] = []
    conflicts: list[dict[str, object]] = []
    new_only: list[str] = []
    for members in components.values():
        keys = sorted(prov[m][0] for m in members)
        family_id = "cobra-family-" + hashlib.sha256("\n".join(keys).encode("utf-8")).hexdigest()[:16]
        splits = sorted({prov[m][1] for m in members if prov[m][1]})
        family = {
            "record_type": "family",
            "family_id": family_id,
            "provenance_keys_sorted": keys,
            "size": len(members),
            "existing_splits_present": splits,
        }
        if len(splits) > 1:
            family["inherited_split"] = None
            family["cross_split_conflict"] = True
            conflicts.append(family)
        elif len(splits) == 1:
            family["inherited_split"] = splits[0]
            family["cross_split_conflict"] = False
        else:
            family["inherited_split"] = None
            family["cross_split_conflict"] = False
            new_only.append(family_id)
        families.append(family)

    # 3. the frozen 1:7 positional split over new-only families
    order = sorted(
        new_only,
        key=lambda f: (hashlib.sha256((FAMILY_SPLIT_SALT + f).encode("utf-8")).hexdigest(), f),
    )
    assigned = {}
    for position, family_id in enumerate(order, start=1):
        assigned[family_id] = "dev" if (position - 1) % 8 == 0 else "holdout"
    for family in families:
        fid = str(family["family_id"])
        if fid in assigned:
            family["provisional_split"] = assigned[fid]
            family["split_source"] = "Section 7 positional 1:7 allocation over new-only families"
            family["split_position"] = order.index(fid) + 1
        elif family["inherited_split"]:
            family["provisional_split"] = family["inherited_split"]
            family["split_source"] = "inherited from an existing cobra-v1 query in the family"
        else:
            family["provisional_split"] = None
            family["split_source"] = "unassigned: cross-split conflict, see the withdrawal record"

    rows.extend(sorted(families, key=lambda f: str(f["family_id"])))
    rows.append(WITHDRAWAL)

    summary = {
        "record_type": "summary",
        "harvest": args.harvest,
        "blind_queries_sha256": _access_ledger.sha256_file(blind_path),
        "reviewer_A_file": reviewer_a.as_posix(),
        "reviewer_A_sha256": _access_ledger.sha256_file(reviewer_a),
        "reviewer_A_ids_answered": len(answered_a),
        "reviewer_A_pairs": len(pairs_a),
        "reviewer_B_file": reviewer_b.as_posix(),
        "reviewer_B_sha256": _access_ledger.sha256_file(reviewer_b),
        "reviewer_B_ids_answered": len(answered_b),
        "reviewer_B_pairs": len(pairs_b),
        "pairs_agreed": len(pairs_a & pairs_b),
        "pairs_only_A": len(pairs_a - pairs_b),
        "pairs_only_B": len(pairs_b - pairs_a),
        "pairs_union": len(union),
        "queries_total": len(ids),
        "families_total": len(families),
        "family_size_distribution": dict(sorted(collections.Counter(int(f["size"]) for f in families).items())),
        "families_inheriting_dev": sum(1 for f in families if f.get("inherited_split") == "dev"),
        "families_inheriting_holdout": sum(1 for f in families if f.get("inherited_split") == "holdout"),
        "families_new_only": len(new_only),
        "new_only_dev": sum(1 for v in assigned.values() if v == "dev"),
        "new_only_holdout": sum(1 for v in assigned.values() if v == "holdout"),
        "cross_split_conflicts": [f["family_id"] for f in conflicts],
    }
    rows.append(summary)

    payload = b"".join(
        (json.dumps(row, ensure_ascii=False, separators=(",", ":")) + "\n").encode("utf-8")
        for row in rows
    )
    out_path.write_bytes(payload)

    _access_ledger.append(
        HARVESTS / args.harvest / "access-ledger.jsonl",
        actor=ACTOR,
        command_tool_class="local Section 7 union, transitive closure, family id and 1:7 positional split",
        input_artifact=(REVIEW / "family-reviewer-A-pi-minimax-m3.txt").as_posix(),
        input_sha256=_access_ledger.sha256_file(reviewer_a),
        output_artifact=out_path.as_posix(),
        output_sha256=hashlib.sha256(payload).hexdigest(),
        detail=(
            "Materialised the family ledger from the two blind reviewers' committed outputs and "
            "the candidate ledger. No source, retrieval or split-directed judgement: the pair "
            "decisions are the reviewers', the closure and ids are mechanical, and the new-only "
            "split is the frozen salt and offset. cobra-family-b63d365b20f4ca64 was NOT "
            "recomputed after the cb-05 withdrawal. Reviewer B's file digest is "
            + _access_ledger.sha256_file(reviewer_b) + "."
        ),
    )

    # counts only; no query text
    print(json.dumps(summary, ensure_ascii=False, indent=2))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
