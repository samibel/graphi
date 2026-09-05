#!/usr/bin/env python3
"""Back-fill access-ledger rows for two diagnostic accesses that predate the helper.

The population-discrepancy probe and the instrument probe ran before
`scripts/eval/_access_ledger.py` existed, so neither emitted its own ledger row at the
time. Both scripts now append their own row on any future run; this script closes the gap
for the runs that already happened.

Every field is derived from artefacts on disk — the timestamps come from each record's own
`resolved_at_utc` / `probed_at_utc`, the digests are recomputed from the files. Nothing is
asserted from memory. The rows therefore carry timestamps that precede the row appended
before them: ledger sequence is append order, not access order, and that is disclosed in
each row's detail.
"""

from __future__ import annotations

import json
import subprocess
import sys
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent))
import _access_ledger  # noqa: E402


NEW_DIR = Path("docs/eval/retrieval/harvests/sw-279-phase-2a2")
LEDGER = NEW_DIR / "access-ledger.jsonl"
ACTOR = "Claude Opus 5 (SW-279 Phase 2 re-harvest fetcher)"

BACKFILL_NOTE = (
    " This row was back-filled by scripts/eval/backfill_sw279_diagnostic_ledger_rows.py: the "
    "access happened at the timestamp shown, which precedes the timestamp of the row appended "
    "before it. Ledger sequence is append order, not access order."
)

SPECS = [
    {
        "output": NEW_DIR / "population-discrepancy-record.json",
        "time_key": "resolved_at_utc",
        "tool": "gh api graphql: single-issue creation-time probe for the population discrepancy",
        "detail": (
            "GraphQL selection set was exactly `issue(number:) { number createdAt }` and nothing "
            "else. No title, body, author, label, reaction, comment, timeline, state, assignee, "
            "milestone or linked pull request was requested. No issue text was read."
        ),
    },
    {
        "output": NEW_DIR / "population-instrument-probe.json",
        "time_key": "probed_at_utc",
        "tool": "gh api graphql: issues-connection vs GitHub Search per-year count comparison",
        "detail": (
            "GraphQL selection sets were exactly `issues(...) { nodes { number createdAt } }` and "
            "`search(...) { issueCount }` and nothing else. No issue text was read."
        ),
    },
]


def main() -> int:
    root = Path(subprocess.run(["git", "rev-parse", "--show-toplevel"], check=True,
                               stdout=subprocess.PIPE, text=True).stdout.strip())
    if Path.cwd().resolve() != root.resolve():
        print("run from the repository root", file=sys.stderr)
        return 2

    existing = [json.loads(line) for line in LEDGER.read_text(encoding="utf-8").splitlines() if line.strip()]
    already = {row["output_artifact"] for row in existing}

    manifest = NEW_DIR / "issue-numbers.txt"
    written = []
    for spec in SPECS:
        output: Path = spec["output"]
        if output.as_posix() in already:
            continue
        record = json.loads(output.read_text(encoding="utf-8"))
        row = _access_ledger.append(
            LEDGER,
            actor=ACTOR,
            command_tool_class=str(spec["tool"]),
            input_artifact=manifest.as_posix(),
            input_sha256=_access_ledger.sha256_file(manifest),
            output_artifact=output.as_posix(),
            output_sha256=_access_ledger.sha256_file(output),
            detail=str(spec["detail"]) + BACKFILL_NOTE,
            timestamp_utc=str(record[str(spec["time_key"])]),
        )
        written.append(row["sequence"])
    print(json.dumps({"backfilled_sequences": written, "ledger_rows": len(existing) + len(written)}, indent=2))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
