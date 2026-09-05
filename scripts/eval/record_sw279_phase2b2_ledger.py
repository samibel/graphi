#!/usr/bin/env python3
"""Record the SW-279 phase 2b2 blind family review: invocations, logs, ledger rows.

The first family review had no ledger row, no timestamp, no session id and no transcript
(see ../sw-279-phase-2b-family-review/family-reviewer-*-attestation-of-record.json). This
review was invoked from a shell, so the evidence it leaves is materially better and is
captured here rather than described: the exact command line, the model, the wall-clock
window, the CLI's own session id where it emits one, and the digests of the prompt and the
console log.

It is still not a first-person attestation. A CLI process that has exited cannot attest,
and a fresh session cannot attest to what an earlier one did. What is written is an
attestation *of record*, labelled as such, stating what the repository evidences and -
separately and explicitly - what it does not.

Absolute paths in the prompts and logs are rewritten to repository-relative paths before
they are committed, because this is a public repository and the paths carry a local
username. Both digests are recorded, as-delivered and as-published, the same way the first
review's brief did it.
"""

from __future__ import annotations

import argparse
import hashlib
import json
import re
import subprocess
import sys
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent))
import _access_ledger  # noqa: E402


HARVESTS = Path("docs/eval/retrieval/harvests")
REVIEW = HARVESTS / "sw-279-phase-2b2-family-review"
LEDGER = HARVESTS / "sw-279-phase-2a2" / "access-ledger.jsonl"
RECORDER = "Claude Opus 5 (SW-279 Phase 2 orchestrator)"

REWRITE_NOTE = (
    "[PUBLICATION NOTE - not part of the file as produced]\n"
    "Absolute paths have been rewritten to repository-relative paths. This is a public\n"
    "repository and the absolute paths carry a local username. Nothing else is changed;\n"
    "both digests are recorded in invocation-record.json.\n"
    "[END PUBLICATION NOTE]\n\n"
)

EVIDENCED = [
    "The reviewer's input was blind-queries.txt: 134 lines of `<opaque-id><TAB><query text>` and "
    "nothing else. Every id recomputes as sha256('sw279-blind-v1\\n' + text)[:10], so it encodes the "
    "text and nothing else - no issue number, no dataset id, no stratum, no split, no provenance, no "
    "answer span. This is checkable from repository bytes.",
    "The exact command line, the model identifier, and the wall-clock window of the invocation are "
    "recorded in invocation-record.json, together with the digest of the prompt as delivered.",
    "The reviewer's output references only opaque ids. It cites no file path, no line range, no "
    "symbol, no rank, no score and no split.",
    "The prompt forbade opening any file other than the brief and the query list, and forbade source, "
    "dataset, retrieval, ranking and previous-review access.",
]

NOT_EVIDENCED = [
    "This is not a first-person attestation. The process has exited; it cannot attest, and a fresh "
    "session cannot attest to what an earlier session did.",
    "The console log records what the CLI printed, not every file it opened. A prohibited read that "
    "the CLI did not print would not appear in it.",
    "Blindness to provenance leaks by inference for one class of row: the list contains bare symbols "
    "and an off-topic noise band that cannot be issue-title-derived under Section 2's first-token "
    "rule, so a reviewer can infer that some rows are pre-existing. A reviewer still cannot infer any "
    "row's split, which is the only leak that would poison a merge.",
]


def rewrite(text: str, repo_root: Path) -> str:
    return text.replace(str(repo_root) + "/", "").replace(str(repo_root), ".")


def main() -> int:
    ap = argparse.ArgumentParser(description=__doc__)
    ap.add_argument("--scratch", required=True, help="directory holding the prompt and log files")
    ap.add_argument("--reviewer-a-started", required=True)
    ap.add_argument("--reviewer-a-finished", required=True)
    ap.add_argument("--reviewer-b-started", required=True)
    ap.add_argument("--reviewer-b-finished", required=True)
    args = ap.parse_args()

    repo_root = Path(subprocess.run(["git", "rev-parse", "--show-toplevel"], check=True,
                                    stdout=subprocess.PIPE, text=True).stdout.strip())
    if Path.cwd().resolve() != repo_root.resolve():
        print("run from the repository root", file=sys.stderr)
        return 2
    scratch = Path(args.scratch)

    specs = [
        {
            "slot": "A",
            "actor": "pi CLI, minimax/MiniMax-M3 (SW-279 Phase 2b2 family reviewer A)",
            "command": 'pi -p --model "minimax/MiniMax-M3" --no-session "$(cat <prompt>)"',
            "model": "minimax/MiniMax-M3",
            "prompt_src": scratch / "reviewer-prompt-A.txt",
            "log_src": scratch / "reviewerA.log",
            "output": REVIEW / "family-reviewer-A-pi-minimax-m3.txt",
            "started": args.reviewer_a_started,
            "finished": args.reviewer_a_finished,
            "session_note": "pi was invoked with --no-session, so no transcript was retained.",
        },
        {
            "slot": "B",
            "actor": "Codex CLI v0.148.0 (SW-279 Phase 2b2 family reviewer B)",
            "command": 'codex exec --dangerously-bypass-approvals-and-sandbox "$(cat <prompt>)"',
            "model": "codex exec default",
            "prompt_src": scratch / "reviewer-prompt-B.txt",
            "log_src": scratch / "reviewerB.log",
            "output": REVIEW / "family-reviewer-B-codex.txt",
            "started": args.reviewer_b_started,
            "finished": args.reviewer_b_finished,
            "session_note": None,
        },
    ]

    blind = REVIEW / "blind-queries.txt"
    brief = REVIEW / "family-reviewer-brief.txt"
    blind_sha = _access_ledger.sha256_file(blind)
    brief_sha = _access_ledger.sha256_file(brief)

    existing = [json.loads(line) for line in LEDGER.read_text(encoding="utf-8").splitlines() if line.strip()]
    already = {row["output_artifact"] for row in existing}

    record = {
        "schema": "sw-279-phase-2b2-invocation-record/v1",
        "recorded_by": RECORDER,
        "recorded_at_utc": _access_ledger.now_utc(),
        "blind_queries_file": blind.as_posix(),
        "blind_queries_sha256": blind_sha,
        "blind_queries_count": sum(1 for line in blind.read_text(encoding="utf-8").splitlines() if line.strip()),
        "brief_file": brief.as_posix(),
        "brief_sha256": brief_sha,
        "reviewers": [],
    }

    for spec in specs:
        prompt_raw = spec["prompt_src"].read_bytes()
        log_raw = spec["log_src"].read_bytes()
        prompt_pub = REWRITE_NOTE + rewrite(prompt_raw.decode("utf-8"), repo_root)
        log_pub = REWRITE_NOTE + rewrite(log_raw.decode("utf-8", errors="replace"), repo_root)
        prompt_dst = REVIEW / f"reviewer-{spec['slot']}-invocation-prompt.txt"
        log_dst = REVIEW / f"reviewer-{spec['slot']}-console-log.txt"
        prompt_dst.write_text(prompt_pub, encoding="utf-8")
        log_dst.write_text(log_pub, encoding="utf-8")

        session_ids = sorted(set(re.findall(
            r"session id: ([0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12})",
            log_raw.decode("utf-8", errors="replace"), re.I)))

        out_sha = _access_ledger.sha256_file(spec["output"])
        entry = {
            "slot": spec["slot"],
            "actor": spec["actor"],
            "model": spec["model"],
            "command": spec["command"],
            "invoked_at_utc": spec["started"],
            "finished_at_utc": spec["finished"],
            "cli_session_ids": session_ids,
            "session_note": spec["session_note"] or (
                "The CLI emitted its own session id, recorded above; a transcript exists outside "
                "this repository." if session_ids else "No session id was emitted."),
            "prompt_sha256_as_delivered": hashlib.sha256(prompt_raw).hexdigest(),
            "prompt_file_as_published": prompt_dst.as_posix(),
            "prompt_sha256_as_published": _access_ledger.sha256_file(prompt_dst),
            "console_log_sha256_as_produced": hashlib.sha256(log_raw).hexdigest(),
            "console_log_file_as_published": log_dst.as_posix(),
            "console_log_sha256_as_published": _access_ledger.sha256_file(log_dst),
            "output_file": spec["output"].as_posix(),
            "output_sha256": out_sha,
        }
        record["reviewers"].append(entry)

        attestation_path = REVIEW / f"family-reviewer-{spec['slot']}-attestation-of-record.json"
        attestation_path.write_text(json.dumps({
            "schema": "sw-279-family-reviewer-attestation-of-record/v1",
            "not_a_first_person_attestation": True,
            "what_this_is": (
                "A statement by the SW-279 orchestrator of what the repository can and cannot "
                "evidence about this reviewer's conduct. Section 8 asks for 'attestations from each "
                "actor'. This is not that; it is what is available once the process has exited."
            ),
            "reviewer_slot": spec["slot"],
            "reviewer_actor": spec["actor"],
            "recorded_by": RECORDER,
            "recorded_at_utc": _access_ledger.now_utc(),
            "invocation": entry,
            "evidenced_from_repository_bytes_and_the_invocation_record": EVIDENCED,
            "not_evidenced": NOT_EVIDENCED,
        }, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")

        if spec["output"].as_posix() in already:
            continue
        _access_ledger.append(
            LEDGER,
            actor=spec["actor"],
            command_tool_class="blind all-pairs family review over opaque query ids; no repository, source, or retrieval access",
            input_artifact=blind.as_posix(),
            input_sha256=blind_sha,
            output_artifact=spec["output"].as_posix(),
            output_sha256=out_sha,
            detail=(
                f"Phase 2b2 family reviewer {spec['slot']}, on the 94-candidate set. Input was 134 "
                "lines of `<opaque-id><TAB><query text>` carrying no provenance, stratum, split, "
                f"source or answer span. Brief sha256 {brief_sha}. Command and model are in "
                "invocation-record.json; the attestation of record is "
                f"{attestation_path.as_posix()}."
            ),
            timestamp_utc=spec["finished"],
            invocation_record=(REVIEW / "invocation-record.json").as_posix(),
            first_person_attestation_exists=False,
            attestation_of_record=attestation_path.as_posix(),
        )

    (REVIEW / "invocation-record.json").write_text(
        json.dumps(record, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")
    print(json.dumps(record, ensure_ascii=False, indent=2))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
