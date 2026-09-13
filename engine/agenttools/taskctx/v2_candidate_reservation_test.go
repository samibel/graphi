package taskctx

import (
	"fmt"
	"testing"

	"github.com/samibel/graphi/engine/agenttools/resolve"
)

func TestSelectRetrievalRowsReservesExactQueryBasenameBeyondPool(t *testing.T) {
	rows := make([]resolve.RetrieverRow, 0, candidatePoolLimit+1)
	for i := 0; i < candidatePoolLimit; i++ {
		candidatePath := fmt.Sprintf("docs/topic-%02d.md", i)
		if i == 0 {
			// One query term is already represented, but that must not hide a
			// different exact basename (man.md) immediately outside the pool.
			candidatePath = "command.go"
		}
		rows = append(rows, resolve.RetrieverRow{
			NodeID: fmt.Sprintf("filler-%02d", i),
			Path:   candidatePath,
		})
	}
	rows = append(rows, resolve.RetrieverRow{NodeID: "manual", Path: "site/content/docgen/man.md"})

	got := selectRetrievalRowsForTask("how to generate man pages for a command tree", rows, candidatePoolLimit)
	if len(got) != candidatePoolLimit {
		t.Fatalf("selected %d rows, want %d", len(got), candidatePoolLimit)
	}
	for i := 0; i < retrievalSeedLimit; i++ {
		if got[i].NodeID != rows[i].NodeID {
			t.Fatalf("primary row %d changed from %q to %q", i, rows[i].NodeID, got[i].NodeID)
		}
	}
	if got[len(got)-1].NodeID != "manual" {
		t.Fatalf("deep exact-basename row was not reserved: last=%+v", got[len(got)-1])
	}
}

func TestSelectRetrievalRowsLeavesOrdinaryRankingUnchanged(t *testing.T) {
	rows := make([]resolve.RetrieverRow, 0, candidatePoolLimit+1)
	for i := 0; i < candidatePoolLimit+1; i++ {
		rows = append(rows, resolve.RetrieverRow{
			NodeID: fmt.Sprintf("row-%02d", i),
			Path:   fmt.Sprintf("pkg/result-%02d.go", i),
		})
	}

	got := selectRetrievalRowsForTask("where are required flags validated", rows, candidatePoolLimit)
	for i := range got {
		if got[i].NodeID != rows[i].NodeID {
			t.Fatalf("row %d changed without an exact basename match: got %q want %q", i, got[i].NodeID, rows[i].NodeID)
		}
	}
}
