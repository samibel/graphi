package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/samibel/graphi/core/graphstore"
	"github.com/samibel/graphi/core/parse"
	"github.com/samibel/graphi/engine/ingest"
	"github.com/samibel/graphi/internal/mcpconfig"
	"github.com/samibel/graphi/internal/mcpregistration"
	"github.com/samibel/graphi/internal/state"
	"github.com/samibel/graphi/internal/testenv"
)

func indexedSetupRepo(t *testing.T, parent, name string) (string, state.Paths) {
	t.Helper()
	root := filepath.Join(parent, name)
	if err := os.MkdirAll(filepath.Join(root, ".git"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "synthetic.go"), []byte("package synthetic\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	paths, err := state.Resolve(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := state.Ensure(paths); err != nil {
		t.Fatal(err)
	}
	store, err := graphstore.OpenSQLite(paths.DB)
	if err != nil {
		t.Fatal(err)
	}
	meta, err := ingest.New(store, parse.NewDefaultRegistry(), paths.Meta)
	if err != nil {
		_ = store.Close()
		t.Fatal(err)
	}
	if err := meta.Close(); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	return root, paths
}

func repoFiles(t *testing.T, root string) map[string]string {
	t.Helper()
	files := map[string]string{}
	if err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		files[rel] = string(raw)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return files
}

func TestPerRepoGlobalOnly(t *testing.T) {
	paths := testenv.Isolate(t)
	repo, _ := indexedSetupRepo(t, filepath.Join(paths.Root, "consumer"), "service")
	before := repoFiles(t, repo)
	config := filepath.Join(paths.Root, "clients", "claude.json")
	rc := runSetup([]string{"--per-repo", "--client", "claude", "--root", repo, "--config", config, "--binary", "/opt/graphi"})
	if rc != 0 {
		t.Fatalf("setup --per-repo rc=%d", rc)
	}
	if after := repoFiles(t, repo); !reflect.DeepEqual(before, after) {
		t.Fatalf("consumer repository changed:\nbefore=%v\nafter=%v", before, after)
	}
	doc, err := mcpconfig.Load(config)
	if err != nil {
		t.Fatal(err)
	}
	servers := doc["mcpServers"].(map[string]any)
	if servers["graphi-service"] == nil {
		t.Fatalf("named global entry missing: %#v", servers)
	}
}

func TestRootSupportedWithPerRepo(t *testing.T) {
	paths := testenv.Isolate(t)
	repo, _ := indexedSetupRepo(t, filepath.Join(paths.Root, "elsewhere"), "billing-api")
	config := filepath.Join(paths.Root, "client.json")
	if rc := runSetup([]string{"--per-repo", "--root", repo, "--client", "claude", "--config", config, "--dry-run", "--binary", "/opt/graphi"}); rc != 0 {
		t.Fatalf("--root with --per-repo rc=%d", rc)
	}
	if _, err := os.Stat(config); !os.IsNotExist(err) {
		t.Fatalf("dry-run wrote config: %v", err)
	}
}

func TestPerRepoProjectConflict(t *testing.T) {
	testenv.Isolate(t)
	if rc := runSetup([]string{"--per-repo", "--project"}); rc == 0 {
		t.Fatal("--per-repo with --project succeeded")
	}
}

func TestConfigRequiresExplicitClient(t *testing.T) {
	paths := testenv.Isolate(t)
	repo, _ := indexedSetupRepo(t, filepath.Join(paths.Root, "consumer"), "service")
	if rc := runSetup([]string{"--per-repo", "--root", repo, "--config", filepath.Join(paths.Root, "client.json"), "--dry-run"}); rc == 0 {
		t.Fatal("--config without an explicit --client succeeded")
	}
}

func TestConfigInsideConsumerRepoRejected(t *testing.T) {
	paths := testenv.Isolate(t)
	repo, _ := indexedSetupRepo(t, filepath.Join(paths.Root, "consumer"), "service")
	target := filepath.Join(repo, ".claude", "global.json")
	if rc := runSetup([]string{"--per-repo", "--root", repo, "--client", "claude", "--config", target, "--auto-register", "--yes", "--binary", "/opt/graphi"}); rc == 0 {
		t.Fatal("config target inside the consumer repository succeeded")
	}
	if _, err := os.Stat(filepath.Dir(target)); !os.IsNotExist(err) {
		t.Fatalf("rejected config created repository state: %v", err)
	}
}

func TestMissingHomeNeverFallsBackToCWD(t *testing.T) {
	paths := testenv.Isolate(t)
	repo, _ := indexedSetupRepo(t, filepath.Join(paths.Root, "consumer"), "service")
	t.Setenv("XDG_STATE_HOME", "")
	t.Setenv("HOME", "")
	t.Setenv("USERPROFILE", "")
	old, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(repo); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(old) })
	if rc := runSetup([]string{"--per-repo", "--client", "claude", "--dry-run", "--binary", "/opt/graphi"}); rc == 0 {
		t.Fatal("per-repo setup fell back without a user state directory")
	}
	if _, err := os.Stat(filepath.Join(repo, ".graphi")); !os.IsNotExist(err) {
		t.Fatalf("fallback state appeared in cwd: %v", err)
	}
}

func TestAllReposReadsOnlyStateDescriptors(t *testing.T) {
	paths := testenv.Isolate(t)
	_, _ = indexedSetupRepo(t, filepath.Join(paths.Root, "consumer-a"), "alpha")
	_, _ = indexedSetupRepo(t, filepath.Join(paths.Root, "consumer-b"), "beta")
	if err := os.MkdirAll(filepath.Join(paths.Home, "unindexed", ".git"), 0o700); err != nil {
		t.Fatal(err)
	}
	config := filepath.Join(paths.Root, "client.json")
	output := captureStdout(t, func() {
		if rc := runSetup([]string{"--per-repo", "--all-repos", "--client", "claude", "--config", config, "--dry-run", "--binary", "/opt/graphi"}); rc != 0 {
			t.Fatalf("--all-repos rc=%d", rc)
		}
	})
	if strings.Count(output, "server: graphi-") != 2 || strings.Contains(output, "unindexed") {
		t.Fatalf("all-repos output does not match state descriptors:\n%s", output)
	}
}

func TestAutoConsentRequiresTTYOrYes(t *testing.T) {
	paths := testenv.Isolate(t)
	repo, _ := indexedSetupRepo(t, filepath.Join(paths.Root, "consumer"), "service")
	if rc := runSetup([]string{"--per-repo", "--root", repo, "--client", "claude", "--auto-register", "--binary", "/opt/graphi"}); rc == 0 {
		t.Fatal("non-interactive auto-register succeeded without --yes")
	}
	manifest := mcpregistration.ManifestPath(state.StateDir())
	if _, err := os.Stat(manifest); !os.IsNotExist(err) {
		t.Fatalf("rejected consent wrote manifest: %v", err)
	}
}

func TestAllClientsConsentSnapshot(t *testing.T) {
	paths := testenv.Isolate(t)
	repo, _ := indexedSetupRepo(t, filepath.Join(paths.Root, "consumer"), "service")
	if err := os.MkdirAll(filepath.Join(paths.Home, ".config", "devin"), 0o700); err != nil {
		t.Fatal(err)
	}
	args := []string{"--per-repo", "--root", repo, "--client", "all", "--auto-register", "--yes", "--binary", "/opt/graphi"}
	if rc := runSetup(append(append([]string(nil), args...), "--dry-run")); rc != 0 {
		t.Fatalf("all-client auto-register dry-run rc=%d", rc)
	}
	if _, err := os.Stat(mcpregistration.ManifestPath(state.StateDir())); !os.IsNotExist(err) {
		t.Fatalf("auto-register dry-run wrote policy manifest: %v", err)
	}
	if rc := runSetup(args); rc != 0 {
		t.Fatalf("all-client auto-register rc=%d", rc)
	}
	manifest, err := mcpregistration.LoadManifest(mcpregistration.ManifestPath(state.StateDir()))
	if err != nil {
		t.Fatal(err)
	}
	var enabled []string
	for _, policy := range manifest.Policies {
		if policy.Enabled {
			enabled = append(enabled, policy.ClientID)
		}
	}
	sort.Strings(enabled)
	want := []string{"claude", "codex", "devin"}
	if !reflect.DeepEqual(enabled, want) {
		t.Fatalf("consent snapshot = %v, want %v", enabled, want)
	}
}

func TestAdoptExplicitMatchingEntry(t *testing.T) {
	paths := testenv.Isolate(t)
	repo, store := indexedSetupRepo(t, filepath.Join(paths.Root, "consumer"), "service")
	entry := mcpconfig.GraphiEntry("/opt/graphi", []string{"mcp", "-db", store.DB, "-meta", store.Meta})
	if _, err := mcpconfig.Apply(paths.ClaudeConfigPath, "graphi-service", entry, false); err != nil {
		t.Fatal(err)
	}
	if rc := runSetup([]string{"--per-repo", "--root", repo, "--client", "claude", "--name", "graphi-service", "--adopt", "--binary", "/opt/graphi"}); rc != 0 {
		t.Fatalf("matching adoption rc=%d", rc)
	}
	manifest, err := mcpregistration.LoadManifest(mcpregistration.ManifestPath(state.StateDir()))
	if err != nil {
		t.Fatal(err)
	}
	if len(manifest.Receipts) != 1 || manifest.Receipts[0].ServerName != "graphi-service" {
		t.Fatalf("adoption receipt missing: %#v", manifest.Receipts)
	}
}

func TestUnregisterDoesNotDeleteIndex(t *testing.T) {
	paths := testenv.Isolate(t)
	repo, store := indexedSetupRepo(t, filepath.Join(paths.Root, "consumer"), "service")
	base := []string{"--per-repo", "--root", repo, "--client", "claude", "--binary", "/opt/graphi"}
	if rc := runSetup(base); rc != 0 {
		t.Fatalf("registration rc=%d", rc)
	}
	if rc := runSetup(append(base, "--unregister")); rc != 0 {
		t.Fatalf("unregister rc=%d", rc)
	}
	if _, err := os.Stat(store.DB); err != nil {
		t.Fatalf("unregister removed index: %v", err)
	}
	doc, err := mcpconfig.Load(paths.ClaudeConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	if servers, _ := doc["mcpServers"].(map[string]any); servers["graphi-service"] != nil {
		t.Fatalf("managed entry remains after unregister: %#v", servers)
	}
}

func TestUnregisterEditedEntryRefuses(t *testing.T) {
	paths := testenv.Isolate(t)
	repo, _ := indexedSetupRepo(t, filepath.Join(paths.Root, "consumer"), "service")
	base := []string{"--per-repo", "--root", repo, "--client", "claude", "--binary", "/opt/graphi"}
	if rc := runSetup(base); rc != 0 {
		t.Fatalf("registration rc=%d", rc)
	}
	doc, err := mcpconfig.Load(paths.ClaudeConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	entry := doc["mcpServers"].(map[string]any)["graphi-service"].(map[string]any)
	entry["command"] = "/manual/edit"
	raw, _ := json.Marshal(doc)
	if err := os.WriteFile(paths.ClaudeConfigPath, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	if rc := runSetup(append(base, "--unregister")); rc == 0 {
		t.Fatal("unregister removed an edited entry")
	}
	after, _ := mcpconfig.Load(paths.ClaudeConfigPath)
	if got := after["mcpServers"].(map[string]any)["graphi-service"].(map[string]any)["command"]; got != "/manual/edit" {
		t.Fatalf("edited entry changed: %v", got)
	}
}

func TestNoAutoRegisterOnlyChangesPolicy(t *testing.T) {
	paths := testenv.Isolate(t)
	repo, store := indexedSetupRepo(t, filepath.Join(paths.Root, "consumer"), "service")
	base := []string{"--per-repo", "--root", repo, "--client", "claude", "--auto-register", "--yes", "--binary", "/opt/graphi"}
	if rc := runSetup(base); rc != 0 {
		t.Fatalf("auto-register rc=%d", rc)
	}
	configBefore, err := os.ReadFile(paths.ClaudeConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(repo); err != nil {
		t.Fatal(err)
	}
	if rc := runSetup([]string{"--per-repo", "--client", "claude", "--no-auto-register", "--binary", "/opt/graphi"}); rc != 0 {
		t.Fatalf("no-auto-register rc=%d", rc)
	}
	configAfter, err := os.ReadFile(paths.ClaudeConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(configAfter) != string(configBefore) {
		t.Fatal("no-auto-register changed client config")
	}
	manifest, err := mcpregistration.LoadManifest(mcpregistration.ManifestPath(state.StateDir()))
	if err != nil {
		t.Fatal(err)
	}
	if len(manifest.Policies) != 1 || manifest.Policies[0].Enabled {
		t.Fatalf("policy not disabled: %#v", manifest.Policies)
	}
	if _, err := os.Stat(store.DB); err != nil {
		t.Fatalf("no-auto-register changed index: %v", err)
	}
}
