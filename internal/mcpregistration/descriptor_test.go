package mcpregistration_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/samibel/graphi/core/graphstore"
	"github.com/samibel/graphi/core/parse"
	"github.com/samibel/graphi/engine/ingest"
	"github.com/samibel/graphi/internal/mcpregistration"
	"github.com/samibel/graphi/internal/state"
	"github.com/samibel/graphi/internal/testenv"
)

func indexedRepo(t *testing.T, parent, name string) (string, state.Paths) {
	t.Helper()
	root := filepath.Join(parent, name)
	if err := os.MkdirAll(filepath.Join(root, ".git"), 0o700); err != nil {
		t.Fatalf("create synthetic repo: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "synthetic.go"), []byte("package synthetic\n"), 0o600); err != nil {
		t.Fatalf("write synthetic source: %v", err)
	}
	p, err := state.Resolve(root)
	if err != nil {
		t.Fatalf("resolve state: %v", err)
	}
	if err := state.Ensure(p); err != nil {
		t.Fatalf("ensure state: %v", err)
	}
	store, err := graphstore.OpenSQLite(p.DB)
	if err != nil {
		t.Fatalf("create graph store: %v", err)
	}
	meta, err := ingest.New(store, parse.NewDefaultRegistry(), p.Meta)
	if err != nil {
		_ = store.Close()
		t.Fatalf("create ingest metadata: %v", err)
	}
	if err := meta.IngestAll(t.Context(), root); err != nil {
		_ = meta.Close()
		_ = store.Close()
		t.Fatalf("ingest synthetic source: %v", err)
	}
	if err := meta.Close(); err != nil {
		t.Fatalf("close ingest metadata: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("close graph store: %v", err)
	}
	return root, p
}

func TestCheckoutReplacementAtSamePathRequiresSync(t *testing.T) {
	paths := testenv.Isolate(t)
	root, _ := indexedRepo(t, filepath.Join(paths.Root, "work"), "service")
	if err := os.RemoveAll(root); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, ".git"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "replacement.go"), []byte("package replacement\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := mcpregistration.NewResolver(state.StateDir()).Resolve(root); err == nil {
		t.Fatal("Resolve advertised the old index for a replacement checkout at the same path")
	}
}

func TestSameBasenameDifferentRoots(t *testing.T) {
	paths := testenv.Isolate(t)
	a, pa := indexedRepo(t, filepath.Join(paths.Root, "work", "alpha"), "service-api")
	b, pb := indexedRepo(t, filepath.Join(paths.Root, "work", "beta"), "service-api")

	resolver := mcpregistration.NewResolver(state.StateDir())
	storeA, err := resolver.Resolve(a)
	if err != nil {
		t.Fatalf("resolve first checkout: %v", err)
	}
	storeB, err := resolver.Resolve(b)
	if err != nil {
		t.Fatalf("resolve second checkout: %v", err)
	}
	if storeA.CheckoutID == storeB.CheckoutID || storeA.CheckoutID != pa.Fingerprint || storeB.CheckoutID != pb.Fingerprint {
		t.Fatalf("checkout identities = %q and %q, want distinct descriptor fingerprints", storeA.CheckoutID, storeB.CheckoutID)
	}

	names, err := mcpregistration.AllocateNames([]mcpregistration.NameCandidate{
		{CheckoutID: storeA.CheckoutID, Root: a},
		{CheckoutID: storeB.CheckoutID, Root: b},
	}, mcpregistration.NameInventory{HomeDir: paths.Home})
	if err != nil {
		t.Fatalf("allocate names: %v", err)
	}
	if names[storeA.CheckoutID] == names[storeB.CheckoutID] {
		t.Fatalf("same-basename names collided at %q", names[storeA.CheckoutID])
	}
}

func TestSameRemoteDifferentCheckouts(t *testing.T) {
	paths := testenv.Isolate(t)
	a, _ := indexedRepo(t, filepath.Join(paths.Root, "clone-a"), "service")
	b, _ := indexedRepo(t, filepath.Join(paths.Root, "clone-b"), "service")
	remote := []byte("[remote \"origin\"]\n\turl = https://example.invalid/acme/service.git\n")
	for _, root := range []string{a, b} {
		if err := os.WriteFile(filepath.Join(root, ".git", "config"), remote, 0o600); err != nil {
			t.Fatalf("write synthetic remote: %v", err)
		}
	}

	resolver := mcpregistration.NewResolver(state.StateDir())
	storeA, err := resolver.Resolve(a)
	if err != nil {
		t.Fatalf("resolve first clone: %v", err)
	}
	storeB, err := resolver.Resolve(b)
	if err != nil {
		t.Fatalf("resolve second clone: %v", err)
	}
	if storeA.CheckoutID == storeB.CheckoutID {
		t.Fatalf("same remote collapsed checkout IDs to %q", storeA.CheckoutID)
	}
}

func TestSymlinkAliasKeepsLegacyID(t *testing.T) {
	paths := testenv.Isolate(t)
	realRoot := filepath.Join(paths.Root, "real", "service")
	if err := os.MkdirAll(filepath.Join(realRoot, ".git"), 0o700); err != nil {
		t.Fatalf("create real repo: %v", err)
	}
	alias := filepath.Join(paths.Root, "legacy-service")
	if err := os.Symlink(realRoot, alias); err != nil {
		t.Fatalf("create alias: %v", err)
	}
	_, legacy := indexedRepo(t, paths.Root, "legacy-service")

	got, err := mcpregistration.NewResolver(state.StateDir()).Resolve(realRoot)
	if err != nil {
		t.Fatalf("resolve real path through legacy descriptor: %v", err)
	}
	if got.CheckoutID != legacy.Fingerprint {
		t.Fatalf("checkout ID = %q, want legacy alias ID %q", got.CheckoutID, legacy.Fingerprint)
	}
	if got.Root != alias {
		t.Fatalf("descriptor root = %q, want preserved legacy root %q", got.Root, alias)
	}
}

func TestMultipleAliasStoresConflict(t *testing.T) {
	paths := testenv.Isolate(t)
	realRoot, _ := indexedRepo(t, filepath.Join(paths.Root, "real"), "service")
	alias := filepath.Join(paths.Root, "alias-service")
	if err := os.Symlink(realRoot, alias); err != nil {
		t.Fatalf("create alias: %v", err)
	}
	_, _ = indexedRepo(t, paths.Root, "alias-service")

	if _, err := mcpregistration.NewResolver(state.StateDir()).Resolve(realRoot); err == nil {
		t.Fatal("Resolve accepted two descriptors for the same canonical checkout")
	}
}

func TestMissingRepoNoRemoteFallback(t *testing.T) {
	paths := testenv.Isolate(t)
	missing, _ := indexedRepo(t, filepath.Join(paths.Root, "old"), "service")
	replacement, _ := indexedRepo(t, filepath.Join(paths.Root, "new"), "service")
	remote := []byte("[remote \"origin\"]\n\turl = https://example.invalid/acme/service.git\n")
	for _, root := range []string{missing, replacement} {
		if err := os.WriteFile(filepath.Join(root, ".git", "config"), remote, 0o600); err != nil {
			t.Fatalf("write synthetic remote: %v", err)
		}
	}
	if err := os.RemoveAll(missing); err != nil {
		t.Fatalf("remove old checkout: %v", err)
	}

	if _, err := mcpregistration.NewResolver(state.StateDir()).Resolve(missing); err == nil {
		t.Fatal("Resolve reused a store for a missing checkout")
	}
	if _, err := mcpregistration.NewResolver(state.StateDir()).Resolve(replacement); err != nil {
		t.Fatalf("unrelated existing checkout no longer resolves: %v", err)
	}
}

func TestDescriptorFingerprintMismatch(t *testing.T) {
	paths := testenv.Isolate(t)
	root, p := indexedRepo(t, filepath.Join(paths.Root, "work"), "service")
	descriptor := map[string]string{
		"abs_root":    root,
		"fingerprint": "0000000000000000",
		"created":     "-",
	}
	b, err := json.Marshal(descriptor)
	if err != nil {
		t.Fatalf("marshal descriptor: %v", err)
	}
	if err := os.WriteFile(p.RepoFile, b, 0o600); err != nil {
		t.Fatalf("corrupt descriptor: %v", err)
	}

	if _, err := mcpregistration.NewResolver(state.StateDir()).Resolve(root); err == nil {
		t.Fatal("Resolve accepted a descriptor/fingerprint mismatch")
	}
}
