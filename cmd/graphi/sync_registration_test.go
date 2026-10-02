package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	rtime "github.com/samibel/graphi/cmd/internal/runtime"
	"github.com/samibel/graphi/internal/ingestlock"
	"github.com/samibel/graphi/internal/mcpconfig"
	"github.com/samibel/graphi/internal/mcpregistration"
	"github.com/samibel/graphi/internal/state"
	"github.com/samibel/graphi/internal/testenv"
)

func writeSyncPolicy(t *testing.T, config string, enabled bool) {
	t.Helper()
	manifest := mcpregistration.NewManifest()
	manifest.Policies = []mcpregistration.AutoRegisterPolicy{{ClientID: "claude", ConfigPath: config, Enabled: enabled}}
	if err := mcpregistration.SaveManifest(mcpregistration.ManifestPath(state.StateDir()), manifest, false); err != nil {
		t.Fatal(err)
	}
}

func TestSuccessfulExplicitSyncRegisters(t *testing.T) {
	paths := testenv.Isolate(t)
	t.Setenv("GRAPHI_EMBEDDER", "")
	repo := writeGoRepo(t)
	gitRepo(t, repo, "main")
	config := filepath.Join(paths.Root, "clients", "claude.json")
	writeSyncPolicy(t, config, true)

	var out bytes.Buffer
	if code := runSyncAt(repo, nil, &out); code != 0 {
		t.Fatalf("sync exit=%d output=%s", code, out.String())
	}
	client, _ := mcpconfig.ClientByID("claude")
	entries, err := client.WithConfigPath(config).Entries()
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("successful sync registrations = %#v", entries)
	}
	for _, entry := range entries {
		object := entry.(map[string]any)
		args := object["args"].([]any)
		if len(args) < 5 || args[0] != "mcp" || args[1] != "-db" || args[3] != "-meta" {
			t.Fatalf("registration is not a pinned attach: %#v", entry)
		}
	}
	if !strings.Contains(out.String(), "registered") {
		t.Fatalf("sync output keeps registration result invisible: %s", out.String())
	}
	before, err := os.ReadFile(config)
	if err != nil {
		t.Fatal(err)
	}
	out.Reset()
	if code := runSyncAt(repo, nil, &out); code != 0 {
		t.Fatalf("idempotent sync exit=%d output=%s", code, out.String())
	}
	after, err := os.ReadFile(config)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(before) || strings.Contains(out.String(), "registered") {
		t.Fatalf("idempotent sync rewrote or re-announced registration:\n%s", out.String())
	}
}

func TestFailedSyncDoesNotRegister(t *testing.T) {
	paths := testenv.Isolate(t)
	repo := writeGoRepo(t)
	gitRepo(t, repo, "main")
	config := filepath.Join(paths.Root, "clients", "claude.json")
	writeSyncPolicy(t, config, true)
	called := false
	old := reconcileAfterExplicitSync
	reconcileAfterExplicitSync = func(string) (mcpregistration.ReconcileResult, error) {
		called = true
		return mcpregistration.ReconcileResult{}, nil
	}
	t.Cleanup(func() { reconcileAfterExplicitSync = old })

	invalid := "definitely-not-a-profile"
	if code := runSyncAt(repo, []string{"-profile", invalid}, new(bytes.Buffer)); code == 0 {
		t.Fatal("sync with invalid profile succeeded")
	}
	if called {
		t.Fatal("failed sync invoked auto-registration")
	}
	if _, err := os.Stat(config); !os.IsNotExist(err) {
		t.Fatalf("failed sync changed config: %v", err)
	}
}

func TestMCPAutoBindDoesNotChangeConfigs(t *testing.T) {
	paths := testenv.Isolate(t)
	t.Setenv("GRAPHI_EMBEDDER", "")
	repo := writeGoRepo(t)
	gitRepo(t, repo, "main")
	config := filepath.Join(paths.Root, "clients", "claude.json")
	writeSyncPolicy(t, config, true)

	runtime, err := rtime.OpenSession(context.Background(), rtime.Options{Root: repo})
	if err != nil {
		t.Fatal(err)
	}
	runtime.Close()
	if _, err := os.Stat(config); !os.IsNotExist(err) {
		t.Fatalf("MCP-style auto-bind changed client config: %v", err)
	}
}

func TestRegistrationRunsAfterIngestUnlock(t *testing.T) {
	testenv.Isolate(t)
	t.Setenv("GRAPHI_EMBEDDER", "")
	repo := writeGoRepo(t)
	gitRepo(t, repo, "main")
	old := reconcileAfterExplicitSync
	reconcileAfterExplicitSync = func(root string) (mcpregistration.ReconcileResult, error) {
		paths, err := state.Resolve(root)
		if err != nil {
			return mcpregistration.ReconcileResult{}, err
		}
		lockState, err := ingestlock.Probe(context.Background(), paths.Meta)
		if err != nil {
			return mcpregistration.ReconcileResult{}, err
		}
		if lockState == ingestlock.StateHeld {
			return mcpregistration.ReconcileResult{}, errors.New("registration ran while ingest lock was held")
		}
		return mcpregistration.ReconcileResult{}, nil
	}
	t.Cleanup(func() { reconcileAfterExplicitSync = old })
	if code := runSyncAt(repo, nil, new(bytes.Buffer)); code != 0 {
		t.Fatalf("sync exit=%d", code)
	}
}

func TestSyncRegistrationFailureReturnsDistinctExit(t *testing.T) {
	testenv.Isolate(t)
	t.Setenv("GRAPHI_EMBEDDER", "")
	repo := writeGoRepo(t)
	gitRepo(t, repo, "main")
	old := reconcileAfterExplicitSync
	reconcileAfterExplicitSync = func(string) (mcpregistration.ReconcileResult, error) {
		return mcpregistration.ReconcileResult{Failures: []mcpregistration.ClientFailure{{ClientID: "claude", Err: errors.New("synthetic config failure")}}}, errors.New("synthetic config failure")
	}
	t.Cleanup(func() { reconcileAfterExplicitSync = old })
	if code := runSyncAt(repo, nil, new(bytes.Buffer)); code != syncIntegrationFailureExit {
		t.Fatalf("registration failure exit=%d, want %d", code, syncIntegrationFailureExit)
	}
	if _, err := mcpregistration.NewResolver(state.StateDir()).Resolve(repo); err != nil {
		t.Fatalf("registration failure discarded successful index: %v", err)
	}
}
