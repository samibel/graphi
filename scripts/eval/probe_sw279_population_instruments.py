#!/usr/bin/env python3
"""Compare the two population instruments year by year, using no issue text.

The superseded Phase 2a harvest built its population with the GitHub *Search* API
(`repo:spf13/cobra is:issue`, partitioned by creation year). The re-harvest uses the
GraphQL `issues` connection. They disagree by one issue. This script quantifies the
disagreement per creation year so the discrepancy has a named cause rather than an
assumption.

GraphQL selection sets used, and nothing else:
  * `issues(...) { nodes { number createdAt } }`
  * `search(query:, type: ISSUE) { issueCount }`
No title, body, author, label, reaction, comment, timeline, state, assignee, milestone
or linked pull request is requested.
"""

from __future__ import annotations

import json
import subprocess
import sys
from collections import Counter
from datetime import datetime, timezone
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent))
import _access_ledger  # noqa: E402


RULE_COMMIT = "a0a13a757c66e8d4f0747d4a68955fe95d072573"
NEW_DIR = Path("docs/eval/retrieval/harvests/sw-279-phase-2a2")
ACTOR = "Claude Opus 5 (SW-279 Phase 2 re-harvest fetcher)"

ISSUES_QUERY = """
query($owner: String!, $name: String!, $cursor: String) {
  repository(owner: $owner, name: $name) {
    issues(
      first: 100
      after: $cursor
      states: [OPEN, CLOSED]
      orderBy: {field: CREATED_AT, direction: ASC}
    ) {
      nodes { number createdAt }
      pageInfo { hasNextPage endCursor }
    }
  }
}
"""

SEARCH_QUERY = """
query($q: String!) { search(query: $q, type: ISSUE, first: 0) { issueCount } }
"""


def run(*args: str) -> str:
    return subprocess.run(args, check=True, stdout=subprocess.PIPE, text=True).stdout.strip()


def parse_time(value: str) -> datetime:
    return datetime.fromisoformat(value.replace("Z", "+00:00")).astimezone(timezone.utc)


def main() -> int:
    root = Path(run("git", "rev-parse", "--show-toplevel"))
    if Path.cwd().resolve() != root.resolve():
        print("run from the repository root", file=sys.stderr)
        return 2

    cutoff = parse_time(run("git", "show", "-s", "--format=%cI", RULE_COMMIT))

    cursor = None
    nodes: list[dict[str, object]] = []
    while True:
        cmd = ["gh", "api", "graphql", "-f", f"query={ISSUES_QUERY}",
               "-F", "owner=spf13", "-F", "name=cobra"]
        if cursor is not None:
            cmd += ["-F", f"cursor={cursor}"]
        payload = json.loads(run(*cmd))
        if payload.get("errors"):
            raise RuntimeError(payload["errors"])
        conn = payload["data"]["repository"]["issues"]
        nodes.extend(conn["nodes"])
        if not conn["pageInfo"]["hasNextPage"]:
            break
        cursor = conn["pageInfo"]["endCursor"]

    in_population = [n for n in nodes if parse_time(str(n["createdAt"])) <= cutoff]
    by_year = Counter(str(n["createdAt"])[:4] for n in in_population)

    rows = []
    for year in sorted(by_year):
        if year == str(cutoff.year):
            window = f"{year}-01-01..{cutoff.date().isoformat()}"
        else:
            window = f"{year}-01-01..{year}-12-31"
        payload = json.loads(run(
            "gh", "api", "graphql", "-f", f"query={SEARCH_QUERY}",
            "-F", f"q=repo:spf13/cobra is:issue created:{window}",
        ))
        if payload.get("errors"):
            raise RuntimeError(payload["errors"])
        search_count = int(payload["data"]["search"]["issueCount"])
        rows.append({
            "creation_year": year,
            "search_window": window,
            "graphql_issues_connection_count": by_year[year],
            "github_search_issue_count": search_count,
            "delta_graphql_minus_search": by_year[year] - search_count,
        })

    out = {
        "schema": "sw-279-population-instrument-probe/v1",
        "probed_at_utc": datetime.now(timezone.utc).isoformat(timespec="microseconds").replace("+00:00", "Z"),
        "probed_by": ACTOR,
        "cutoff_utc": cutoff.isoformat(timespec="microseconds").replace("+00:00", "Z"),
        "graphql_issues_connection_total": len(in_population),
        "github_search_total": sum(r["github_search_issue_count"] for r in rows),
        "per_year": rows,
        "issue_text_read": False,
    }
    path = NEW_DIR / "population-instrument-probe.json"
    if path.exists():
        print(f"refusing to overwrite {path}", file=sys.stderr)
        return 2
    path.write_text(json.dumps(out, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")
    _access_ledger.append(
        NEW_DIR / "access-ledger.jsonl",
        actor=ACTOR,
        command_tool_class='gh api graphql: issues-connection vs GitHub Search per-year count comparison',
        input_artifact=(NEW_DIR / 'issue-numbers.txt').as_posix(),
        input_sha256=_access_ledger.sha256_file(NEW_DIR / 'issue-numbers.txt'),
        output_artifact=(path).as_posix(),
        output_sha256=_access_ledger.sha256_file(path),
        detail='GraphQL selection sets were exactly `issues(...) { nodes { number createdAt } }` and `search(...) { issueCount }` and nothing else. No issue text was read.',
    )
    print(json.dumps(out, ensure_ascii=False, indent=2))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
