#!/usr/bin/env python3
"""Freeze the SW-279 Cobra issue-number population without fetching issue text."""

from __future__ import annotations

import hashlib
import json
import subprocess
import sys
from datetime import datetime, timezone
from pathlib import Path


RULE_COMMIT = "a0a13a757c66e8d4f0747d4a68955fe95d072573"
RULE_PATH = Path("docs/eval/retrieval/dataset-v2-inclusion-rule.md")
OUT_DIR = Path("docs/eval/retrieval/harvests/sw-279-phase-2a")
CUTOFF_SOURCE = "committer timestamp of the frozen Phase 1 rule commit"
EXPECTED_POPULATION = 1255

QUERY = """
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


def run(*args: str) -> str:
    result = subprocess.run(args, check=True, stdout=subprocess.PIPE, text=True)
    return result.stdout.strip()


def parse_time(value: str) -> datetime:
    return datetime.fromisoformat(value.replace("Z", "+00:00")).astimezone(timezone.utc)


def sha256(data: bytes) -> str:
    return hashlib.sha256(data).hexdigest()


def canonical_json(value: object) -> bytes:
    return (json.dumps(value, ensure_ascii=False, indent=2) + "\n").encode("utf-8")


def main() -> int:
    root = Path(run("git", "rev-parse", "--show-toplevel"))
    if Path.cwd().resolve() != root.resolve():
        print("run from the repository root", file=sys.stderr)
        return 2

    resolved = run("git", "rev-parse", f"{RULE_COMMIT}^{{commit}}")
    if resolved != RULE_COMMIT:
        print(f"frozen rule commit mismatch: {resolved}", file=sys.stderr)
        return 2

    committed_rule = subprocess.run(
        ["git", "show", f"{RULE_COMMIT}:{RULE_PATH.as_posix()}"],
        check=True,
        stdout=subprocess.PIPE,
    ).stdout
    working_rule = RULE_PATH.read_bytes()
    if working_rule != committed_rule:
        print("working rule bytes differ from the frozen commit", file=sys.stderr)
        return 2

    commit_time_text = run("git", "show", "-s", "--format=%cI", RULE_COMMIT)
    commit_time = parse_time(commit_time_text)
    fetch_start = datetime.now(timezone.utc)
    if fetch_start <= commit_time:
        print("fetch start is not later than the frozen rule commit", file=sys.stderr)
        return 2

    OUT_DIR.mkdir(parents=True, exist_ok=True)
    manifest_path = OUT_DIR / "issue-numbers.txt"
    metadata_path = OUT_DIR / "population-manifest.json"
    first_fetch_path = OUT_DIR / "first-fetch-record.json"
    access_path = OUT_DIR / "access-ledger.jsonl"
    for path in (manifest_path, metadata_path, first_fetch_path, access_path):
        if path.exists():
            print(f"refusing to overwrite frozen artifact: {path}", file=sys.stderr)
            return 2

    cursor: str | None = None
    all_nodes: list[dict[str, object]] = []
    page_count = 0
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
        all_nodes.extend(issues["nodes"])
        page_count += 1
        page_info = issues["pageInfo"]
        if not page_info["hasNextPage"]:
            break
        cursor = page_info["endCursor"]

    cutoff = commit_time
    population = sorted(
        (
            node
            for node in all_nodes
            if parse_time(str(node["createdAt"])) <= cutoff
        ),
        key=lambda node: int(node["number"]),
    )
    numbers = [int(node["number"]) for node in population]
    if len(numbers) != len(set(numbers)):
        print("duplicate issue number returned by GitHub", file=sys.stderr)
        return 2

    manifest_bytes = "".join(f"{number}\n" for number in numbers).encode("ascii")
    manifest_digest = sha256(manifest_bytes)
    fetch_end = datetime.now(timezone.utc)
    timestamp = lambda value: value.isoformat(timespec="microseconds").replace("+00:00", "Z")
    rule_digest = sha256(working_rule)

    manifest_path.write_bytes(manifest_bytes)
    metadata = {
        "schema": "sw-279-population-manifest/v1",
        "repository": "spf13/cobra",
        "population_definition": "GitHub objects classified as issues, created at or before cutoff",
        "cutoff_utc": timestamp(cutoff),
        "cutoff_source": CUTOFF_SOURCE,
        "serialization": "ascending decimal issue number followed by LF, including final LF",
        "population_size": len(numbers),
        "expected_population_size": EXPECTED_POPULATION,
        "count_matches_phase_1_expectation": len(numbers) == EXPECTED_POPULATION,
        "issue_numbers_file": manifest_path.name,
        "issue_numbers_sha256": manifest_digest,
        "first_issue_number": numbers[0] if numbers else None,
        "last_issue_number": numbers[-1] if numbers else None,
        "github_pages_fetched": page_count,
        "github_issue_nodes_seen_including_post_cutoff": len(all_nodes),
        "fetch_started_at_utc": timestamp(fetch_start),
        "fetch_completed_at_utc": timestamp(fetch_end),
    }
    metadata_path.write_bytes(canonical_json(metadata))

    first_fetch = {
        "schema": "sw-279-first-fetch-record/v1",
        "repository": "spf13/cobra",
        "phase_1_rule_commit": RULE_COMMIT,
        "phase_1_rule_path": RULE_PATH.as_posix(),
        "phase_1_rule_sha256": rule_digest,
        "phase_1_commit_time_utc": timestamp(commit_time),
        "fetch_started_at_utc": timestamp(fetch_start),
        "fetch_start_postdates_rule_commit": fetch_start > commit_time,
        "requested_fields": ["number", "createdAt"],
        "explicitly_not_requested": ["title", "body", "labels", "reactions", "comments", "timeline", "linkedPullRequests"],
        "output_artifact": manifest_path.as_posix(),
        "output_sha256": manifest_digest,
    }
    first_fetch_bytes = canonical_json(first_fetch)
    first_fetch_path.write_bytes(first_fetch_bytes)

    access_event = {
        "sequence": 1,
        "actor": "Codex /root (SW-279 Phase 2a selector)",
        "timestamp_utc": timestamp(fetch_end),
        "command_tool_class": "gh api graphql: issue number and creation-time population fetch",
        "input_artifact": RULE_PATH.as_posix(),
        "input_sha256": rule_digest,
        "output_artifact": manifest_path.as_posix(),
        "output_sha256": manifest_digest,
        "detail": "No title, body, comment, label, reaction, timeline, linked resource, source, or retrieval field requested.",
    }
    access_path.write_bytes((json.dumps(access_event, ensure_ascii=False, separators=(",", ":")) + "\n").encode("utf-8"))

    print(json.dumps({
        "population_size": len(numbers),
        "expected_population_size": EXPECTED_POPULATION,
        "manifest_sha256": manifest_digest,
        "rule_sha256": rule_digest,
        "commit_time_utc": timestamp(commit_time),
        "fetch_started_at_utc": timestamp(fetch_start),
        "pages": page_count,
        "nodes_seen": len(all_nodes),
    }, indent=2))
    if len(numbers) != EXPECTED_POPULATION:
        print("population count discrepancy: stop before issue-text fetch", file=sys.stderr)
        return 3
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
