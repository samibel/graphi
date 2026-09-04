#!/usr/bin/env python3
"""Resolve the SW-279 population-count discrepancy without reading any issue text.

Section 1 of the frozen rule says a population count differing from the recorded 1,255
"is a discrepancy to report and resolve without reading issue text; it does not authorize
silently changing the population." This script produces the resolution record.

Its GraphQL selection set is exactly `issue(number:) { number createdAt }`. It requests no
title, body, author, label, reaction, comment, timeline, state, assignee, milestone, or
linked pull request. Section 1 permits the immutable issue number and creation time.
"""

from __future__ import annotations

import hashlib
import json
import subprocess
import sys
from datetime import datetime, timezone
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent))
import _access_ledger  # noqa: E402


OLD_DIR = Path("docs/eval/retrieval/harvests/sw-279-phase-2a-superseded")
NEW_DIR = Path("docs/eval/retrieval/harvests/sw-279-phase-2a2")
RULE_COMMIT = "a0a13a757c66e8d4f0747d4a68955fe95d072573"
ACTOR = "Claude Opus 5 (SW-279 Phase 2 re-harvest fetcher)"

QUERY = """
query($owner: String!, $name: String!, $number: Int!) {
  repository(owner: $owner, name: $name) {
    issue(number: $number) { number createdAt }
  }
}
"""


def run(*args: str) -> str:
    return subprocess.run(args, check=True, stdout=subprocess.PIPE, text=True).stdout.strip()


def parse_time(value: str) -> datetime:
    return datetime.fromisoformat(value.replace("Z", "+00:00")).astimezone(timezone.utc)


def stamp(value: datetime) -> str:
    return value.isoformat(timespec="microseconds").replace("+00:00", "Z")


def main() -> int:
    root = Path(run("git", "rev-parse", "--show-toplevel"))
    if Path.cwd().resolve() != root.resolve():
        print("run from the repository root", file=sys.stderr)
        return 2

    cutoff = parse_time(run("git", "show", "-s", "--format=%cI", RULE_COMMIT))
    old = [int(n) for n in (OLD_DIR / "issue-numbers.txt").read_text().split()]
    new = [int(n) for n in (NEW_DIR / "issue-numbers.txt").read_text().split()]
    added = sorted(set(new) - set(old))
    removed = sorted(set(old) - set(new))

    probes = []
    for number in added:
        payload = json.loads(run(
            "gh", "api", "graphql",
            "-f", f"query={QUERY}",
            "-F", "owner=spf13",
            "-F", "name=cobra",
            "-F", f"number={number}",
        ))
        if payload.get("errors"):
            raise RuntimeError(payload["errors"])
        node = payload["data"]["repository"]["issue"]
        created = parse_time(str(node["createdAt"]))
        probes.append({
            "issue_number": int(node["number"]),
            "created_at": str(node["createdAt"]),
            "created_at_or_before_cutoff": created <= cutoff,
            "classified_by_github_as": "issue (the GraphQL issues connection and issue(number:) "
                                       "field never return pull requests)",
        })

    out_path = NEW_DIR / "population-discrepancy-record.json"
    if out_path.exists():
        print(f"refusing to overwrite {out_path}", file=sys.stderr)
        return 2

    record = {
        "schema": "sw-279-population-discrepancy-record/v1",
        "resolved_at_utc": stamp(datetime.now(timezone.utc)),
        "resolved_by": ACTOR,
        "cutoff_utc": stamp(cutoff),
        "expected_population_size": 1255,
        "observed_population_size": len(new),
        "superseded_manifest": (OLD_DIR / "issue-numbers.txt").as_posix(),
        "superseded_manifest_sha256": hashlib.sha256((OLD_DIR / "issue-numbers.txt").read_bytes()).hexdigest(),
        "new_manifest": (NEW_DIR / "issue-numbers.txt").as_posix(),
        "new_manifest_sha256": hashlib.sha256((NEW_DIR / "issue-numbers.txt").read_bytes()).hexdigest(),
        "numbers_present_in_new_absent_from_superseded": added,
        "numbers_present_in_superseded_absent_from_new": removed,
        "new_is_strict_superset_of_superseded": not removed and bool(added),
        "probes": probes,
        "selection_set": "issue(number:) { number createdAt }",
        "issue_text_read": False,
    }
    out_path.write_text(json.dumps(record, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")
    _access_ledger.append(
        NEW_DIR / "access-ledger.jsonl",
        actor=ACTOR,
        command_tool_class='gh api graphql: single-issue creation-time probe for the population discrepancy',
        input_artifact=(NEW_DIR / 'issue-numbers.txt').as_posix(),
        input_sha256=_access_ledger.sha256_file(NEW_DIR / 'issue-numbers.txt'),
        output_artifact=(out_path).as_posix(),
        output_sha256=_access_ledger.sha256_file(out_path),
        detail='GraphQL selection set was exactly `issue(number:) { number createdAt }` and nothing else. No title, body, author, label, reaction, comment, timeline, state, assignee, milestone or linked pull request was requested. No issue text was read.',
    )
    print(json.dumps(record, ensure_ascii=False, indent=2))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
