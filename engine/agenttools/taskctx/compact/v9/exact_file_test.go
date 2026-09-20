package v9

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/samibel/graphi/engine/agenttools/contract"
	"github.com/samibel/graphi/engine/agenttools/shape"
)

func exactFileRepository() fstest.MapFS {
	license := make([]string, 300)
	for i := range license {
		license[i] = fmt.Sprintf("license line %d", i+1)
	}
	return fstest.MapFS{
		"go.mod":         {Data: []byte("module example.com/m\n\ngo 1.22\n\nrequire (\n\tx v1\n\ty v2\n)\n")},
		"Makefile":       {Data: []byte("BIN=./bin\n\n.PHONY: fmt test\n\nfmt:\n\tgofmt -l .\n\ntest:\n\tgo test ./...\n")},
		"LICENSE.txt":    {Data: []byte(strings.Join(license, "\n") + "\n")},
		"cmd/run.go":     {Data: followupGoFile(5)},
		"assets/x.bin":   {Data: []byte("a\x00b\n")},
		".github/ci.yml": {Data: []byte("on: push\n")},
	}
}

// TestExactFileHydratesAOneTokenPathTheSnapshotDoesNotScan pins the
// compact/14 rule: a one-token question that is the path of a repository
// file outside the .go/.md snapshot (a module file, a Makefile, a licence)
// is answered by that file itself, its first lines as one ranked region
// placed ahead of every retrieval row.
func TestExactFileHydratesAOneTokenPathTheSnapshotDoesNotScan(t *testing.T) {
	repository := exactFileRepository()
	items := []contract.Item{{RefID: "r1", Rank: 7 << 20, Reason: "candidate: function p.Run (cmd/run.go:4) score 3"}}
	evidence, linked, err := compactTaskContextHydrateExactFile(context.Background(), repository, "go.mod", items)
	if err != nil {
		t.Fatal(err)
	}
	if len(evidence) != 1 || len(linked) != 1 {
		t.Fatalf("hydration = %d evidence, %d items", len(evidence), len(linked))
	}
	if evidence[0].Path != "go.mod" || evidence[0].Span != "1-8" || evidence[0].Line != 1 || !strings.HasPrefix(evidence[0].Snippet, "module example.com/m") {
		t.Fatalf("evidence = %+v", evidence[0])
	}
	if linked[0].Rank <= items[0].Rank || !strings.HasPrefix(linked[0].Reason, "candidate: file go.mod (go.mod:1) score 0") || linked[0].EvidenceRefIDs[0] != evidence[0].RefID {
		t.Fatalf("item = %+v", linked[0])
	}
}

// TestExactFileIsCappedAndLeadsSelection: a long file is cut at
// FollowupMaxLines, and once hydrated the file leads the response whether
// the token is identifier-shaped ("Makefile") or not ("LICENSE.txt").
func TestExactFileIsCappedAndLeadsSelection(t *testing.T) {
	repository := exactFileRepository()
	for _, query := range []string{"Makefile", "LICENSE.txt"} {
		other := contract.Evidence{RefID: "r1", Path: "cmd/run.go", Line: 3, Span: "3-11", Role: "snippet", Snippet: strings.Join(strings.Split(string(followupGoFile(5)), "\n")[2:11], "\n")}
		other.TextHash = shape.TextHash(other.Snippet)
		retrieved := []contract.Item{{RefID: "r1-item", Rank: 7 << 20, Reason: "candidate: function p.Run (cmd/run.go:4) score 3", EvidenceRefIDs: []string{"r1"}}}
		evidence, linked, err := compactTaskContextHydrateExactFile(context.Background(), repository, query, retrieved)
		if err != nil || len(evidence) != 1 {
			t.Fatalf("%s: %v %d", query, err, len(evidence))
		}
		if query == "LICENSE.txt" && evidence[0].Span != fmt.Sprintf("1-%d", FollowupMaxLines) {
			t.Fatalf("long file span = %s", evidence[0].Span)
		}
		items := append(retrieved, linked...)
		sources, _, err := compactTaskContextSelect(query, append([]contract.Evidence{other}, evidence...), items, 325)
		if err != nil {
			t.Fatal(err)
		}
		if len(sources) == 0 || sources[0].Path != query || sources[0].Start != 1 {
			t.Fatalf("%s: lead source = %+v", query, sources)
		}
	}
}

// TestExactFileIsSupplementalAndNarrow: nothing is hydrated for a .go path
// (the outline handles those), a multi-word question, a missing file, a
// directory, a dot-directory, binary bytes, or a nil repository.
func TestExactFileIsSupplementalAndNarrow(t *testing.T) {
	repository := exactFileRepository()
	for _, query := range []string{"cmd/run.go", "go.mod and Makefile", "missing.txt", "cmd", ".github/ci.yml", "assets/x.bin", "../go.mod", ""} {
		evidence, linked, err := compactTaskContextHydrateExactFile(context.Background(), repository, query, nil)
		if err != nil || len(evidence) != 0 || len(linked) != 0 {
			t.Fatalf("%q hydrated %d evidence: %v", query, len(evidence), err)
		}
	}
	if evidence, _, err := compactTaskContextHydrateExactFile(context.Background(), nil, "go.mod", nil); err != nil || len(evidence) != 0 {
		t.Fatalf("nil repository hydrated %d: %v", len(evidence), err)
	}
}
