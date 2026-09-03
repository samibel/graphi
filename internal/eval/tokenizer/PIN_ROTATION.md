# Real-tokenizer pin rotation governance

Current governed tokenizer: `tiktoken:cl100k_base:ordinary`.
Current governed file: `cl100k_base.tiktoken`.
Current governed source: `https://openaipublic.blob.core.windows.net/encodings/cl100k_base.tiktoken`.
Current governed vocabulary SHA-256: `223921b76ee99bde995b7ff738513eef100fb51d18c93597a113bcffe865b2a7`.

This is the review record beside `PinnedVocabularySHA256` in `pins.go`. The
`TestTokenizer_PinRotationGovernance` gate requires the current digest above to
match the Go pin. Changing only the pin therefore fails the ordinary Go test
gate.

## Approval

A repository maintainer responsible for the SW-266 token-savings evaluation
approves a rotation in the pull request after reviewing its evidence. The
appended rotation entry must name that approver and pull request; merely saying
that an upstream vocabulary is newer is not approval evidence.

## Required rotation record and re-measurement

Before a tokenizer ID, counting policy, pre-tokenizer, or vocabulary digest may
replace the current pin, append a dated entry containing all of the following:

1. The old and new `tokenizer_id`, source URL, file name, exact SHA-256, upstream
   reason for rotating, approving maintainer, and pull request.
2. CGo-free conformance against the upstream tokenizer implementation for the
   complete golden token vectors: token IDs as well as counts, including the
   identifier, punctuation-dense code, long-path, and non-ASCII UTF-8 cases.
3. A fresh corruption, truncation, and missing-artifact run showing the loader
   fails closed and names the expected and actual SHA-256 values.
4. Regenerated raw payload token counts for every invalidated token-savings run
   directory. Counts, aggregates, confidence interval, and any claim sentence
   derived from an older tokenizer are stale; they may not be relabeled.
5. Review of the ordinary-text special-token policy and the frozen cl100k
   pre-tokenizer. A code-only behavior change invalidates measurements just as
   a vocabulary-file change does and requires a new `tokenizer_id`.

## Records made stale by the next rotation

No token-savings run directories exist at adoption time (SW-277).

The governance test scans every directory immediately below
`docs/eval/retrieval/runs/` for the current `tokenizer_id`. Once a run contains
real-token counts, its exact directory must be added here in backticks before
the gate will pass. This makes the inventory grow with evidence rather than
letting a later pin silently inherit old counts.

## Current-pin adoption record

SW-277 adopts OpenAI's canonical `cl100k_base.tiktoken` vocabulary at SHA-256
`223921b76ee99bde995b7ff738513eef100fb51d18c93597a113bcffe865b2a7`.
The implementation is a standard-library-only Go pre-tokenizer and byte-pair
encoder. It interprets payloads as ordinary text, not as a channel that may
inject tokenizer special tokens. This adoption publishes no measurement and
invalidates no prior run because no SW-266 token-savings run exists yet.
