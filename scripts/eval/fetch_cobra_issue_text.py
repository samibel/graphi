#!/usr/bin/env python3
"""Fetch the allowed Cobra issue text for the SW-279 population, and nothing else.

Section 1 of the frozen inclusion rule permits exactly four pieces of issue content for
selection: the author's title, the opening body, the immutable issue number, the author,
and the creation time. It forbids fetching labels, reactions, comments, maintainer
replies, linked pull requests, closing events, and external links.

That prohibition is a transport prohibition (see
projects/graphi/stories/SW-279/decision-transport-overfetch.md), so the compliance
argument has to live in a selection set a reviewer can read, not in a sentence an actor
wrote afterwards. The selection set below is the whole of it:

    nodes { number createdAt title body author { login } }

A REST issue response cannot express this: it always carries labels, state, assignees,
milestone and reactions. GraphQL selects fields, so it can.

The result is written to disk. It is never printed. The actor that classifies these rows
reads the file and never holds the API response.
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


OUT_DIR = Path("docs/eval/retrieval/harvests/sw-279-phase-2a2")
RULE_COMMIT = "a0a13a757c66e8d4f0747d4a68955fe95d072573"
RULE_PATH = Path("docs/eval/retrieval/dataset-v2-inclusion-rule.md")
ACTOR = "Claude Opus 5 (SW-279 Phase 2 re-harvest fetcher)"

# The literal selection set, quoted into the access ledger so the ledger's claim and the
# program's behaviour are the same string.
SELECTION_SET = "nodes { number createdAt title body author { login } }"

QUERY = """
query($owner: String!, $name: String!, $cursor: String) {
  repository(owner: $owner, name: $name) {
    issues(
      first: 50
      after: $cursor
      states: [OPEN, CLOSED]
      orderBy: {field: CREATED_AT, direction: ASC}
    ) {
      nodes { number createdAt title body author { login } }
      pageInfo { hasNextPage endCursor }
    }
  }
}
"""


def run(*args: str) -> str:
    return subprocess.run(args, check=True, stdout=subprocess.PIPE, text=True).stdout.strip()


def sha256(data: bytes) -> str:
    return hashlib.sha256(data).hexdigest()


def parse_time(value: str) -> datetime:
    return datetime.fromisoformat(value.replace("Z", "+00:00")).astimezone(timezone.utc)


def main() -> int:
    root = Path(run("git", "rev-parse", "--show-toplevel"))
    if Path.cwd().resolve() != root.resolve():
        print("run from the repository root", file=sys.stderr)
        return 2

    # The Phase 1 boundary is re-verified here too: this fetch must postdate the frozen
    # rule commit and the working rule bytes must still be the committed bytes.
    committed_rule = subprocess.run(
        ["git", "show", f"{RULE_COMMIT}:{RULE_PATH.as_posix()}"],
        check=True, stdout=subprocess.PIPE,
    ).stdout
    if RULE_PATH.read_bytes() != committed_rule:
        print("working rule bytes differ from the frozen commit", file=sys.stderr)
        return 2
    commit_time = parse_time(run("git", "show", "-s", "--format=%cI", RULE_COMMIT))
    fetch_start = datetime.now(timezone.utc)
    if fetch_start <= commit_time:
        print("fetch start is not later than the frozen rule commit", file=sys.stderr)
        return 2

    manifest_path = OUT_DIR / "issue-numbers.txt"
    raw_path = OUT_DIR / "issue-text-raw.jsonl"
    meta_path = OUT_DIR / "issue-text-raw-metadata.json"
    ledger_path = OUT_DIR / "access-ledger.jsonl"
    for path in (raw_path, meta_path):
        if path.exists():
            print(f"refusing to overwrite frozen artifact: {path}", file=sys.stderr)
            return 2

    manifest_bytes = manifest_path.read_bytes()
    manifest_digest = sha256(manifest_bytes)
    numbers = [int(line) for line in manifest_bytes.decode("ascii").splitlines()]
    wanted = set(numbers)
    if len(wanted) != len(numbers):
        print("duplicate issue number in the manifest", file=sys.stderr)
        return 2

    cursor: str | None = None
    pages = 0
    nodes_seen = 0
    by_number: dict[int, dict[str, object]] = {}
    while True:
        command = [
            "gh", "api", "graphql",
            "-f", f"query={QUERY}",
            "-F", "owner=spf13",
            "-F", "name=cobra",
        ]
        if cursor is not None:
            command.extend(["-F", f"cursor={cursor}"])
        payload = json.loads(run(*command))
        if payload.get("errors"):
            raise RuntimeError(payload["errors"])
        issues = payload["data"]["repository"]["issues"]
        pages += 1
        for node in issues["nodes"]:
            nodes_seen += 1
            number = int(node["number"])
            if number not in wanted:
                continue  # created after the frozen cutoff; not in the population
            if number in by_number:
                print(f"duplicate issue node for {number}", file=sys.stderr)
                return 2
            author = node.get("author")
            by_number[number] = {
                "issue_number": number,
                "author": (author or {}).get("login"),
                "created_at": node.get("createdAt"),
                "title": node.get("title"),
                "body": node.get("body"),
            }
        page_info = issues["pageInfo"]
        if not page_info["hasNextPage"]:
            break
        cursor = page_info["endCursor"]

    missing = sorted(wanted - set(by_number))
    if missing:
        print(f"manifest issues absent from the fetch: {missing}", file=sys.stderr)
        return 2

    rows = [by_number[number] for number in numbers]

    # created_at was null in 1,255 of 1,255 rows of the superseded archive. Section 1
    # permits creation time, the population cutoff is defined on it, and without it the
    # population boundary is not re-derivable from the archive. Stop rather than repeat it.
    null_created = [row["issue_number"] for row in rows if not row["created_at"]]
    if null_created:
        print(f"created_at is null for {len(null_created)} rows; refusing to write", file=sys.stderr)
        return 2
    null_titles = [row["issue_number"] for row in rows if row["title"] is None]
    if null_titles:
        print(f"title is null for {len(null_titles)} rows; refusing to write", file=sys.stderr)
        return 2
    off_cutoff = [row["issue_number"] for row in rows if parse_time(str(row["created_at"])) > commit_time]
    if off_cutoff:
        print(f"rows created after the cutoff: {off_cutoff}", file=sys.stderr)
        return 2

    raw_bytes = b"".join(
        (json.dumps(row, ensure_ascii=False, separators=(",", ":")) + "\n").encode("utf-8")
        for row in rows
    )
    raw_path.write_bytes(raw_bytes)
    raw_digest = sha256(raw_bytes)
    fetch_end = datetime.now(timezone.utc)
    stamp = lambda value: value.isoformat(timespec="microseconds").replace("+00:00", "Z")

    metadata = {
        "schema": "sw-279-raw-issue-text/v1",
        "repository": "spf13/cobra",
        "population_manifest_file": manifest_path.name,
        "population_manifest_sha256": manifest_digest,
        "row_count": len(rows),
        "row_order": "ascending issue_number, exactly matching issue-numbers.txt",
        "graphql_selection_set": SELECTION_SET,
        "requested_fields": ["number", "createdAt", "title", "body", "author.login"],
        "explicitly_not_requested": [
            "labels", "reactions", "comments", "timelineItems", "closedAt", "stateReason",
            "state", "assignees", "milestone", "projectCards", "linkedBranches",
            "participants", "userContentEdits", "reactionGroups",
        ],
        "external_link_targets_opened": 0,
        "created_at_null_count": 0,
        "title_null_count": 0,
        "body_null_count": sum(1 for row in rows if row["body"] is None),
        "author_null_count": sum(1 for row in rows if row["author"] is None),
        "raw_file": raw_path.name,
        "raw_sha256": raw_digest,
        "github_pages_fetched": pages,
        "github_issue_nodes_seen_including_post_cutoff": nodes_seen,
        "fetch_started_at_utc": stamp(fetch_start),
        "fetch_completed_at_utc": stamp(fetch_end),
        "fetched_by": ACTOR,
        "printed_to_operator_console": False,
    }
    meta_path.write_text(json.dumps(metadata, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")

    _access_ledger.append(
        ledger_path,
        actor=ACTOR,
        command_tool_class="gh api graphql: field-selective allowed issue-text fetch",
        input_artifact=manifest_path.as_posix(),
        input_sha256=manifest_digest,
        output_artifact=raw_path.as_posix(),
        output_sha256=raw_digest,
        detail=(
            "GraphQL selection set was exactly `" + SELECTION_SET + "` and nothing else. "
            "No label, reaction, comment, maintainer reply, timeline item, closing event, "
            "state, assignee, milestone, or linked pull request was requested, so none was "
            "transported. No external link target was opened. The response was written to "
            "disk by this program and was never printed to an operator console."
        ),
        timestamp_utc=stamp(fetch_end),
    )

    # Counts only. No title or body value is printed.
    print(json.dumps({
        "row_count": metadata["row_count"],
        "population_manifest_sha256": manifest_digest,
        "raw_sha256": raw_digest,
        "created_at_null_count": 0,
        "body_null_count": metadata["body_null_count"],
        "author_null_count": metadata["author_null_count"],
        "pages": pages,
        "nodes_seen": nodes_seen,
        "selection_set": SELECTION_SET,
    }, indent=2))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
