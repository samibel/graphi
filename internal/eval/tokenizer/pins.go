// Package tokenizer owns the pinned real-tokenizer identity used by the
// SW-266 token-savings measurement. The pin table is the single source of
// truth for every file Load reads. Acquisition is deliberately elsewhere:
// cmd/graphi/staticfetch is the repository's already-allowlisted egress
// package, while this package only reads verified local bytes.
package tokenizer

const (
	// TokenizerID names both the upstream encoding and the counting policy.
	// Payloads are actor-visible ordinary text, so special-token spellings are
	// encoded as ordinary bytes rather than interpreted as control tokens.
	TokenizerID = "tiktoken:cl100k_base:ordinary"

	// PinnedVocabularySHA256 is the SHA-256 published for OpenAI's canonical
	// cl100k_base.tiktoken artifact. It is also the vocabulary identity that
	// accompanies every count in the measurement report.
	PinnedVocabularySHA256 = "223921b76ee99bde995b7ff738513eef100fb51d18c93597a113bcffe865b2a7"

	PinnedVocabularyFile = "cl100k_base.tiktoken"
	PinnedVocabularyURL  = "https://openaipublic.blob.core.windows.net/encodings/cl100k_base.tiktoken"
)

// PinnedSHA256 follows engine/embed/static's pin-table shape. Keep the map even
// though this tokenizer currently reads one file: adding a loader input without
// adding its digest must be an obvious code-review and test failure.
var PinnedSHA256 = map[string]string{
	PinnedVocabularyFile: PinnedVocabularySHA256,
}

// PinnedFileNames is the complete, ordered list of files Load requires.
var PinnedFileNames = []string{PinnedVocabularyFile}
