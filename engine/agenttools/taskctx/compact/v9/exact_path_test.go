package v9

import (
	"context"
	"fmt"
	"testing"
	"testing/fstest"

	"github.com/samibel/graphi/engine/agenttools/contract"
	"github.com/samibel/graphi/engine/agenttools/shape"
)

func TestExactPathOutlineCoversDeclarationSeparators(t *testing.T) {
	repository := fstest.MapFS{
		"outline.go": {Data: []byte("package outline\n\nconst (\n\tone = 1\n)\n\nvar two = 2\n")},
	}
	evidence, items, err := compactTaskContextHydrateExactPath(context.Background(), repository, nil, "outline.go")
	if err != nil {
		t.Fatal(err)
	}
	sources, _, err := compactTaskContextSelect("outline.go", evidence, items, 325)
	if err != nil {
		t.Fatal(err)
	}
	assertExactPathLinesCovered(t, sources, "outline.go", 3, 7)
}

func TestExactPathOutlineCoversBuildConstraints(t *testing.T) {
	repository := fstest.MapFS{
		"command_notwin.go": {Data: []byte("// Copyright Example\n\n//go:build !windows\n// +build !windows\n\npackage cobra\n\nvar preExecHookFn func(*Command)\n")},
	}
	evidence, items, err := compactTaskContextHydrateExactPath(context.Background(), repository, nil, "command_notwin.go")
	if err != nil {
		t.Fatal(err)
	}
	sources, _, err := compactTaskContextSelect("command_notwin.go", evidence, items, 325)
	if err != nil {
		t.Fatal(err)
	}
	assertExactPathLinesCovered(t, sources, "command_notwin.go", 3, 8)
}

func TestExactPathSelectionUsesFileOrderAfterDeduplication(t *testing.T) {
	lines := []int{100, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11}
	evidence := make([]contract.Evidence, 0, len(lines))
	items := make([]contract.Item, 0, len(lines))
	for index, line := range lines {
		ref := fmt.Sprintf("path-%d", line)
		snippet := fmt.Sprintf("var value%d = %d", line, line)
		evidence = append(evidence, contract.Evidence{
			RefID: ref, Path: "outline.go", Line: line, Span: fmt.Sprintf("%d-%d", line, line),
			Role: "snippet", Snippet: snippet, TextHash: shape.TextHash(snippet),
		})
		items = append(items, contract.Item{
			RefID: fmt.Sprintf("item-%d", index), Rank: index + 1,
			Reason:         fmt.Sprintf("path-declaration: variable outline.value%d (outline.go:%d) score 0", line, line),
			EvidenceRefIDs: []string{ref},
		})
	}
	sources, _, err := compactTaskContextSelect("outline.go", evidence, items, 325)
	if err != nil {
		t.Fatal(err)
	}
	assertExactPathLinesCovered(t, sources, "outline.go", 1, 10)
	for _, source := range sources {
		if source.Start <= 100 && source.End >= 100 {
			t.Fatalf("late declaration displaced the file outline: %+v", sources)
		}
	}
}

func assertExactPathLinesCovered(t *testing.T, sources []CompactTaskContextSource, path string, start, end int) {
	t.Helper()
	for _, source := range sources {
		if source.Path == path && source.Start <= start && source.End >= end {
			return
		}
	}
	t.Fatalf("%s:%d-%d is not covered by one coherent source: %+v", path, start, end, sources)
}
