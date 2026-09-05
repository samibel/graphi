"""Append-only access ledger helper for SW-279 Phase 2.

Section 8 of the frozen inclusion rule requires "an append-only access ledger containing
actor, timestamp, command/tool class, input artifact digest, and output artifact digest".
The superseded harvest's ledger rows were composed by the actor rather than emitted by the
program that made the access, which is how its account of the projection came to be
falsified by its own output. Every access in this harvest goes through this function, so a
row is a by-product of the access rather than a description of it.
"""

from __future__ import annotations

import hashlib
import json
from datetime import datetime, timezone
from pathlib import Path


def sha256_file(path: Path) -> str:
    return hashlib.sha256(path.read_bytes()).hexdigest()


def now_utc() -> str:
    return datetime.now(timezone.utc).isoformat(timespec="microseconds").replace("+00:00", "Z")


def append(
    ledger_path: Path,
    *,
    actor: str,
    command_tool_class: str,
    input_artifact: str,
    input_sha256: str,
    output_artifact: str,
    output_sha256: str,
    detail: str,
    timestamp_utc: str | None = None,
    **extra: object,
) -> dict[str, object]:
    """Append one row, numbering it from the rows already present. Never rewrites.

    ``extra`` keys are added verbatim after the fixed schema. Use them when a row needs to
    say something the fixed columns cannot express honestly - for example that the access
    itself has no recorded timestamp, so ``timestamp_utc`` is the time the row was written
    rather than the time of the access.
    """
    if ledger_path.exists():
        existing = [json.loads(line) for line in ledger_path.read_text(encoding="utf-8").splitlines() if line.strip()]
    else:
        existing = []
    row = {
        "sequence": len(existing) + 1,
        "actor": actor,
        "timestamp_utc": timestamp_utc or now_utc(),
        "command_tool_class": command_tool_class,
        "input_artifact": input_artifact,
        "input_sha256": input_sha256,
        "output_artifact": output_artifact,
        "output_sha256": output_sha256,
        "detail": detail,
    }
    row.update(extra)
    ledger_path.parent.mkdir(parents=True, exist_ok=True)
    with ledger_path.open("a", encoding="utf-8") as handle:
        handle.write(json.dumps(row, ensure_ascii=False, separators=(",", ":")) + "\n")
    return row
