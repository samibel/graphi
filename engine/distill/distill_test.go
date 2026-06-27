package distill

import (
	"bytes"
	"context"
	"testing"
)

type testLedger struct {
	distills []int64
}

func (tl *testLedger) RecordDistill(ctx context.Context, savedTokens int64) error {
	tl.distills = append(tl.distills, savedTokens)
	return nil
}

func sampleTrace() SessionTrace {
	return SessionTrace{
		SessionID: "sess-abc",
		Turns: []Turn{
			{ID: "t1", Prompt: "add feature", FilesIn: []string{"a.go"}, FilesOut: []string{"a.go", "b.go"}},
			{ID: "t2", Prompt: "review", FilesIn: []string{"a.go"}, FilesOut: []string{"c.go"}},
		},
		Decisions:      []string{"use JSONL", "no LLM"},
		Risks:          []string{"determinism"},
		OpenQuestions:  []string{"surface parity?"},
		FileReferences: []string{"a.go", "a.go"},
	}
}

func TestDistillDeterminism(t *testing.T) {
	ctx := context.Background()
	d := NewDistiller(nil)
	trace := sampleTrace()

	var first []byte
	for i := 0; i < 3; i++ {
		art, err := d.Distill(ctx, trace)
		if err != nil {
			t.Fatalf("distill %d: %v", i, err)
		}
		data, err := Marshal(art)
		if err != nil {
			t.Fatalf("marshal %d: %v", i, err)
		}
		if first == nil {
			first = data
			continue
		}
		if !bytes.Equal(first, data) {
			t.Fatalf("run %d output differs:\n%s\nvs\n%s", i, first, data)
		}
	}
}

func TestDistillContent(t *testing.T) {
	ctx := context.Background()
	tl := &testLedger{}
	d := NewDistiller(tl)
	trace := sampleTrace()

	art, err := d.Distill(ctx, trace)
	if err != nil {
		t.Fatalf("distill: %v", err)
	}
	if art.SessionID != "sess-abc" {
		t.Fatalf("session id: %s", art.SessionID)
	}
	if len(art.Decisions) != 2 {
		t.Fatalf("expected 2 decisions, got %d", len(art.Decisions))
	}
	if len(art.FileReferences) != 3 {
		t.Fatalf("expected 3 file refs, got %d: %v", len(art.FileReferences), art.FileReferences)
	}
	if len(art.TouchedFiles) != 3 {
		t.Fatalf("expected 3 touched files, got %d", len(art.TouchedFiles))
	}
	if len(tl.distills) != 1 {
		t.Fatalf("expected 1 ledger entry, got %d", len(tl.distills))
	}
}

func TestLoadDistilled(t *testing.T) {
	ctx := context.Background()
	d := NewDistiller(nil)
	trace := sampleTrace()
	art, err := d.Distill(ctx, trace)
	if err != nil {
		t.Fatalf("distill: %v", err)
	}
	data, err := Marshal(art)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	loaded, err := LoadDistilled(data)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if loaded.SessionID != art.SessionID {
		t.Fatalf("loaded session id mismatch")
	}
	if len(loaded.Decisions) != len(art.Decisions) {
		t.Fatalf("loaded decisions mismatch")
	}
}

func TestMarshalKeyOrder(t *testing.T) {
	art := DistilledSession{
		Version:        ArtifactVersion,
		SessionID:      "s1",
		Summary:        "summary",
		Decisions:      []string{"d1"},
		Risks:          []string{"r1"},
		OpenQuestions:  []string{"q1"},
		FileReferences: []string{"f1"},
		TouchedFiles:   []string{"t1"},
	}
	data, err := Marshal(art)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	want := `{"version":"distill/v1","session_id":"s1","summary":"summary","decisions":["d1"],"risks":["r1"],"open_questions":["q1"],"file_references":["f1"],"touched_files":["t1"]}`
	if string(data) != want {
		t.Fatalf("unexpected marshal\n got: %s\nwant: %s", data, want)
	}
}
