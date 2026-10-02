package main

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/samibel/graphi/internal/mcpconfig"
	"github.com/samibel/graphi/internal/state"
	"github.com/samibel/graphi/internal/testenv"
)

// TestGlobalMCPAcceptanceMatrixSynthetic is the compact A01/A05/A07/A18/A19
// end-to-end fixture. It uses only temporary local checkouts and an isolated
// profile; no client process, account, network, or real user config is needed.
func TestGlobalMCPAcceptanceMatrixSynthetic(t *testing.T) {
	isolated := testenv.Isolate(t)
	repositories := make([]string, 0, 3)
	statePaths := make(map[string]state.Paths, 3)
	before := make(map[string]map[string]string, 3)
	for _, name := range []string{"billing-api", "catalog-api", "identity-api"} {
		root, paths := indexedSetupRepo(t, filepath.Join(isolated.Root, "consumer"), name)
		repositories = append(repositories, root)
		statePaths[name] = paths
		before[root] = repoFiles(t, root)
	}
	config := filepath.Join(isolated.Root, "clients", "claude.json")
	args := []string{"--per-repo", "--all-repos", "--client", "claude", "--config", config, "--binary", "/opt/graphi"}
	if code := runSetup(args); code != 0 {
		t.Fatalf("three-repository setup exit=%d", code)
	}

	client, _ := mcpconfig.ClientByID("claude")
	entries, err := client.WithConfigPath(config).Entries()
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 3 {
		t.Fatalf("global entries=%d, want 3: %#v", len(entries), entries)
	}
	boundDBs := map[string]bool{}
	for name, raw := range entries {
		entry, ok := raw.(map[string]any)
		if !ok {
			t.Fatalf("%s entry is not an object: %#v", name, raw)
		}
		arguments, ok := entry["args"].([]any)
		if !ok || len(arguments) != 5 || arguments[0] != "mcp" || arguments[1] != "-db" || arguments[3] != "-meta" {
			t.Fatalf("%s is not a pinned attach: %#v", name, raw)
		}
		db, _ := arguments[2].(string)
		boundDBs[db] = true
	}
	if len(boundDBs) != 3 {
		t.Fatalf("three named servers do not bind three stores: %#v", boundDBs)
	}

	firstConfig, err := os.ReadFile(config)
	if err != nil {
		t.Fatal(err)
	}
	if code := runSetup(args); code != 0 {
		t.Fatalf("idempotent setup exit=%d", code)
	}
	secondConfig, err := os.ReadFile(config)
	if err != nil {
		t.Fatal(err)
	}
	if string(secondConfig) != string(firstConfig) {
		t.Fatal("second registration changed config bytes")
	}
	for _, root := range repositories {
		if after := repoFiles(t, root); !reflect.DeepEqual(after, before[root]) {
			t.Fatalf("consumer repository changed: %s\nbefore=%v\nafter=%v", root, before[root], after)
		}
	}

	// Starting from another checkout cannot reroute a generated attach. The
	// context is recovered from billing-api's exact DB/meta descriptor only.
	oldCWD, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(repositories[1]); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(oldCWD) })
	billing := statePaths["billing-api"]
	context, err := resolveAttachRepoContext(state.StateDir(), billing.DB, billing.Meta)
	if err != nil {
		t.Fatal(err)
	}
	if context.Root != repositories[0] || context.Label != "graphi-billing-api" {
		t.Fatalf("explicit cross-repo binding was rerouted by cwd: %#v", context)
	}
}
