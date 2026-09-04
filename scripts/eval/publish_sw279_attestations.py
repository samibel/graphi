#!/usr/bin/env python3
"""Rewrite absolute paths in the SW-279 attestations to repository-relative paths.

This is a public repository and a pre-commit guard refuses any staged file containing
the local username. The annotators and reviewers were given absolute paths and wrote
them back into their attestations, so the files as produced cannot be committed.

Nothing else is changed. Each file gains a `publication_note` recording the digest of
the bytes as the actor produced them, so a reader can tell the published bytes from the
produced ones and nothing is quietly rewritten. The digests the attestation *asserts*
(of the annotation or review file it covers) are untouched and still recompute.
"""

from __future__ import annotations

import argparse
import hashlib
import json
import re
import subprocess
import sys
from pathlib import Path


def main() -> int:
    ap = argparse.ArgumentParser(description=__doc__)
    ap.add_argument("paths", nargs="+")
    args = ap.parse_args()

    repo_root = Path(subprocess.run(["git", "rev-parse", "--show-toplevel"], check=True,
                                    stdout=subprocess.PIPE, text=True).stdout.strip())
    home = str(Path.home())
    rewritten = []
    for name in args.paths:
        path = Path(name)
        raw = path.read_bytes()
        text = raw.decode("utf-8")
        as_produced = hashlib.sha256(raw).hexdigest()

        new_text = text.replace(str(repo_root) + "/", "").replace(str(repo_root), ".")
        # the pinned corpus checkout lives under the user's home directory
        new_text = re.sub(re.escape(home) + r"/\.cache/graphi/corpus/cobra", "<cobra-checkout>", new_text)
        new_text = new_text.replace(home + "/", "<home>/").replace(home, "<home>")
        if new_text == text:
            continue

        document = json.loads(new_text)
        document["publication_note"] = (
            "Absolute paths were rewritten to repository-relative paths before this file was "
            "committed; this repository is public and a pre-commit guard refuses a staged file "
            "containing the local username. Nothing else was changed. sha256 of the bytes as the "
            "actor produced them: " + as_produced + "."
        )
        path.write_text(json.dumps(document, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")
        rewritten.append({
            "file": path.as_posix(),
            "sha256_as_produced": as_produced,
            "sha256_as_published": hashlib.sha256(path.read_bytes()).hexdigest(),
        })

    print(json.dumps({"rewritten": rewritten, "count": len(rewritten)}, ensure_ascii=False, indent=2))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
