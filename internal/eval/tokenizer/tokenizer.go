// Package tokenizer preserves the evaluator-facing import path for the
// governed cl100k tokenizer. The CGo-free implementation lives in core so
// production engine code can enforce the same exact wire budget without an
// engine-to-evaluation dependency.
package tokenizer

import coretokenizer "github.com/samibel/graphi/core/tokenizer"

type Tokenizer = coretokenizer.Tokenizer
type PinMismatchError = coretokenizer.PinMismatchError

const (
	TokenizerID            = coretokenizer.TokenizerID
	PinnedVocabularySHA256 = coretokenizer.PinnedVocabularySHA256
	PinnedVocabularyFile   = coretokenizer.PinnedVocabularyFile
	PinnedVocabularyURL    = coretokenizer.PinnedVocabularyURL
)

var (
	PinnedSHA256    = coretokenizer.PinnedSHA256
	PinnedFileNames = coretokenizer.PinnedFileNames
)

func LoadEmbedded() (*Tokenizer, error)   { return coretokenizer.LoadEmbedded() }
func LoadPinned() (*Tokenizer, error)     { return coretokenizer.LoadPinned() }
func ArtifactDir() string                 { return coretokenizer.ArtifactDir() }
func Load(dir string) (*Tokenizer, error) { return coretokenizer.Load(dir) }
func Identity() (string, string)          { return coretokenizer.Identity() }
func DescribePin() string                 { return coretokenizer.DescribePin() }
