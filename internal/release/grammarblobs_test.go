package release

import (
	"reflect"
	"sort"
	"strings"
	"testing"
)

// TestExpectedGrammarBlobs_DerivedFromTags asserts the blob set is derived from
// DefaultGrammarSubsetTags: one grammar_blobs/<lang>.bin per grammar_subset_<lang>
// tag, the umbrella "grammar_subset" tag excluded, sorted and deduped. This keeps
// the release-gate expectation in lock-step with the source of truth so adding a
// tier-1 language cannot silently drift the gate.
func TestExpectedGrammarBlobs_DerivedFromTags(t *testing.T) {
	got := ExpectedGrammarBlobs()

	var want []string
	for _, tag := range DefaultGrammarSubsetTags {
		lang := strings.TrimPrefix(tag, "grammar_subset_")
		if lang == tag || lang == "" {
			continue // umbrella tag, no blob
		}
		want = append(want, "grammar_blobs/"+lang+".bin")
	}
	sort.Strings(want)

	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ExpectedGrammarBlobs() = %v, want %v", got, want)
	}
	if !sort.StringsAreSorted(got) {
		t.Errorf("not sorted: %v", got)
	}
	// The umbrella tag must never appear as a blob.
	for _, b := range got {
		if b == "grammar_blobs/.bin" || b == "grammar_blobs/grammar_subset.bin" {
			t.Errorf("umbrella tag leaked into blob set: %q", b)
		}
	}
	// TypeScript is the original SW-053 grammar; it must be present.
	found := false
	for _, b := range got {
		if b == "grammar_blobs/typescript.bin" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected grammar_blobs/typescript.bin in %v", got)
	}
}
