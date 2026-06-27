package forge_test

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"

	"github.com/samibel/graphi/surfaces/forge"
)

// TestMockForge_NoNetwork (AC-1 / AC-5): the injectable MockForge returns the
// fixed PR set with NO network I/O, so the whole suite can be exercised offline.
func TestMockForge_NoNetwork(t *testing.T) {
	prs := []forge.PR{
		{Number: 2, Title: "b", Author: "bob", ChangedFiles: []string{"z.go", "a.go"}},
		{Number: 1, Title: "a", Author: "alice", ChangedFiles: []string{"x.go"}},
	}
	m := forge.NewMockForge(prs)
	got, err := m.ListOpenPRs(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("want 2 PRs, got %d", len(got))
	}
}

// TestMarshalPRList_Deterministic (AC-1): list_prs metadata is byte-stable —
// PRs sorted by number ASC, changed-file paths normalized + sorted, no scoring
// fields. Repeated marshals are byte-identical regardless of input order.
func TestMarshalPRList_Deterministic(t *testing.T) {
	a := []forge.PR{
		{Number: 2, Title: "b", ChangedFiles: []string{"z.go", "a.go"}},
		{Number: 1, Title: "a", ChangedFiles: []string{"x.go"}},
	}
	b := []forge.PR{ // reversed input order + duplicate file
		{Number: 1, Title: "a", ChangedFiles: []string{"x.go", "x.go"}},
		{Number: 2, Title: "b", ChangedFiles: []string{"a.go", "z.go"}},
	}
	ba, err := forge.MarshalPRList(a)
	if err != nil {
		t.Fatal(err)
	}
	bb, err := forge.MarshalPRList(b)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(ba, bb) {
		t.Fatalf("list_prs not order-independent:\nA: %s\nB: %s", ba, bb)
	}

	var rep forge.PRList
	if err := json.Unmarshal(ba, &rep); err != nil {
		t.Fatal(err)
	}
	if rep.Outcome != "found" || len(rep.PRs) != 2 {
		t.Fatalf("unexpected envelope: %+v", rep)
	}
	if rep.PRs[0].Number != 1 || rep.PRs[1].Number != 2 {
		t.Fatalf("PRs must be sorted by number ASC: %+v", rep.PRs)
	}
	// changed files normalized + sorted + deduped.
	if len(rep.PRs[0].ChangedFiles) != 1 {
		t.Fatalf("duplicate changed-file not deduped: %+v", rep.PRs[0].ChangedFiles)
	}
	// The list_prs envelope carries NO scoring fields (metadata only).
	if bytes.Contains(ba, []byte("composite")) || bytes.Contains(ba, []byte("blast_radius")) {
		t.Fatalf("list_prs must not contain any scoring fields: %s", ba)
	}
}

// TestMarshalPRList_Empty yields a stable empty envelope (never null).
func TestMarshalPRList_Empty(t *testing.T) {
	b, err := forge.MarshalPRList(nil)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(b, []byte(`"outcome":"empty"`)) || !bytes.Contains(b, []byte(`"prs":[]`)) {
		t.Fatalf("empty list_prs envelope must materialize prs:[] and outcome empty: %s", b)
	}
}
