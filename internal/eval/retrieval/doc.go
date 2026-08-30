// Package retrieval is the retrieval-quality evaluation harness (story
// SW-258). It runs real queries against a pinned repository through the real
// engine seams — engine/search.Service lexical search, search_hybrid/1 via
// engine/agenttools/hybridsearch, and Service.SemanticSearch — scores every
// ranking against graded source spans (0..3 with a reason and a reviewer),
// and emits a versioned JSON report whose every published number can be
// recomputed from exported raw samples (Reproduce; a discrepancy is an error).
//
// It is a SIBLING of internal/eval, not an extension of it: internal/eval is
// the static token-parity harness over prebuilt context strings and never
// executes a retrieval pipeline. The only thing shared is the whitespace
// tokenizer (eval.CountTokens), stamped here as TokenizerID.
//
// Layering: this package sits under internal/ and imports engine/* and core/*
// only — never surfaces/* or cmd/*. The cmd/retrieval-eval binary is the
// dispatch entry point; go test ./internal/eval/retrieval is the hermetic
// PR-time run over testdata/fixture-repo.
package retrieval
