# Task 5 report: evaluation-only CodeRank reference sidecar

## Scope

Added `scripts/eval/coderank_sidecar.py`, its standard-library fake-encoder tests,
and extended the existing protocol document. No default/product activation,
GrapHi process launch, inference package installation, or live model download/load.

The sidecar verifies the local model tree, tokenizer artifacts, and runtime version
inventory before importing SentenceTransformers. It implements the three bounded
loopback endpoints with strict payloads, stable identity/epoch binding, exact UTF-8
prefix admission, explicit no-truncation tokenization, and normalized vectors.

Controller clarification overrides the brief's raw-query example: Go owns query
instruction preparation. The sidecar requires its presence and preserves wire
bytes, including a repeated instruction if it was original query content.

## TDD and verification evidence

- RED: `python3 -m unittest scripts.eval.tests.test_coderank_sidecar -v` failed
  before implementation with `ImportError: cannot import name 'coderank_sidecar'
  from 'scripts.eval'`; one failed import, exit 1.
- First implementation run: 18 tests; four HTTP setup errors from sandbox-denied
  loopback binds, one artifact assertion failure and one model-loading setup error
  because macOS `/var` is an ancestor symlink. The remaining 12 tests passed.
- Corrected local-directory handling to resolve OS ancestor aliases while still
  rejecting a symlink artifact root or symlinks within the artifact tree. The
  constructor-boundary test expects the canonical absolute path.
- GREEN: controller supplied approved execution evidence for
  `python3 -m unittest scripts.eval.tests.test_coderank_sidecar -v`:
  **18 tests in 2.171s, OK**. The subagent's own escalated attempts were interrupted
  during approval waiting; they are not represented as successful runs.
- GREEN: `python3 -m py_compile scripts/eval/coderank_sidecar.py
  scripts/eval/tests/test_coderank_sidecar.py` and `git diff --check` exited 0
  with no output against the final source, tests, and protocol document.

## Self-review and limitations

- No model/runtime libraries are imported at module import time or by `verify`.
- Loading sets offline flags before importing SentenceTransformers, passes the
  canonical absolute directory, CPU, trusted code, and `local_files_only=True`.
- Model/tokenizer tree framing and runtime inventory inputs are documented and
  reproducible. Runtime SHA is explicitly a version fingerprint, not installed
  code-byte attestation or a dependency-lock security guarantee.
- Admission uses descending character boundaries because token counts need not
  be monotonic; this guarantees the longest exact prefix but can be expensive for
  a long over-limit request. HTTP bodies and embedding batch sizes are bounded.
- Query text is neither prefixed nor stripped by the sidecar. Document text is
  passed directly to tokenization. Special-token prepared counts are authoritative
  and reserve is not subtracted twice.
- The shared Go/Python identity golden is absent, per the controller's bounded
  finish instruction. Python implements the documented ordered length framing;
  cross-language golden coverage remains an explicit follow-up concern.
- Fake tests establish the service contract, not live CodeRank compatibility.
  The direct tokenizer/model-forward path still requires real-model qualification.
- Trusted custom code is not sandboxed. The follow-up below closes configuration
  redirects through `auto_map`; reviewed local Python code and installed runtime
  packages still retain their normal process privileges.

## Required follow-up: local custom-code confinement

The controller identified external `auto_map` references as a binding-contract
gap and required closing it after the initial commit `fe5b51fb`.

- Added pre-import scanning of local JSON configuration files (filenames
  containing `config`), recursively inspecting `auto_map` values. Targets must
  be dotted module/class references backed by a `.py` file inside the verified
  artifact tree, relative to the containing configuration. External repository
  references, URLs, absolute paths, traversal, path separators, and missing local
  modules fail closed. Standard nullable tokenizer alternatives remain supported.
- RED: the focused unittest command ran **20 tests in 2.177s**, with **9 failed
  subtests**, each exposing an attempted SentenceTransformers import before
  rejecting an invalid custom-code reference. The import guard prevented loading
  any actual inference package.
- GREEN: the same command ran **20 tests in 2.161s, OK** after implementation.
- Self-review: validation runs after artifact digest verification and before
  runtime verification/import, uses strict JSON decoding, inspects nested maps
  and all list alternatives, and requires a real local module. Tokenizer vocabulary
  JSON is excluded because its tokens can include the literal `auto_map`.
- No live model or package was fetched, installed, or loaded. The identity golden
  and real forward-path qualification remain the previously documented concerns.

## Files

- `scripts/eval/coderank_sidecar.py`
- `scripts/eval/tests/test_coderank_sidecar.py`
- `docs/eval/retrieval/coderank-sidecar-protocol.md`
- `.superpowers/sdd/2026-09-16-embedded-model-dev-qualification/task-5-report.md`
