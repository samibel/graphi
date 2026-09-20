# Candidate-bound product compact development capture

This directory is reserved before the candidate freeze. After that commit is
clean, `TestRecoveryDevCapture` writes two independent production MCP captures
here while binding the observed worktree to the frozen candidate SHA. It uses
only the committed development slice and the clean pinned Cobra checkout; it
does not open the sealed holdout.

The preregistered dependencies are the production embedder
`static:potion-code-16M-v2@e9d2a44ca6a05ac6685f3b23709ea57eb7352d5b` and the governed real tokenizer
`tiktoken:cl100k_base:ordinary`.

The capture command and measured result are recorded in `RESULT.md` after the
run. This directory is development evidence, never a release decision.
