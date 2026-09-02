package client

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/samibel/graphi/core/graphstore"
	"github.com/samibel/graphi/engine/embed"
)

func TestSemanticStatus_AC1_FieldShape(t *testing.T) {
	reg := embed.NewRegistry()
	if err := reg.Register(embed.NewMockEmbedder(8)); err != nil {
		t.Fatal(err)
	}
	b, _, err := SemanticStatus(context.Background(), SemanticStatusOptions{Embedder: reg})
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(b, &doc); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"schema_version", "installed", "configured", "indexed", "fresh", "state", "model", "active_generation", "last_generation", "languages", "repair"} {
		if _, ok := doc[key]; !ok {
			t.Errorf("wire document missing %q: %s", key, b)
		}
	}
	if doc["schema_version"] != float64(SemanticStatusJSONSchemaVersion) {
		t.Errorf("schema_version=%v", doc["schema_version"])
	}
}

func TestSemanticStatus_AC5_GoValidatedOthersUnvalidated(t *testing.T) {
	b, _, err := SemanticStatus(context.Background(), SemanticStatusOptions{})
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Languages map[string]string `json:"languages"`
	}
	if err := json.Unmarshal(b, &doc); err != nil {
		t.Fatal(err)
	}
	if doc.Languages["go"] != "validated" {
		t.Fatalf("go=%q, want validated", doc.Languages["go"])
	}
	for language, validation := range doc.Languages {
		if language != "go" && validation != "unvalidated" {
			t.Errorf("%s=%q, want unvalidated", language, validation)
		}
	}
}

// TestSemanticStatus_UsesLiveGraphGeneration is the production-honesty pin:
// the generation built against index.commit_generation must read ready. Using
// the historical placeholder here makes this test report stale.
func TestSemanticStatus_UsesLiveGraphGeneration(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	dbPath := filepath.Join(root, "graph.sqlite")
	metaDir := filepath.Join(root, "meta")
	if err := os.MkdirAll(metaDir, 0o700); err != nil {
		t.Fatal(err)
	}
	graph, err := graphstore.OpenSQLite(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := graph.SetMetadata(ctx, "index.commit_generation", "graph-generation-7"); err != nil {
		t.Fatal(err)
	}
	if err := graph.Close(); err != nil {
		t.Fatal(err)
	}

	embedder := embed.NewMockEmbedder(8)
	reg := embed.NewRegistry()
	if err := reg.Register(embedder); err != nil {
		t.Fatal(err)
	}
	store, err := embed.OpenSQLiteGenerationStore(ctx, metaDir)
	if err != nil {
		t.Fatal(err)
	}
	build, err := store.Begin(ctx, embed.Fingerprint{ModelID: embedder.ID(), Dim: embedder.Dim(), DocumentSchema: embed.DocumentSchema, GraphGeneration: "graph-generation-7"})
	if err != nil {
		t.Fatal(err)
	}
	if err := build.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	_, state, err := SemanticStatus(ctx, SemanticStatusOptions{Root: root, DBPath: dbPath, MetaDir: metaDir, Embedder: reg})
	if err != nil {
		t.Fatal(err)
	}
	if state != embed.StateReady {
		t.Fatalf("state=%s, want ready; a placeholder graph generation reports stale", state)
	}
}

func TestSemanticStatus_NoGenerationIsReadOnly(t *testing.T) {
	metaDir := t.TempDir()
	reg := embed.NewRegistry()
	if err := reg.Register(embed.NewMockEmbedder(8)); err != nil {
		t.Fatal(err)
	}
	_, state, err := SemanticStatus(context.Background(), SemanticStatusOptions{MetaDir: metaDir, Embedder: reg})
	if err != nil {
		t.Fatal(err)
	}
	if state != embed.StateMissing {
		t.Fatalf("state=%s, want missing", state)
	}
	if _, err := os.Stat(filepath.Join(metaDir, "ingest-meta.db")); !os.IsNotExist(err) {
		t.Fatalf("status created or touched a missing sidecar: %v", err)
	}
}

func TestSemanticStatus_AC1_ActiveAndLastGeneration(t *testing.T) {
	ctx := context.Background()
	metaDir := t.TempDir()
	embedder := embed.NewMockEmbedder(8)
	reg := embed.NewRegistry()
	if err := reg.Register(embedder); err != nil {
		t.Fatal(err)
	}
	store, err := embed.OpenSQLiteGenerationStore(ctx, metaDir)
	if err != nil {
		t.Fatal(err)
	}
	fp := embed.Fingerprint{ModelID: embedder.ID(), Dim: embedder.Dim(), DocumentSchema: embed.DocumentSchema, GraphGeneration: embed.GraphGenerationPlaceholder}
	first, err := store.Begin(ctx, fp)
	if err != nil {
		t.Fatal(err)
	}
	firstID := first.ID()
	if err := first.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	second, err := store.Begin(ctx, fp)
	if err != nil {
		t.Fatal(err)
	}
	secondID := second.ID()
	if err := second.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	b, state, err := SemanticStatus(ctx, SemanticStatusOptions{MetaDir: metaDir, Embedder: reg})
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Active embed.GenerationSummary `json:"active_generation"`
		Last   embed.GenerationSummary `json:"last_generation"`
	}
	if err := json.Unmarshal(b, &doc); err != nil {
		t.Fatal(err)
	}
	if state != embed.StateReady || doc.Active.ID != string(secondID) || doc.Last.ID != string(firstID) {
		t.Fatalf("state=%s active=%q last=%q, want ready/%q/%q", state, doc.Active.ID, doc.Last.ID, secondID, firstID)
	}
	if doc.Active.BuiltAt == "" || doc.Last.BuiltAt == "" {
		t.Fatalf("built timestamps missing: active=%q last=%q", doc.Active.BuiltAt, doc.Last.BuiltAt)
	}
}
