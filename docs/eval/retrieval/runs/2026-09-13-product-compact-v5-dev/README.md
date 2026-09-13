# Public-version-bound product compact development capture

This directory is reserved before the next candidate freeze. The recapture
uses only the committed development slice and a clean Cobra checkout at
`a0a6ae020bb3899ff0276067863e50523f897370`; it never opens the sealed holdout.

The preregistered dependencies are the production embedder
`static:potion-code-16M-v2@e9d2a44ca6a05ac6685f3b23709ea57eb7352d5b` and the governed real tokenizer
`tiktoken:cl100k_base:ordinary`.

This fifth product capture verifies that actor-visible payloads carry
`task_context/2-compact/4` and retains the sealed source-snapshot boundaries.
