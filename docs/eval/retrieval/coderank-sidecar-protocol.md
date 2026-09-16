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
the token count locally. Documents are sent unchanged. Query embedding prepends
the pinned instruction once and sends `kind=query`.

Construction pins the serving epoch. Later attestation, admission, and embedding
responses must retain that epoch, protocol, and full identity digest. Empty
document batches return an empty result without a request. `CheckAvailable`
validates the pinned local state without dialing; query and generation operations
perform their runtime freshness checks through `VerifyRuntime`.
