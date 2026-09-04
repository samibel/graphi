#!/usr/bin/env python3
"""Seal allowed Cobra issue text and prepare SW-279 Phase 2a semantic review rows."""

from __future__ import annotations

import hashlib
import json
import unicodedata
from collections import Counter
from datetime import datetime, timezone
from pathlib import Path


ROOT = Path("docs/eval/retrieval/harvests/sw-279-phase-2a")
MANIFEST = ROOT / "issue-numbers.txt"
ARCHIVE = ROOT / "issue-text.jsonl"
ARCHIVE_META = ROOT / "issue-text-metadata.json"
MECHANICAL = ROOT / "mechanical-candidate-ledger.jsonl"
SEMANTIC_REVIEW = ROOT / "semantic-review.jsonl"
ACCESS = ROOT / "access-ledger.jsonl"
MANIFEST_SHA256 = "b9f712af1bea40bbde437dee649a35346de023891839e8ae148138a94a8c4a17"
SELECTOR = "Codex /root (SW-279 Phase 2a selector)"

MARKERS = ("[question]:", "(question):", "question:", "[question]", "(question)")
FIRST_TOKENS = {
    "how", "what", "where", "when", "why", "which", "who", "can",
    "could", "does", "do", "is", "are", "should", "would",
}
WHITE_SPACE = {
    0x0009, 0x000A, 0x000B, 0x000C, 0x000D, 0x0020, 0x0085, 0x00A0,
    0x1680, 0x2028, 0x2029, 0x202F, 0x205F, 0x3000,
} | set(range(0x2000, 0x200B))


def digest(data: bytes) -> str:
    return hashlib.sha256(data).hexdigest()


def timestamp() -> str:
    return datetime.now(timezone.utc).isoformat(timespec="microseconds").replace("+00:00", "Z")


def encode_jsonl(rows: list[dict[str, object]]) -> bytes:
    return b"".join(
        (json.dumps(row, ensure_ascii=False, separators=(",", ":")) + "\n").encode("utf-8")
        for row in rows
    )


def collapse_white_space(value: str) -> str:
    out: list[str] = []
    in_space = False
    for char in value:
        if ord(char) in WHITE_SPACE:
            if out and not in_space:
                out.append(" ")
            in_space = True
        else:
            out.append(char)
            in_space = False
    if out and out[-1] == " ":
        out.pop()
    return "".join(out)


def derive_question(title: str) -> tuple[str, str | None]:
    query = collapse_white_space(unicodedata.normalize("NFC", title))
    folded = query.casefold()
    removed: str | None = None
    for marker in MARKERS:
        if folded.startswith(marker):
            query = query[len(marker):].lstrip(" ")
            removed = marker
            break
    return query, removed


def syntax_failures(query: str) -> list[str]:
    failures: list[str] = []
    if not 3 <= len(query) <= 200:
        failures.append("S2_CODEPOINT_COUNT")
    tokens = query.split(" ") if query else []
    if len(tokens) < 3:
        failures.append("S2_TOKEN_COUNT")
    if query.count("?") > 1 or ("?" in query and not query.endswith("?")):
        failures.append("S2_QUESTION_MARK")
    first = tokens[0] if tokens else ""
    if not (first.isascii() and first.isalpha() and first.lower() in FIRST_TOKENS):
        failures.append("S2_FIRST_TOKEN")
    return failures


def main() -> None:
    manifest_bytes = MANIFEST.read_bytes()
    if digest(manifest_bytes) != MANIFEST_SHA256:
        raise SystemExit("population manifest digest mismatch")
    numbers = [int(line) for line in manifest_bytes.decode("ascii").splitlines()]

    raw_rows = [json.loads(line) for line in ARCHIVE.read_text(encoding="utf-8").splitlines()]
    if [row["issue_number"] for row in raw_rows] != numbers:
        raise SystemExit("issue archive does not match the population manifest in order")
    if ARCHIVE_META.exists() or MECHANICAL.exists() or SEMANTIC_REVIEW.exists():
        raise SystemExit("refusing to overwrite a sealed/prepared Phase 2a artifact")

    prepared_at = timestamp()
    archive_rows: list[dict[str, object]] = []
    mechanical_rows: list[dict[str, object]] = []
    semantic_rows: list[dict[str, object]] = []
    reason_counts: Counter[str] = Counter()
    marker_counts: Counter[str] = Counter()

    for row in raw_rows:
        title = row["title"]
        body = row["body"]
        if not isinstance(title, str) or not (body is None or isinstance(body, str)):
            raise SystemExit(f"invalid raw text type for issue {row['issue_number']}")
        title_sha = digest(title.encode("utf-8"))
        body_sha = digest((body or "").encode("utf-8"))
        query, marker = derive_question(title)
        failures = syntax_failures(query)
        if marker:
            marker_counts[marker] += 1

        archived = {
            "issue_number": row["issue_number"],
            "author": row["author"],
            "created_at": row["created_at"],
            "title": title,
            "body": body,
            "body_was_null": body is None,
            "title_sha256": title_sha,
            "body_sha256": body_sha,
            "population_manifest_sha256": MANIFEST_SHA256,
        }
        archive_rows.append(archived)

        base = {
            "issue_number": row["issue_number"],
            "selector": SELECTOR,
            "verdict_at_utc": prepared_at,
            "population_manifest_sha256": MANIFEST_SHA256,
            "title_sha256": title_sha,
            "body_sha256": body_sha,
            "T": title,
            "Q": query,
            "removed_question_marker": marker,
        }
        if failures:
            for failure in failures:
                reason_counts[failure] += 1
            mechanical_rows.append({
                **base,
                "state": "reject:not_candidate",
                "verdict": "reject",
                "deciding_clauses": failures,
                "rationale": "The mechanically derived Q fails the frozen Section 2 syntactic eligibility test: " + ", ".join(failures) + ".",
            })
        else:
            pending = {
                **base,
                "state": "pending_semantic_classification",
                "verdict": None,
                "deciding_clauses": [],
                "rationale": None,
            }
            mechanical_rows.append(pending)
            semantic_rows.append({
                "issue_number": row["issue_number"],
                "author": row["author"],
                "created_at": row["created_at"],
                "T": title,
                "Q": query,
                "body": body,
                "title_sha256": title_sha,
                "body_sha256": body_sha,
                "population_manifest_sha256": MANIFEST_SHA256,
            })

    archive_bytes = encode_jsonl(archive_rows)
    ARCHIVE.write_bytes(archive_bytes)
    MECHANICAL.write_bytes(encode_jsonl(mechanical_rows))
    SEMANTIC_REVIEW.write_bytes(encode_jsonl(semantic_rows))
    archive_sha = digest(archive_bytes)
    mechanical_sha = digest(MECHANICAL.read_bytes())
    semantic_sha = digest(SEMANTIC_REVIEW.read_bytes())

    metadata = {
        "schema": "sw-279-allowed-issue-text-archive/v1",
        "repository": "spf13/cobra",
        "population_manifest_sha256": MANIFEST_SHA256,
        "row_count": len(archive_rows),
        "fields_fetched_for_archive": ["issue_number", "author.login", "created_at", "title", "body"],
        "null_body_digest_convention": "A null opening body is preserved as null, body_was_null is true, and body_sha256 hashes the empty byte sequence.",
        "title_digest_convention": "SHA-256 of the raw title's UTF-8 bytes.",
        "body_digest_convention": "SHA-256 of the raw opening body's UTF-8 bytes, or empty bytes when GitHub returned null.",
        "row_order": "ascending issue_number, exactly matching issue-numbers.txt",
        "archive_file": ARCHIVE.name,
        "archive_sha256": archive_sha,
        "mechanical_ledger_file": MECHANICAL.name,
        "mechanical_ledger_sha256": mechanical_sha,
        "semantic_review_file": SEMANTIC_REVIEW.name,
        "semantic_review_sha256": semantic_sha,
        "prepared_at_utc": prepared_at,
        "syntactically_eligible_count": len(semantic_rows),
        "mechanical_reject_count": len(mechanical_rows) - len(semantic_rows),
        "mechanical_failure_counts_nonexclusive": dict(sorted(reason_counts.items())),
        "removed_marker_counts": dict(sorted(marker_counts.items())),
    }
    ARCHIVE_META.write_text(json.dumps(metadata, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")

    prior = ACCESS.read_text(encoding="utf-8").splitlines()
    if len(prior) != 1 or json.loads(prior[0]).get("sequence") != 1:
        raise SystemExit("unexpected access-ledger state before allowed-text archive event")
    event = {
        "sequence": 2,
        "actor": SELECTOR,
        "timestamp_utc": timestamp(),
        "command_tool_class": "installed GitHub connector: allowed opening issue text search plus exact-number author reads",
        "input_artifact": MANIFEST.as_posix(),
        "input_sha256": MANIFEST_SHA256,
        "output_artifact": ARCHIVE.as_posix(),
        "output_sha256": archive_sha,
        "detail": "Projected only issue_number, author.login, created_at, title, and opening body. Connector-returned labels, comments, reactions, state, assignees, milestones, and other fields were discarded and were not used for selection.",
    }
    with ACCESS.open("a", encoding="utf-8") as handle:
        handle.write(json.dumps(event, ensure_ascii=False, separators=(",", ":")) + "\n")

    print(json.dumps(metadata, ensure_ascii=False, indent=2))


if __name__ == "__main__":
    main()
