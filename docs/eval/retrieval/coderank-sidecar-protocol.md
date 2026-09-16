# Development-only CodeRank sidecar contract

## Example safety

[`coderank-sidecar-manifest.example.json`](coderank-sidecar-manifest.example.json)
is a non-live template. Its all-zero digest sentinels are intentionally rejected.
Before use, operators must replace every digest with locally computed SHA-256
values and every `REPLACE-...` label with the exact immutable model/tokenizer
revision or runtime version. No live artifact revision or runtime version is
implied by the example. The instruction digest covers the exact UTF-8 instruction,
including its final space.

The example's 8192 usable tokens and zero special-token reserve are illustrative.
The qualifying manifest must use the verified tokenizer/runtime's actual usable
limit and reserve; use a positive reserve when preparation needs special-token
headroom. Manifest validation accepts a positive usable limit and a nonnegative
reserve, but does not establish the underlying model's context capacity.

Loading a manifest does not download artifacts, launch a process, resolve DNS,
or register a public embedder selector. The endpoint accepts only HTTP origins
on `localhost`, literal IPv4 `127.0.0.0/8`, or literal IPv6 `::1`, without
credentials, path, query, or fragment. A transport must map `localhost` directly
to loopback without DNS resolution.

## Durable identity encoding

The SHA-256 input is a fixed ordered sequence of fields. Each field is encoded as
`<UTF-8-byte-length>:<value>`, with decimal byte lengths and one newline between
fields, without a final newline. Integers use decimal notation. The field order is:

1. `schema_version`, `protocol`.
2. `model.id`, `model.revision`, `model.sha256`.
3. `tokenizer.id`, `tokenizer.revision`, `tokenizer.sha256`.
4. `runtime.name`, `runtime.version`, `runtime.sha256`.
5. `dimension`, `precision`, `normalization`, `compute`.
6. `admission.max_tokens`, `admission.reserve`, `admission.algorithm`, `admission.algorithm_version`.
7. `query.id`, `query.version`, `query.instruction`, `query.instruction_sha256`.

The lowercase hexadecimal digest excludes the endpoint and process epoch.
Every attestation, admission, and embedding response carries `protocol`,
`identity_digest`, and `epoch`; the adapter verifies the binding before accepting
data. JSON decoding rejects unknown response fields and trailing JSON values.

## HTTP operations

The adapter uses `GET /v1/attestation`, `POST /v1/admit`, and `POST /v1/embed`.
All operations require HTTP 200; redirects are rejected without following them.
Admission responses must include an integer `token_count`; missing and `null`
counts are rejected. The sidecar owns the authoritative tokenizer. The adapter
checks the returned count is nonnegative and within the manifest's usable limit,
and checks that admitted text is an unchanged UTF-8 prefix; it does not recompute
the token count locally. Documents are sent unchanged. GrapHi's `EmbedQuery`
prepends the pinned instruction once and sends `kind=query`. The sidecar requires
that exact leading instruction and encodes the supplied bytes unchanged. Direct
clients must prepare queries identically. A second textual occurrence is preserved
because it may be part of the user's original query.

Protocol `graphi-coderank/2` requires every `/v1/embed` response to include
`unknown_token_counts` with exactly one nonnegative integer per returned vector.
Missing, null, negative, or cardinality-mismatched counts are rejected. The
reference sidecar derives each count from the same `input_ids` and
`attention_mask` passed to the model forward call; inactive padding positions
are excluded. A tokenizer without an unknown-token ID reports an observed zero.
For `kind=query`, the count covers the exact prepared model input, including the
pinned query instruction and tokenizer-added special tokens.

Construction pins the serving epoch. Later attestation, admission, and embedding
responses must retain that epoch, protocol, and full identity digest. Empty
document batches return an empty result without a request. `CheckAvailable`
validates the pinned local state without dialing; query and generation operations
perform their runtime freshness checks through `VerifyRuntime`.

## Reference sidecar lifecycle

`scripts/eval/coderank_sidecar.py` is an evaluation-only Python 3.11+ tool. It
does not activate a product embedder or change any default. GrapHi never launches
the sidecar and never downloads its artifacts. Operators must provision an already
trusted local artifact tree and an already installed, pinned Python runtime
separately; these commands install or fetch nothing.

After replacing the example manifest's placeholders with verified local pins:

```bash
python3 scripts/eval/coderank_sidecar.py verify --manifest /absolute/path/coderank.json --model-dir /absolute/path/CodeRankEmbed
python3 scripts/eval/coderank_sidecar.py serve --manifest /absolute/path/coderank.json --model-dir /absolute/path/CodeRankEmbed --bind 127.0.0.1 --port 8765
curl --fail --silent http://127.0.0.1:8765/v1/attestation
```

Run `serve` in a separate operator-managed terminal. Stop it with Ctrl-C. Any
restart creates a new random process epoch and requires a new adapter instance,
even when the artifact identity is unchanged. `verify` checks the manifest,
artifact hashes, and installed runtime versions without importing
SentenceTransformers or loading weights. It emits the durable identity on success
and exits nonzero on failure. `serve` repeats verification before importing the
runtime, sets Hugging Face/Transformers offline flags, and uses
`SentenceTransformer(local_absolute_path, device="cpu", trust_remote_code=True,
local_files_only=True)`. Relative paths, model hub IDs, URLs, symlink artifact
roots, symlinks inside the tree, and special files are rejected. OS ancestor
aliases such as macOS `/var` are resolved to a canonical local path.

Before importing the runtime, verification scans local JSON configuration files
(filenames containing `config`) recursively for `auto_map`. Every target must be
a dotted local module/class reference, and the module's `.py` file must exist
beside that configuration (or in its dotted subpackage) inside the verified tree.
External-repository `repo--module.Class` references, URLs, absolute paths, path
separators, traversal, and missing modules are rejected before any inference
import. Tokenizer target lists may contain a null fast/slow alternative when
another target is present. Tokenizer vocabulary files are not configuration:
a vocabulary token named `auto_map` does not name executable code.

Every `modules.json` is also checked before inference imports. Each module `type`
must either name a class under the pinned runtime's `sentence_transformers.*`
namespace or name custom Python source inside the verified tree. Module `path`
values must resolve to existing directories inside that tree; external repository
syntax, URLs, absolute paths, and parent traversal are rejected. An empty module
path refers to the directory containing its `modules.json`.

`trust_remote_code=True` executes locally pinned model code. Offline flags and
`local_files_only=True` disable supported library downloads; they are not an
operating-system sandbox for Python code. Operators must review the model code,
configuration, and installed runtime before execution and keep the tree immutable
while the process runs. The contract protects against accidental drift rather than a lying
process. Hashes and self-attestation do not establish trust in an untrusted
process or defend against concurrent local artifact replacement.

The server binds only literal `127.0.0.1` or `::1` (stricter than the adapter's
allowed endpoint origins), caps bodies at 1 MiB and embedding batches at 32 texts,
rejects unknown/duplicate JSON keys, null or invalid text, unsupported protocols,
and unsupported kinds, and returns JSON errors with the same process binding.
Errors do not expose stack traces. It provides no authentication and should be
used only on a trusted evaluation workstation. Inference is serialized.

Admission tokenizes with special tokens and truncation disabled. It examines
Unicode character boundaries from longest to shortest and returns the first
prefix whose exact prepared count is at most `admission.max_tokens`. This
preserves UTF-8 bytes and handles token counts that can decrease when text
completes a merged token. The already-usable limit is not reduced by `reserve`
again. If even the empty preparation cannot fit, admission fails. This reference
algorithm can be slow for long over-limit inputs; it is intended for bounded
evaluation workloads. Embedding tokenizes once with truncation disabled, checks
the full prepared count, derives unknown-token counts from those same active
features, and passes the features directly through the model's forward path. It
validates cardinality, 768-dimensional finite vectors, and nonzero norms before
L2 normalization.

## Reproducing local digest pins

All digest framing below uses the same length-prefixed UTF-8 field encoding
described above: decimal byte length, colon, value, newline between fields,
no final newline. SHA-256 output is lowercase hexadecimal.

`model.sha256` fingerprints every regular file in the model directory, recursively.
Sort the relative POSIX paths by UTF-8 bytes. For each file append two fields:
its relative path and the SHA-256 of its raw contents. Hash the framed sequence.
No files are excluded; changing code, configuration, or weights changes the pin.
Empty directories do not contribute. Use an immutable snapshot with no caches
inside it, and keep the manifest outside the model directory.

`tokenizer.sha256` uses the same two-field-per-file encoding over the existing
root-level tokenizer files in this exact allowlist (sorted by UTF-8 path bytes):
`added_tokens.json`, `merges.txt`, `sentencepiece.bpe.model`,
`special_tokens_map.json`, `spiece.model`, `tokenizer.json`, `tokenizer.model`,
`tokenizer_config.json`, `vocab.json`, and `vocab.txt`. At least one vocabulary or
tokenizer model file must exist. Nested/custom tokenizer code is also covered by
the full model tree digest. A layout using other tokenizer filenames must be
reviewed before extending this reference implementation.

`runtime.name` must be `sentence-transformers`; `runtime.version` must match that
installed distribution exactly. `runtime.sha256` fingerprints the exact versions
of `python` (from `platform.python_version()`), `sentence-transformers`,
`transformers`, `torch`, `tokenizers`, `huggingface-hub`, `safetensors`, and `numpy`
(from installed package metadata). Sort names lexicographically and append each
name followed by its version as two framed fields. This is a reproducible version
inventory, not a hash of installed package bytes or the complete dependency
closure; operators remain responsible for trusted runtime installation.

Compute local values without importing the inference runtime:

```bash
python3 - <<'PY'
from pathlib import Path
from scripts.eval.coderank_sidecar import (
    QUERY_INSTRUCTION, runtime_digest, runtime_versions, tokenizer_digest, tree_digest,
)
import hashlib
import json

root = Path("/absolute/path/CodeRankEmbed")
versions = runtime_versions()
print(json.dumps({
    "model.sha256": tree_digest(root),
    "tokenizer.sha256": tokenizer_digest(root),
    "runtime.version": versions["sentence-transformers"],
    "runtime.sha256": runtime_digest(versions),
    "runtime_inventory": versions,
    "query.instruction_sha256": hashlib.sha256(QUERY_INSTRUCTION.encode("utf-8")).hexdigest(),
}, indent=2))
PY
```

Recording a digest pins observed bytes or versions; it does not independently
verify their provenance. Review provenance before using these pins in an
evaluation manifest. No live model has been qualified by the fake-encoder tests.

## Offline contract tests

```bash
python3 -m unittest scripts.eval.tests.test_coderank_sidecar -v
python3 -m py_compile scripts/eval/coderank_sidecar.py scripts/eval/tests/test_coderank_sidecar.py
```

The tests use Python's standard library and a fake encoder, including HTTP
requests to a temporary loopback port. They require no SentenceTransformers
installation, model weights, or external network access.
