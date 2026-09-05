# FINDING — the preserved `task_context/2` payload is not byte-reproducible across index builds

Found: 2026-09-05, while capturing the bundles for the SW-280 qrel-blind smoke evaluation.
Severity: does not affect this slice's result. **Does affect SW-281**, whose claim is charged in the
tokenizer counts this defect moves.

## What was observed

The capture was run three times over the same pinned cobra checkout (`a0a6ae0`), the same dataset
(`cobra-v2`, sha256 `7de5ce6e…`), the same candidate commit and the same production static embedder.
Comparing two of those runs, for all 64 answerable holdout queries:

| property | result |
|---|---|
| byte count | identical for all 64 |
| `whitespace-fields-v1` token count | identical for all 64 |
| SHA-256 of the preserved payload | **different for all 64** |
| `tiktoken:cl100k_base:ordinary` token count | **different for all 64** |

## The cause, located exactly

The only differing bytes are a 32-character lowercase hex field inside the `task_context/2` summary
line, in the fingerprint block, immediately after `\n0:\n32:`. For `cb-06` the two runs read:

```text
…\n3:256\n2:v3\n0:\n32:2eaf3d79954b776888e933a792a1ca59; 509/1200 snippet tokens; strategy semantic_first; …
…\n3:256\n2:v3\n0:\n32:46fd864cb5fa65938d2250cc64523cc2; 509/1200 snippet tokens; strategy semantic_first; …
```

Thirty differing byte positions in a 15,818-byte payload, all inside that one hex field. Everything
else — every item, every evidence citation, every snippet, and the engine's own
`509/1200 snippet tokens` accounting — is byte-identical. The field is a per-index-build identity,
and it changes because the index was rebuilt, not because retrieval behaved differently.

## Why the token count moves even though the byte count does not

The two hex strings are the same length, so byte counts and whitespace-field counts cannot notice
them. A byte-pair encoder can and does: `2eaf3d79954b776888e933a792a1ca59` and
`46fd864cb5fa65938d2250cc64523cc2` do not merge into the same number of BPE tokens. Every one of the
64 real-tokenizer counts changed.

## Why it does not affect this evaluation

The qrel-blind smoke evaluation asks whether a rater can answer from the bundle it was given. The
bundles it was given are the ones preserved in `bundles/`, content-addressed in
`pre-registration.json` before any response existed, and byte-identical to what each rater received.
The pass count is not a token measurement and does not enter the token estimand
(`docs/eval/retrieval/methodology.md`, "Estimand and claim boundary").

## Why SW-281 has to deal with it

SW-281 charges the savings claim in `tiktoken:cl100k_base:ordinary` tokens computed from exactly
these preserved bytes. As things stand, rebuilding the index changes every candidate token count by
a small, unpredictable amount, so:

- the candidate arm's per-query token count is not reproducible from the same inputs;
- a payload digest recorded in a report identifies one index build, not one candidate version;
- `-aggregate` reproduction of a committed run will disagree with a fresh capture even when nothing
  the method names has changed.

The magnitude is small — a 32-hex string is on the order of ten BPE tokens — but the measurement
contract's payload rule is exact-byte, not approximately-byte, and the reproducibility clause is
part of the confidence method. This is a defect to fix or to state, not one to average away.

## Options, not decided here

1. Make the index-build identity deterministic for a fixed corpus, embedder and candidate.
2. Exclude the fingerprint block from the payload boundary — which contradicts SW-274's rule that
   the actor-visible response bytes are the measured object, and would need a contract version.
3. Keep it and state it: report the candidate token count together with the index build it was
   captured under, and declare the measurement reproducible only up to that identity.

Option 2 changes the frozen method and cannot be taken inside a slice. Option 1 or 3 is SW-281's
call, and the owner's.
