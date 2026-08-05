package archdiff

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"

	"github.com/samibel/graphi/core/graphstore"
	"github.com/samibel/graphi/core/parse"
	"github.com/samibel/graphi/engine/analysis"
	"github.com/samibel/graphi/engine/distill"
	"github.com/samibel/graphi/engine/edit"
	"github.com/samibel/graphi/engine/ingest"
	"github.com/samibel/graphi/engine/ledger"
	"github.com/samibel/graphi/engine/memory"
	"github.com/samibel/graphi/engine/query"
	"github.com/samibel/graphi/engine/review"
	"github.com/samibel/graphi/engine/search"
	"github.com/samibel/graphi/engine/skillgen"
	"github.com/samibel/graphi/surfaces/client"
	"github.com/samibel/graphi/surfaces/forge"
)

// FixtureRelPath is the workload every recorded run uses: the same frozen corpus
// the characterization baseline pins, chosen so the two baselines describe the
// same graph rather than two similar-looking ones.
const FixtureRelPath = "corpus/fixtures/go"

// Env is one built environment: an indexed workspace, a fully wired legacy
// client, and the deterministic inputs the use cases need.
type Env struct {
	// Root is the indexed workspace (a copy, so mutating use cases cannot touch
	// the checked-in fixture).
	Root string
	// Store is the graph the client reads.
	Store graphstore.Graphstore
	// Client is the fully wired legacy client.
	Client *client.Direct
	// HelloID and ChainAID are deterministically chosen symbols from the fixture.
	HelloID  string
	ChainAID string

	tempDirs []string
	closers  []func() error
}

// mockForgePRs is the fixed, offline PR set the review use cases enumerate. It
// mirrors the shape the cross-surface parity tests use so the recorded review
// outcomes describe the same scenario the existing suite already reasons about.
func mockForgePRs() []forge.PR {
	return []forge.PR{
		{Number: 1, Title: "hub", Author: "alice", HeadSHA: "sha1", ChangedFiles: []string{"sample.go"}},
		{Number: 2, Title: "tested", Author: "bob", HeadSHA: "sha2", ChangedFiles: []string{"sample_test.go"}},
	}
}

// BuildEnv indexes the fixture into a fresh durable store and wires every
// optional service the legacy client accepts.
//
// It deliberately mirrors, rather than imports, the process composition in
// cmd/internal/runtime: Go's internal-visibility rule puts that package out of
// reach here, and duplicating the wiring in one reviewable place is better than
// recording a baseline through a client that is missing half its services — an
// unwired service records as a sentinel, which would silently freeze "this
// capability is unavailable" as the expected behaviour.
func BuildEnv(ctx context.Context, moduleRoot string) (*Env, error) {
	env := &Env{}
	cleanupOnFailure := func(err error) (*Env, error) {
		_ = env.Close()
		return nil, err
	}

	work, err := env.tempDir("graphi-archdiff-root-*")
	if err != nil {
		return nil, err
	}
	root := filepath.Join(work, "repo")
	if err := copyDir(filepath.Join(moduleRoot, filepath.FromSlash(FixtureRelPath)), root); err != nil {
		return cleanupOnFailure(fmt.Errorf("archdiff: copy fixture: %w", err))
	}
	env.Root = root

	storeDir, err := env.tempDir("graphi-archdiff-store-*")
	if err != nil {
		return cleanupOnFailure(err)
	}
	store, err := graphstore.OpenSQLite(filepath.Join(storeDir, "graph.db"))
	if err != nil {
		return cleanupOnFailure(fmt.Errorf("archdiff: open store: %w", err))
	}
	env.Store = store
	env.closers = append(env.closers, store.Close)

	metaDir := filepath.Join(storeDir, "meta")
	ing, err := ingest.New(store, ingest.NewNotebookParser(parse.NewDefaultRegistry()), metaDir)
	if err != nil {
		return cleanupOnFailure(fmt.Errorf("archdiff: new ingester: %w", err))
	}
	env.closers = append(env.closers, ing.Close)
	if err := ing.IngestAll(ctx, root); err != nil {
		return cleanupOnFailure(fmt.Errorf("archdiff: ingest: %w", err))
	}

	env.HelloID, err = functionID(ctx, store, "Hello")
	if err != nil {
		return cleanupOnFailure(err)
	}
	env.ChainAID, err = functionID(ctx, store, "ChainA")
	if err != nil {
		return cleanupOnFailure(err)
	}

	analysisSvc := analysis.NewDefaultService(store)

	ledgerPath := filepath.Join(storeDir, "ledger.jsonl")
	led, err := ledger.Open(ledgerPath)
	if err != nil {
		return cleanupOnFailure(fmt.Errorf("archdiff: open ledger: %w", err))
	}
	env.closers = append(env.closers, led.Close)

	memStore, err := memory.NewMemStore(nil)
	if err != nil {
		return cleanupOnFailure(fmt.Errorf("archdiff: new memory store: %w", err))
	}

	applier, err := edit.NewApplier(store, ing, root, nil)
	if err != nil {
		return cleanupOnFailure(fmt.Errorf("archdiff: new applier: %w", err))
	}
	recorder, err := edit.NewChangeRecorder(ctx, ing, metaDir)
	if err != nil {
		return cleanupOnFailure(fmt.Errorf("archdiff: new change recorder: %w", err))
	}

	env.Client = client.NewDirect(query.New(store), search.New(store)).
		WithAnalysis(analysisSvc).
		WithLedger(led).
		WithEditor(applier, recorder).
		WithReview(review.NewService(analysisSvc)).
		WithMemory(memStore).
		WithDistill(distill.NewDistiller(nil)).
		WithSkillGen(skillgen.NewGenerator(nil)).
		WithForge(forge.NewMockForge(mockForgePRs()))
	return env, nil
}

// tempDir creates a temp dir owned by the environment.
func (e *Env) tempDir(pattern string) (string, error) {
	dir, err := os.MkdirTemp("", pattern)
	if err != nil {
		return "", fmt.Errorf("archdiff: temp dir: %w", err)
	}
	e.tempDirs = append(e.tempDirs, dir)
	return dir, nil
}

// Close releases every resource the environment owns, in reverse order.
func (e *Env) Close() error {
	var firstErr error
	for i := len(e.closers) - 1; i >= 0; i-- {
		if err := e.closers[i](); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	for _, dir := range e.tempDirs {
		if err := os.RemoveAll(dir); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

// tempPathPattern matches any path under a system temp dir, including the ones
// this harness creates and any a service creates internally.
var tempPathPattern = regexp.MustCompile(`(?:/private)?/(?:tmp|var/folders/[^/\s"]*)/[^\s",\\]*`)

// sessionIDPattern matches the ledger's per-Open session identifier, which is
// fresh on every run by design.
var sessionIDPattern = regexp.MustCompile(`"session_id"\s*:\s*"[^"]*"`)

// Normalize replaces run-specific values with stable tokens.
//
// Two things vary between runs no matter what: absolute temp paths, and the
// ledger session id (deliberately fresh per Open). Both would make every digest
// unique and turn the baseline into noise. Everything else is left alone — the
// point of the recording is to notice changes, so normalizing generously would
// defeat it.
func (e *Env) Normalize(s string) string {
	if e.Root != "" {
		s = strings.ReplaceAll(s, e.Root, "{{ROOT}}")
	}
	for _, dir := range e.tempDirs {
		s = strings.ReplaceAll(s, dir, "{{TMP}}")
	}
	s = tempPathPattern.ReplaceAllString(s, "{{TMP}}")
	s = sessionIDPattern.ReplaceAllString(s, `"session_id":"{{SESSION}}"`)
	return s
}

// NormalizeBytes applies Normalize to a response payload.
func (e *Env) NormalizeBytes(b []byte) []byte {
	if len(b) == 0 {
		return b
	}
	return []byte(e.Normalize(string(b)))
}

// functionID returns the deterministic node id of a function by name, choosing
// the lowest canonical id when several match so the recorded inputs never depend
// on map iteration order.
func functionID(ctx context.Context, store graphstore.Graphstore, name string) (string, error) {
	nodes, err := store.Nodes(ctx, graphstore.Query{})
	if err != nil {
		return "", fmt.Errorf("archdiff: read nodes: %w", err)
	}
	var ids []string
	for _, n := range nodes {
		if n.Kind() != "function" {
			continue
		}
		if n.QualifiedName() == name || strings.HasSuffix(n.QualifiedName(), "."+name) {
			ids = append(ids, string(n.ID()))
		}
	}
	if len(ids) == 0 {
		return "", fmt.Errorf("archdiff: function %q not found in the indexed fixture", name)
	}
	sort.Strings(ids)
	return ids[0], nil
}

// copyDir recursively copies src into dst.
func copyDir(src, dst string) error {
	return filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, rerr := filepath.Rel(src, p)
		if rerr != nil {
			return rerr
		}
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		in, rerr := os.ReadFile(p)
		if rerr != nil {
			return rerr
		}
		return os.WriteFile(target, in, 0o644)
	})
}

// ModuleRoot resolves the module root once via `go env GOMOD` and caches it.
var ModuleRoot = sync.OnceValues(func() (string, error) {
	out, err := exec.Command("go", "env", "GOMOD").Output()
	if err != nil {
		return "", err
	}
	gomod := strings.TrimSpace(string(out))
	if gomod == "" || gomod == "/dev/null" {
		return "", fmt.Errorf("archdiff: no go.mod found (GOMOD=%q)", gomod)
	}
	return filepath.Dir(gomod), nil
})

// RecordAll builds a fresh environment and records every use case: the wired
// table against the fully wired client, and the fail-closed table against a bare
// one. Both belong in the same baseline — "this works" and "this correctly
// refuses" are equally part of the behaviour a refactor must preserve.
func RecordAll(ctx context.Context, moduleRoot string) (map[string]Entry, error) {
	env, err := BuildEnv(ctx, moduleRoot)
	if err != nil {
		return nil, err
	}
	defer env.Close()

	recorded, err := Record(ctx, env.Client, env, Cases())
	if err != nil {
		return nil, err
	}
	// A client with nothing optional wired. It still needs query/search so the
	// use cases can be constructed; everything else must refuse.
	bare := client.NewDirect(query.New(env.Store), search.New(env.Store))
	unwired, err := Record(ctx, bare, env, UnwiredCases())
	if err != nil {
		return nil, err
	}
	for id, entry := range unwired {
		if _, dup := recorded[id]; dup {
			return nil, fmt.Errorf("archdiff: use case id %q appears in both tables", id)
		}
		recorded[id] = entry
	}
	return recorded, nil
}
