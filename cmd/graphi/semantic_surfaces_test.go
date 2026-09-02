package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"

	"github.com/samibel/graphi/core/graphstore"
	"github.com/samibel/graphi/engine/embed"
	"github.com/samibel/graphi/engine/query"
	"github.com/samibel/graphi/engine/search"
	clientpkg "github.com/samibel/graphi/surfaces/client"
	httpsurface "github.com/samibel/graphi/surfaces/http"
	"github.com/samibel/graphi/surfaces/mcp"
)

const semanticFixtureRepair = "graphi setup-embedder static:potion-code-16M-v2@e9d2a44ca6a05ac6685f3b23709ea57eb7352d5b"

type semanticFixtureEmbedder struct{ unavailable bool }

func (e semanticFixtureEmbedder) ID() string       { return "fixture:model@rev1" }
func (e semanticFixtureEmbedder) Dim() int         { return 2 }
func (e semanticFixtureEmbedder) Revision() string { return "rev1" }
func (e semanticFixtureEmbedder) ModelSHA256() string {
	return "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
}
func (e semanticFixtureEmbedder) Embed(_ context.Context, texts []string) ([][]float32, error) {
	return make([][]float32, len(texts)), nil
}
func (e semanticFixtureEmbedder) CheckAvailable(context.Context) error {
	if e.unavailable {
		return semanticFixtureUnavailable{}
	}
	return nil
}

type semanticFixtureUnavailable struct{}

func (semanticFixtureUnavailable) Error() string  { return "fixture model artifact absent" }
func (semanticFixtureUnavailable) Repair() string { return semanticFixtureRepair }

// semanticStatusSurfaceClient intentionally exposes only the base Client
// interface, so the MCP catalog is not narrowed by Direct's operation-handler
// capability report. semantic_status is composition-owned and does not invoke
// the wrapped client.
type semanticStatusSurfaceClient struct{ clientpkg.Client }

// TestSemanticStatus_FiveStateGoldensAcrossCLIMCPHTTP is the AC-2/AC-3
// byte proof. Each real surface receives the same store and is compared to an
// external pinned golden, including the exact repair command.
func TestSemanticStatus_FiveStateGoldensAcrossCLIMCPHTTP(t *testing.T) {
	t.Setenv(httpsurface.LabsEnvVar, "1")
	t.Setenv(embed.EnvSelector, "")
	cases := []struct {
		name  string
		setup func(*testing.T) (string, *embed.Registry)
	}{
		{name: "unconfigured", setup: func(t *testing.T) (string, *embed.Registry) { return "", embed.NewRegistry() }},
		{name: "model-absent", setup: func(t *testing.T) (string, *embed.Registry) { return "", semanticFixtureRegistry(t, true) }},
		{name: "no-generation", setup: func(t *testing.T) (string, *embed.Registry) { return t.TempDir(), semanticFixtureRegistry(t, false) }},
		{name: "stale", setup: func(t *testing.T) (string, *embed.Registry) {
			dir := t.TempDir()
			seedSemanticStatusFixture(t, dir, "stale")
			return dir, semanticFixtureRegistry(t, false)
		}},
		{name: "corrupt", setup: func(t *testing.T) (string, *embed.Registry) {
			dir := t.TempDir()
			seedSemanticStatusFixture(t, dir, "corrupt")
			return dir, semanticFixtureRegistry(t, false)
		}},
		{name: "ready", setup: func(t *testing.T) (string, *embed.Registry) {
			dir := t.TempDir()
			seedSemanticStatusFixture(t, dir, "ready")
			return dir, semanticFixtureRegistry(t, false)
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			meta, reg := tc.setup(t)
			root := t.TempDir()
			golden, err := os.ReadFile(filepath.Join("..", "..", "testdata", "semantic-status", tc.name+".json"))
			if err != nil {
				t.Fatal(err)
			}

			var cli bytes.Buffer
			rc := runSemanticStatusWithRegistryAt(root, []string{"--json", "-root", root, "-meta", meta}, &cli, reg)
			wantRC := 1
			if tc.name == "ready" {
				wantRC = 0
			} else if tc.name == "corrupt" {
				wantRC = 2
			}
			if rc != wantRC {
				t.Fatalf("CLI exit=%d want=%d: %s", rc, wantRC, cli.Bytes())
			}

			graph := graphstore.NewMemStore()
			defer graph.Close()
			direct := clientpkg.NewDirect(query.New(graph), search.New(graph))
			baseClient := semanticStatusSurfaceClient{Client: direct}
			httpServer := httpsurface.New(baseClient, nil).WithEmbedderRegistry(reg)
			req := httptest.NewRequest("GET", "http://127.0.0.1/semantic/status?root="+url.QueryEscape(root)+"&meta="+url.QueryEscape(meta), nil)
			recorder := httptest.NewRecorder()
			httpServer.Handler().ServeHTTP(recorder, req)
			if recorder.Code != 200 {
				t.Fatalf("HTTP status=%d body=%s", recorder.Code, recorder.Body.Bytes())
			}

			mcpServer := mcp.NewServerWithClient(baseClient, mcp.WithLabs(), mcp.WithRepository(clientpkg.Repository{Root: root, MetaDir: meta}), mcp.WithEmbedderRegistry(reg))
			mcpText := callSemanticStatusMCP(t, mcpServer)
			for surface, got := range map[string][]byte{"CLI": cli.Bytes(), "HTTP": recorder.Body.Bytes(), "MCP": mcpText} {
				if !bytes.Equal(got, golden) {
					t.Fatalf("%s bytes differ from %s golden:\n got %q\nwant %q", surface, tc.name, got, golden)
				}
			}
		})
	}
}

func semanticFixtureRegistry(t *testing.T, unavailable bool) *embed.Registry {
	t.Helper()
	reg := embed.NewRegistry()
	if err := reg.Register(semanticFixtureEmbedder{unavailable: unavailable}); err != nil {
		t.Fatal(err)
	}
	reg.Freeze()
	return reg
}

func seedSemanticStatusFixture(t *testing.T, metaDir, state string) {
	t.Helper()
	db, err := sql.Open("sqlite", filepath.Join(metaDir, "ingest-meta.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, ddl := range []string{
		`CREATE TABLE generations (id TEXT PRIMARY KEY, fingerprint TEXT NOT NULL, fingerprint_dim INTEGER NOT NULL, document_schema TEXT NOT NULL, row_count INTEGER NOT NULL, is_active INTEGER NOT NULL, is_staging INTEGER NOT NULL, committed_at TEXT NOT NULL DEFAULT '')`,
		`CREATE TABLE generation_rows (generation_id TEXT NOT NULL, document_id TEXT NOT NULL, node_id TEXT NOT NULL, text_hash TEXT NOT NULL, path TEXT NOT NULL, start_line INTEGER NOT NULL, end_line INTEGER NOT NULL, span_method TEXT NOT NULL, vector BLOB NOT NULL, PRIMARY KEY(generation_id,node_id))`,
	} {
		if _, err := db.Exec(ddl); err != nil {
			t.Fatal(err)
		}
	}
	fp := embed.Fingerprint{ModelID: "fixture:model@rev1", Revision: "rev1", ModelSHA256: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", Dim: 2, DocumentSchema: embed.DocumentSchema, GraphGeneration: embed.GraphGenerationPlaceholder}
	if state == "stale" {
		fp.ModelID = "fixture:old@rev0"
	}
	rowCount := 1
	if _, err := db.Exec(`INSERT INTO generations VALUES(?,?,?,?,?,1,0,?)`, "g-fixed-"+state, fp.Canonical(), fp.Dim, fp.DocumentSchema, rowCount, "2026-08-30T12:34:56Z"); err != nil {
		t.Fatal(err)
	}
	vector := make([]byte, 8)
	if state == "corrupt" {
		vector = make([]byte, 4)
	}
	if _, err := db.Exec(`INSERT INTO generation_rows VALUES(?,?,?,?,?,?,?,?,?)`, "g-fixed-"+state, "doc-1", "node-1", "hash-1", "pkg/a.go", 10, 12, "ast", vector); err != nil {
		t.Fatal(err)
	}
}

func callSemanticStatusMCP(t *testing.T, server *mcp.Server) []byte {
	t.Helper()
	request := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"semantic_status","arguments":{}}}`
	var output bytes.Buffer
	if err := server.Serve(context.Background(), bytes.NewBufferString(request+"\n"), &output); err != nil {
		t.Fatal(err)
	}
	var envelope struct {
		Result struct {
			Content []struct {
				Text string `json:"text"`
			} `json:"content"`
		} `json:"result"`
		Error *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(output.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	if envelope.Error != nil {
		t.Fatal(errors.New(envelope.Error.Message))
	}
	if len(envelope.Result.Content) != 1 {
		t.Fatalf("MCP content=%d: %s", len(envelope.Result.Content), output.Bytes())
	}
	return []byte(envelope.Result.Content[0].Text)
}
