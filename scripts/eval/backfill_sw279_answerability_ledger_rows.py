#!/usr/bin/env python3
"""Back-fill the §8 access-ledger rows for the answerability actors, and record their inputs.

SW-279 review round 1, finding B2. Section 8 requires an append-only access ledger carrying
"actor, timestamp, command/tool class, input artifact digest, and output artifact digest" for
every access. Ten actors made a step-4 source access to the pinned checkout and each produced
a committed output file, and none of them had a ledger row: the only answerability rows were
the orchestrator's own finalizer runs. The same defect for two family reviewers was treated as
a hard stop in `projects/graphi/stories/SW-279/decision-holdout-dev-overlap.md` §3 item 6;
applying a weaker standard to ten actors than to two would be indefensible.

This script writes what the repository can actually evidence, and says where each field comes
from:

  * `timestamp_utc` is the actor's own `attested_at_utc`, not the time this row was written.
    The row carries `backfilled: true` and says so in its `detail`, exactly as sequences 6 and
    7 do for the two diagnostic probes.
  * output digests are the ones the actor attested, and every one is re-verified here against
    the committed bytes; a mismatch is a refusal, not a warning.
  * input digests are computed from the committed input file. For the five reviewers this
    matches the `annotator_file_sha256` the reviewer itself attested, and that agreement is
    checked. **For the five first-pass annotators there is no first-person input digest to
    check against**: their attestation schema did not carry one. Their rows therefore record
    the input digest as orchestrator-recorded, and the ledger row says so rather than
    presenting it as the annotator's claim.

Second step, `--annotate-attestations`: writes the same orchestrator-recorded input digest into
each first-pass annotator attestation under a field whose name states who recorded it. It is
not a first-person statement and must never be read as one - the annotators have exited and a
process that has exited cannot attest.

Nothing here is deleted or rewritten: the ledger is appended to, and the attestations gain a
labelled field without any existing field changing.

**The two steps are order-independent, and where they cannot be, this refuses rather than
producing a record that lies.** Round 1 wrote them as one pass that snapshotted the
already-recorded outputs first, then possibly rewrote an attestation, then skipped any row
that already existed. Running it once without `--annotate-attestations` and once with returned
0 both times and left a committed ledger row whose `attestation_sha256` was the digest of the
attestation as it stood before the second run edited it - a pinned digest that no longer
resolves, which is the exact defect class the ledger exists to make visible. Three changes fix
it:

  * every attestation that is going to be annotated is annotated BEFORE any ledger row is
    written, and the set of already-recorded outputs is read after that, so within one
    invocation the two flag settings converge on the same result;
  * annotating an attestation that an existing ledger row already pins by digest is REFUSED,
    because the ledger is append-only and there is no honest in-place repair - the remedy is a
    correction row (`append_sw279_ledger_correction.py`), and the operator is told so;
  * every run, with or without the flag, ends by re-resolving every `attestation_sha256` in
    the ledger against the committed bytes and refusing if any of them is stale. A stale
    digest already on disk is reported by the next run rather than waiting for a reader.
"""

from __future__ import annotations

import argparse
import json
import subprocess
import sys
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent))
import _access_ledger  # noqa: E402


HARVESTS = Path("docs/eval/retrieval/harvests")
ORCHESTRATOR = "Claude Opus 5 (SW-279 Phase 2 orchestrator)"
BACKFILL_REASON = (
    "SW-279 review round 1 finding B2: this actor's step-4 access had no ledger row. The row is "
    "back-filled with the actor's own attested timestamp, so ledger sequence here is append "
    "order and not access order."
)


def plans(harvest: Path) -> list[dict[str, object]]:
    answerability = harvest / "answerability"
    batches = json.loads((answerability / "batch-plan.json").read_text(encoding="utf-8"))["batches"]
    reannotation = answerability / "reannotation-plan.json"
    if reannotation.exists():
        batches = batches + json.loads(reannotation.read_text(encoding="utf-8"))["batches"]
    return batches


def main() -> int:
    ap = argparse.ArgumentParser(description=__doc__)
    ap.add_argument("--harvest", required=True)
    ap.add_argument("--annotate-attestations", action="store_true",
                    help="also write the orchestrator-recorded input digest into the annotator attestations")
    args = ap.parse_args()

    root = Path(subprocess.run(["git", "rev-parse", "--show-toplevel"], check=True,
                               stdout=subprocess.PIPE, text=True).stdout.strip())
    if Path.cwd().resolve() != root.resolve():
        print("run from the repository root", file=sys.stderr)
        return 2

    harvest = HARVESTS / args.harvest
    answerability = harvest / "answerability"
    ledger_path = harvest / "access-ledger.jsonl"

    def ledger_rows() -> list[dict[str, object]]:
        return [json.loads(line) for line in ledger_path.read_text(encoding="utf-8").splitlines()
                if line.strip()]

    def stale_attestation_digests() -> list[str]:
        """Every `attestation_sha256` in the ledger that no longer resolves to its file."""
        stale: list[str] = []
        for row in ledger_rows():
            attestation = row.get("attestation")
            pinned = row.get("attestation_sha256")
            if not attestation or not pinned:
                continue
            path = Path(str(attestation))
            if not path.exists():
                stale.append(f"sequence {row.get('sequence')}: {attestation} no longer exists")
                continue
            actual = _access_ledger.sha256_file(path)
            if actual != str(pinned):
                stale.append(
                    f"sequence {row.get('sequence')}: {attestation} is pinned at {pinned} but "
                    f"its committed bytes hash to {actual}"
                )
        return stale

    # A stale digest already on disk is reported now, whatever this run was asked to do.
    entry_stale = stale_attestation_digests()
    if entry_stale:
        for problem in entry_stale:
            print(problem, file=sys.stderr)
        print(
            f"{len(entry_stale)} committed ledger row(s) pin an attestation digest that no longer "
            "resolves. The ledger is append-only, so the remedy is a correction row "
            "(scripts/eval/append_sw279_ledger_correction.py), not an edit.",
            file=sys.stderr,
        )
        return 2

    # Step one, for every batch, before any ledger row is written: annotate the attestations
    # that need it. Doing this first is what makes the two flag settings converge on the same
    # committed bytes regardless of the order they are invoked in.
    if args.annotate_attestations:
        pinned_by_row = {str(row.get("attestation")): row for row in ledger_rows()
                         if row.get("attestation") and row.get("attestation_sha256")}
        pending: list[tuple[Path, dict[str, object]]] = []
        for batch in plans(harvest):
            attestation_path = answerability / f"annotator-{batch['annotator_slot']}-attestation.json"
            if not attestation_path.exists():
                print(f"missing artefact: {attestation_path}", file=sys.stderr)
                return 2
            record = json.loads(attestation_path.read_text(encoding="utf-8"))
            if record.get("input_sha256") is not None:
                continue
            if "input_artifact_orchestrator_recorded" in record:
                continue
            input_path = Path(str(batch["input"]))
            if not input_path.exists():
                print(f"missing artefact: {input_path}", file=sys.stderr)
                return 2
            row = pinned_by_row.get(attestation_path.as_posix())
            if row is not None:
                print(
                    f"{attestation_path.as_posix()} is already pinned by ledger sequence "
                    f"{row.get('sequence')} at {row.get('attestation_sha256')}. Annotating it now "
                    "would leave that committed row pointing at bytes that no longer exist, which "
                    "is the record lying about itself. Run --annotate-attestations BEFORE the "
                    "ledger rows are written; on an existing ledger the remedy is a correction "
                    "row (scripts/eval/append_sw279_ledger_correction.py), not an edit.",
                    file=sys.stderr,
                )
                return 2
            record["input_artifact_orchestrator_recorded"] = {
                "artifact": input_path.as_posix(),
                "sha256": _access_ledger.sha256_file(input_path),
                "recorded_by": ORCHESTRATOR,
                "note": (
                    "Back-filled during SW-279 review round 1. This attestation's schema "
                    "carried no input digest, so the bytes this annotator read were recorded "
                    "nowhere. This is the digest of the committed batch input the batch plan "
                    "assigned to this annotator; it is NOT a first-person statement by the "
                    "annotator, which has exited and cannot make one. The annotator's own "
                    "output_sha256 is unchanged and still resolves."
                ),
            }
            pending.append((attestation_path, record))
        for attestation_path, record in pending:
            attestation_path.write_text(
                json.dumps(record, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")
            print(f"recorded input digest in {attestation_path.as_posix()}")

    # Only now is it safe to read what the ledger already records and to digest the
    # attestations: every byte they will be pinned at is on disk.
    already = {str(row.get("output_artifact")) for row in ledger_rows()}

    written: list[dict[str, object]] = []
    for batch in plans(harvest):
        number = int(batch["batch"])
        input_path = Path(str(batch["input"]))
        annotations_path = Path(str(batch["annotations_output"]))
        reviews_path = Path(str(batch["reviews_output"]))
        annotator_attestation = answerability / f"annotator-{batch['annotator_slot']}-attestation.json"
        reviewer_attestation = answerability / f"reviewer-{batch['reviewer_slot']}-attestation.json"
        for path in (input_path, annotations_path, reviews_path,
                     annotator_attestation, reviewer_attestation):
            if not path.exists():
                print(f"missing artefact: {path}", file=sys.stderr)
                return 2

        annotator = json.loads(annotator_attestation.read_text(encoding="utf-8"))
        reviewer = json.loads(reviewer_attestation.read_text(encoding="utf-8"))

        input_digest = _access_ledger.sha256_file(input_path)
        annotations_digest = _access_ledger.sha256_file(annotations_path)
        reviews_digest = _access_ledger.sha256_file(reviews_path)

        # Every attested digest must resolve to the committed bytes. If it does not, the
        # record and the artefact disagree and neither may be published as if they agreed.
        for label, attested, actual in (
            (f"annotator {batch['annotator_slot']} output", str(annotator["output_sha256"]), annotations_digest),
            (f"reviewer {batch['reviewer_slot']} output", str(reviewer["output_sha256"]), reviews_digest),
            (f"reviewer {batch['reviewer_slot']} input", str(reviewer["annotator_file_sha256"]), annotations_digest),
        ):
            if attested != actual:
                print(f"{label}: attested {attested}, committed bytes are {actual}", file=sys.stderr)
                return 2

        annotator_attested_input = annotator.get("input_sha256")
        if annotator_attested_input is not None and str(annotator_attested_input) != input_digest:
            print(f"annotator {batch['annotator_slot']} attested input {annotator_attested_input}, "
                  f"committed bytes are {input_digest}", file=sys.stderr)
            return 2
        input_provenance = (
            "first-person: the annotator attested this input digest itself"
            if annotator_attested_input is not None else
            "orchestrator-recorded: the annotator's attestation carried no input digest, and a "
            "process that has exited cannot attest to one. This is the digest of the committed "
            "batch input that the batch plan assigned to it, not a claim by the annotator."
        )

        if annotations_path.as_posix() not in already:
            written.append(_access_ledger.append(
                ledger_path,
                actor=str(annotator["actor"]),
                timestamp_utc=str(annotator["attested_at_utc"]),
                command_tool_class=(
                    "local source reading and plain text search inside the pinned cobra checkout "
                    "(Section 8 step 4 answerability annotation); no retrieval, no network"
                ),
                input_artifact=input_path.as_posix(),
                input_sha256=input_digest,
                output_artifact=annotations_path.as_posix(),
                output_sha256=annotations_digest,
                detail=(
                    f"Annotated batch {number} ({len(annotator['assigned_issue_numbers'])} sealed "
                    f"candidates) against the pinned checkout, HEAD verified as "
                    f"{annotator['checkout_head_verified']}. {BACKFILL_REASON} "
                    f"Input digest provenance - {input_provenance}"
                ),
                backfilled=True,
                attestation=annotator_attestation.as_posix(),
                attestation_sha256=_access_ledger.sha256_file(annotator_attestation),
                input_digest_provenance=input_provenance,
            ))

        if reviews_path.as_posix() not in already:
            written.append(_access_ledger.append(
                ledger_path,
                actor=str(reviewer["actor"]),
                timestamp_utc=str(reviewer["attested_at_utc"]),
                command_tool_class=(
                    "local source reading and plain text search inside the pinned cobra checkout "
                    "(Section 4 independent review of annotated spans and rejections); no "
                    "retrieval, no network"
                ),
                input_artifact=annotations_path.as_posix(),
                input_sha256=annotations_digest,
                output_artifact=reviews_path.as_posix(),
                output_sha256=reviews_digest,
                detail=(
                    f"Independently reviewed batch {number} against the pinned checkout, HEAD "
                    f"verified as {reviewer['checkout_head_verified']}, re-reading every cited "
                    f"span rather than accepting the annotator's description. {BACKFILL_REASON} "
                    "Input digest is the annotator output digest this reviewer attested, "
                    "re-verified here against the committed bytes."
                ),
                backfilled=True,
                attestation=reviewer_attestation.as_posix(),
                attestation_sha256=_access_ledger.sha256_file(reviewer_attestation),
                input_digest_provenance="first-person: the reviewer attested this input digest itself",
            ))

    # Exit check. Whatever this run did or skipped, no committed row may pin an attestation
    # digest that does not resolve. This is what makes the two invocation orders equivalent in
    # their observable result rather than only in their intent.
    exit_stale = stale_attestation_digests()
    if exit_stale:
        for problem in exit_stale:
            print(problem, file=sys.stderr)
        print(
            f"{len(exit_stale)} ledger row(s) now pin an attestation digest that does not "
            "resolve; refusing to report success over a record that contradicts the files it "
            "points at.",
            file=sys.stderr,
        )
        return 2

    print(json.dumps(
        [{"sequence": row["sequence"], "actor": row["actor"], "output_artifact": row["output_artifact"]}
         for row in written], ensure_ascii=False, indent=2))
    print(f"{len(written)} ledger rows appended")
    print(f"{len(ledger_rows())} ledger rows; every attestation digest re-resolved against the "
          "committed bytes")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
