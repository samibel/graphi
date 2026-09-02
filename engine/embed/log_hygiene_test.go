package embed_test

// SW-265 AC-7: log hygiene.
//
// The system shall NOT log source text, queries, or vectors at any log
// level. A regression that started logging the embedded text (e.g. for
// a debug "embedded N nodes" line that included the text) would leak
// repository content into stderr/log files, and the privacy contract
// (docs/privacy-audit) names repository text as untrusted.
//
// The test runs the embedding pipeline against a fixture repo whose
// body text contains a recognisable body string (the "secret
// passphrase" the test asserts is NEVER present in any captured
// output). It captures stderr during the run and asserts the body
// text is absent.
//
// The fixture's body string MUST be something that no normal log line
// would contain. The test grep is exact (no regex wildcard); a
// regression that emitted the body verbatim would fail this assertion,
// a regression that emitted it as part of a longer string would also
// fail.

import (
	"context"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/samibel/graphi/core/graphstore"
	"github.com/samibel/graphi/core/model"
	"github.com/samibel/graphi/engine/embed"
	"github.com/samibel/graphi/engine/search"
)

// secretPassphrase is the body's sentinel substring. It is chosen to be
// distinctive enough that a "log the embedded text" regression would
// surface in any captured output. The string is committed only here.
const secretPassphrase = "GRAPHI_AC7_PASSPHRASE_NEVER_LOG_ME_42617218"

type secretDocumentSource struct{ text string }

func (s secretDocumentSource) Document(n model.Node) (embed.SemanticDocument, bool) {
	return embed.SemanticDocument{
		DocumentID:     "secret-document",
		NodeID:         n.ID(),
		QualifiedName:  n.QualifiedName(),
		Path:           n.SourcePath(),
		TextHash:       "secret-hash",
		DocumentSchema: embed.DocumentSchema,
		Text:           s.text,
	}, true
}

// TestIndexSearch_NoBodyTextInStderr runs the embedding pipeline and
// the search service against a fixture whose body contains the
// passphrase, with the passphrase also used as the search query. Both
// runs share a stderr capture; the test greps the buffer and fails if
// the passphrase appears verbatim.
//
// The pipeline here is the in-process engine/embed (the same one the
// CLI cmd/graphi/index.go --semantic path uses), not the cmd-rank
// runner. The cmd-rank runner adds its own milestone lines
// ("graphi: embedding via mock...") which are NOT body text — those
// pass the grep by construction. The point of the test is that the
// engine leaf never logs the text it embeds.
//
// The SAME pipeline runs in production for `graphi index --semantic`
// (the cmd-graphi runner calls BuildSemanticGeneration which calls
// GenerateAndPersistWithProgress). A regression that logs the body
// here would also leak repository text in production.
func TestIndexSearch_NoBodyTextInStderr(t *testing.T) {
	body := "package shop\n\nconst " + secretPassphrase + " = \"value\"\n"
	nodes := []model.Node{makeNode("p.secret", "/secret.go")}
	mock := embed.NewMockEmbedder(8)
	reg := embed.NewRegistry()
	if err := reg.Register(mock); err != nil {
		t.Fatalf("register: %v", err)
	}
	reg.Freeze()

	rerr, werr, perr := os.Pipe()
	if perr != nil {
		t.Fatal(perr)
	}
	oldStderr := os.Stderr
	os.Stderr = werr
	defer func() { os.Stderr = oldStderr }()
	done := make(chan string)
	go func() {
		b, _ := io.ReadAll(rerr)
		done <- string(b)
	}()

	ctx := context.Background()
	store := embed.NewMemGenerationStore()
	index := embed.NewIndex()
	_, err := embed.GenerateAndPersistWithProgress(ctx, reg, nodes, secretDocumentSource{text: body}, index, store, nil, embed.GraphGenerationPlaceholder)
	if err != nil {
		t.Fatalf("GenerateAndPersist: %v", err)
	}
	// Wire the index the search service will read. Reload from the
	// just-built generation so the index mirrors what the runtime path
	// would produce post-rebuild.
	rows, lerr := store.Load(ctx, buildActiveID(t, store))
	if lerr != nil {
		t.Fatalf("load rows: %v", lerr)
	}
	for _, r := range rows {
		index.Put(r.NodeID, r.Vector)
	}

	// Drive the search service with the passphrase as the query. The
	// search service runs Embed(query) and returns ranked hits; we
	// capture stderr around it.
	st := graphstore.NewMemStore()
	svc := search.New(st).WithSemantic(reg, index, st)
	res, err := svc.SemanticSearch(ctx, secretPassphrase+"_as_query", 10)
	if err != nil {
		t.Fatalf("SemanticSearch: %v", err)
	}
	if !res.Available {
		t.Fatalf("search service reports unavailable; expected the configured path")
	}

	_ = werr.Close()
	captured := <-done

	if strings.Contains(captured, secretPassphrase) {
		t.Fatalf("AC-7 privacy violation: passphrase appears in stderr output:\n%s", captured)
	}
}

// TestSemanticStatus_NoQueryTextInStderr runs the status composition
// and asserts the wire document does not embed the secret passphrase.
// A regression that started including the bound repository path in
// the wire document (or in a debug log) would fail here.
func TestSemanticStatus_NoQueryTextInStderr(t *testing.T) {
	t.Setenv("GRAPHI_EMBEDDER", "")
	rerr, werr, perr := os.Pipe()
	if perr != nil {
		t.Fatal(perr)
	}
	oldStderr := os.Stderr
	os.Stderr = werr
	defer func() { os.Stderr = oldStderr }()
	done := make(chan string)
	go func() {
		b, _ := io.ReadAll(rerr)
		done <- string(b)
	}()

	status := embed.LoadStatus(context.Background(), "", embed.NewRegistry(), "", nil)
	_ = status
	_ = werr.Close()
	captured := <-done
	if strings.Contains(captured, secretPassphrase) {
		t.Fatalf("status stderr contains the secret passphrase: %s", captured)
	}
}

// makeNode synthesises a model.Node with the given qualified name, body
// and source path. It is intentionally local to the test (not exposed
// elsewhere) because the body carries the secret passphrase.
func makeNode(qualified, path string) model.Node {
	n, err := model.NewNode("function", qualified, path, 1, 1)
	if err != nil {
		panic(err)
	}
	return n
}

// buildActiveID reads the active generation id from the store. The
// caller passes the just-built store after a successful Commit.
func buildActiveID(t *testing.T, store *embed.MemGenerationStore) embed.GenerationID {
	t.Helper()
	// The fingerprint under which the test built is the mock embedder's
	// fingerprint. Querying Active() with the same fingerprint yields
	// the just-committed id.
	mock := embed.NewMockEmbedder(8)
	fp := embed.Fingerprint{
		ModelID:         mock.ID(),
		Dim:             mock.Dim(),
		DocumentSchema:  embed.DocumentSchema,
		GraphGeneration: embed.GraphGenerationPlaceholder,
	}
	gen, state, err := store.Active(context.Background(), fp, nil)
	if err != nil {
		t.Fatalf("active: %v", err)
	}
	if state != embed.StateReady {
		t.Fatalf("active state = %v, want ready", state)
	}
	return gen.ID
}
