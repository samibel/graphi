package mcpregistration_test

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/samibel/graphi/internal/mcpconfig"
	"github.com/samibel/graphi/internal/mcpregistration"
	"github.com/samibel/graphi/internal/state"
	"github.com/samibel/graphi/internal/testenv"
)

func savePolicies(t *testing.T, stateDir string, policies ...mcpregistration.AutoRegisterPolicy) {
	t.Helper()
	manifest := mcpregistration.NewManifest()
	manifest.Policies = append(manifest.Policies, policies...)
	if err := mcpregistration.SaveManifest(mcpregistration.ManifestPath(stateDir), manifest, false); err != nil {
		t.Fatal(err)
	}
}

func TestAutoRegistrationOffByDefault(t *testing.T) {
	paths := testenv.Isolate(t)
	root, _ := indexedRepo(t, filepath.Join(paths.Root, "work"), "billing-api")
	config := filepath.Join(paths.Root, "clients", "claude.json")
	result, err := (mcpregistration.Reconciler{StateDir: state.StateDir(), Binary: "/opt/graphi"}).Reconcile(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Changes) != 0 || len(result.Failures) != 0 {
		t.Fatalf("policy-free reconcile = %#v", result)
	}
	if _, err := os.Stat(config); !os.IsNotExist(err) {
		t.Fatalf("policy-free reconcile wrote a config: %v", err)
	}
}

func TestOnlyConsentedClientsUpdated(t *testing.T) {
	paths := testenv.Isolate(t)
	root, _ := indexedRepo(t, filepath.Join(paths.Root, "work"), "billing-api")
	claudePath := filepath.Join(paths.Root, "clients", "claude.json")
	codexPath := filepath.Join(paths.Root, "clients", "config.toml")
	savePolicies(t, state.StateDir(),
		mcpregistration.AutoRegisterPolicy{ClientID: "claude", ConfigPath: claudePath, Enabled: true},
		mcpregistration.AutoRegisterPolicy{ClientID: "codex", ConfigPath: codexPath, Enabled: false},
	)
	result, err := (mcpregistration.Reconciler{StateDir: state.StateDir(), Binary: "/opt/graphi"}).Reconcile(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Changes) != 1 || result.Changes[0].ClientID != "claude" {
		t.Fatalf("changes = %#v, want only consented Claude target", result.Changes)
	}
	if _, err := os.Stat(claudePath); err != nil {
		t.Fatalf("consented config missing: %v", err)
	}
	if _, err := os.Stat(codexPath); !os.IsNotExist(err) {
		t.Fatalf("disabled target changed: %v", err)
	}
}

func TestNewlyInstalledClientNotAutoEnabled(t *testing.T) {
	paths := testenv.Isolate(t)
	root, _ := indexedRepo(t, filepath.Join(paths.Root, "work"), "billing-api")
	claudePath := filepath.Join(paths.Root, "clients", "claude.json")
	devinPath := filepath.Join(paths.Home, ".config", "devin", "mcp_config.json")
	if err := os.MkdirAll(filepath.Dir(devinPath), 0o700); err != nil {
		t.Fatal(err)
	}
	before := []byte(`{"mcpServers":{"foreign":{"command":"foreign"}}}`)
	if err := os.WriteFile(devinPath, before, 0o600); err != nil {
		t.Fatal(err)
	}
	savePolicies(t, state.StateDir(), mcpregistration.AutoRegisterPolicy{ClientID: "claude", ConfigPath: claudePath, Enabled: true})
	if _, err := (mcpregistration.Reconciler{StateDir: state.StateDir(), Binary: "/opt/graphi"}).Reconcile(root); err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadFile(devinPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(before) {
		t.Fatalf("newly installed, unconsented Devin config changed: %s", after)
	}
}

func TestChangedConfigTargetRequiresConsent(t *testing.T) {
	paths := testenv.Isolate(t)
	root, _ := indexedRepo(t, filepath.Join(paths.Root, "work"), "billing-api")
	consented := filepath.Join(paths.Root, "consented", "claude.json")
	savePolicies(t, state.StateDir(), mcpregistration.AutoRegisterPolicy{ClientID: "claude", ConfigPath: consented, Enabled: true})

	newHome := filepath.Join(paths.Root, "new-home")
	if err := os.MkdirAll(newHome, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", newHome)
	resolved := filepath.Join(newHome, ".claude.json")
	before := []byte(`{"mcpServers":{"foreign":{"command":"foreign"}}}`)
	if err := os.WriteFile(resolved, before, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := (mcpregistration.Reconciler{StateDir: state.StateDir(), Binary: "/opt/graphi"}).Reconcile(root); err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadFile(resolved)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(before) {
		t.Fatalf("newly resolved config target changed without consent: %s", after)
	}
	if _, err := os.Stat(consented); err != nil {
		t.Fatalf("frozen consented target was not updated: %v", err)
	}
}

func TestPartialClientFailureKeepsIndex(t *testing.T) {
	paths := testenv.Isolate(t)
	root, repoPaths := indexedRepo(t, filepath.Join(paths.Root, "work"), "billing-api")
	broken := filepath.Join(paths.Root, "clients", "claude.json")
	valid := filepath.Join(paths.Root, "clients", "config.toml")
	if err := os.MkdirAll(filepath.Dir(broken), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(broken, []byte(`{"mcpServers":`), 0o600); err != nil {
		t.Fatal(err)
	}
	savePolicies(t, state.StateDir(),
		mcpregistration.AutoRegisterPolicy{ClientID: "claude", ConfigPath: broken, Enabled: true},
		mcpregistration.AutoRegisterPolicy{ClientID: "codex", ConfigPath: valid, Enabled: true},
	)
	result, err := (mcpregistration.Reconciler{StateDir: state.StateDir(), Binary: "/opt/graphi"}).Reconcile(root)
	if err == nil || len(result.Failures) != 1 || len(result.Changes) != 1 {
		t.Fatalf("partial reconcile result=%#v err=%v", result, err)
	}
	if _, err := mcpregistration.NewResolver(state.StateDir()).Resolve(root); err != nil {
		t.Fatalf("registration failure damaged valid index %s: %v", repoPaths.DB, err)
	}
	raw, err := os.ReadFile(valid)
	if err != nil || !strings.Contains(string(raw), "graphi-billing-api") {
		t.Fatalf("healthy client did not complete after sibling failure: %v\n%s", err, raw)
	}
}

func TestDisableStopsFutureWrites(t *testing.T) {
	paths := testenv.Isolate(t)
	root, _ := indexedRepo(t, filepath.Join(paths.Root, "work"), "billing-api")
	config := filepath.Join(paths.Root, "clients", "claude.json")
	savePolicies(t, state.StateDir(), mcpregistration.AutoRegisterPolicy{ClientID: "claude", ConfigPath: config, Enabled: false})
	if _, err := (mcpregistration.Reconciler{StateDir: state.StateDir(), Binary: "/opt/graphi"}).Reconcile(root); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(config); !os.IsNotExist(err) {
		t.Fatalf("disabled policy wrote config: %v", err)
	}
}

func TestParallelSyncsDoNotLoseEntries(t *testing.T) {
	paths := testenv.Isolate(t)
	first, _ := indexedRepo(t, filepath.Join(paths.Root, "work-a"), "billing-api")
	second, _ := indexedRepo(t, filepath.Join(paths.Root, "work-b"), "catalog-api")
	config := filepath.Join(paths.Root, "clients", "claude.json")
	savePolicies(t, state.StateDir(), mcpregistration.AutoRegisterPolicy{ClientID: "claude", ConfigPath: config, Enabled: true})
	reconciler := mcpregistration.Reconciler{StateDir: state.StateDir(), Binary: "/opt/graphi"}

	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for _, root := range []string{first, second} {
		wg.Add(1)
		go func(root string) {
			defer wg.Done()
			_, err := reconciler.Reconcile(root)
			errs <- err
		}(root)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	client, _ := mcpconfig.ClientByID("claude")
	entries, err := client.WithConfigPath(config).Entries()
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 || entries["graphi-billing-api"] == nil || entries["graphi-catalog-api"] == nil {
		t.Fatalf("parallel config entries = %#v", entries)
	}
	manifest, err := mcpregistration.LoadManifest(mcpregistration.ManifestPath(state.StateDir()))
	if err != nil {
		t.Fatal(err)
	}
	if len(manifest.Registrations) != 2 || len(manifest.Receipts) != 2 {
		t.Fatalf("parallel manifest lost state: registrations=%d receipts=%d", len(manifest.Registrations), len(manifest.Receipts))
	}
}

func TestDirectSetupRecoversCompletedPendingReceipt(t *testing.T) {
	paths := testenv.Isolate(t)
	root, _ := indexedRepo(t, filepath.Join(paths.Root, "work"), "billing-api")
	store, err := mcpregistration.NewResolver(state.StateDir()).Resolve(root)
	if err != nil {
		t.Fatal(err)
	}
	config := filepath.Join(paths.Root, "clients", "claude.json")
	client, _ := mcpconfig.ClientByID("claude")
	client = client.WithConfigPath(config)
	entry := mcpconfig.GraphiEntry("/opt/graphi", []string{"mcp", "-db", store.DB, "-meta", store.Meta})
	plan, err := client.PlanEntryState("graphi-billing-api", entry)
	if err != nil {
		t.Fatal(err)
	}
	receipt, err := mcpregistration.NewClientReceipt(store.CheckoutID, client.ID, config, "graphi-billing-api", entry, plan.Target)
	if err != nil {
		t.Fatal(err)
	}
	manifest := mcpregistration.NewManifest()
	manifest.Registrations = []mcpregistration.Registration{{CheckoutID: store.CheckoutID, Name: "graphi-billing-api", RepoFile: store.RepoFile}}
	manifest.Pending = []mcpregistration.PendingChange{{
		ID: store.CheckoutID + ":claude:graphi-billing-api", BeforeDigest: plan.BeforeConfigDigest,
		TargetDigest: plan.TargetConfigDigest, Receipt: receipt,
	}}
	if err := mcpregistration.SaveManifest(mcpregistration.ManifestPath(state.StateDir()), manifest, false); err != nil {
		t.Fatal(err)
	}
	if _, err := client.ApplyEntryObserved("graphi-billing-api", entry, plan.BeforeConfigDigest, false); err != nil {
		t.Fatal(err)
	}

	if _, err := (mcpregistration.Service{StateDir: state.StateDir(), Binary: "/opt/graphi"}).Execute(mcpregistration.ServiceRequest{Roots: []string{root}, Clients: []mcpconfig.Client{client}}); err != nil {
		t.Fatalf("setup restart did not recover completed pending write: %v", err)
	}
	got, err := mcpregistration.LoadManifest(mcpregistration.ManifestPath(state.StateDir()))
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Pending) != 0 || len(got.Receipts) != 1 {
		t.Fatalf("recovered manifest = %#v", got)
	}
}

func TestUnregisterAfterCompletedPendingAddStillRemovesEntry(t *testing.T) {
	paths := testenv.Isolate(t)
	root, _ := indexedRepo(t, filepath.Join(paths.Root, "work"), "billing-api")
	store, err := mcpregistration.NewResolver(state.StateDir()).Resolve(root)
	if err != nil {
		t.Fatal(err)
	}
	config := filepath.Join(paths.Root, "clients", "claude.json")
	client, _ := mcpconfig.ClientByID("claude")
	client = client.WithConfigPath(config)
	entry := mcpconfig.GraphiEntry("/opt/graphi", []string{"mcp", "-db", store.DB, "-meta", store.Meta})
	plan, err := client.PlanEntryState("graphi-billing-api", entry)
	if err != nil {
		t.Fatal(err)
	}
	receipt, err := mcpregistration.NewClientReceipt(store.CheckoutID, client.ID, config, "graphi-billing-api", entry, plan.Target)
	if err != nil {
		t.Fatal(err)
	}
	manifest := mcpregistration.NewManifest()
	manifest.Registrations = []mcpregistration.Registration{{CheckoutID: store.CheckoutID, Name: "graphi-billing-api", RepoFile: store.RepoFile}}
	manifest.Pending = []mcpregistration.PendingChange{{
		ID: store.CheckoutID + ":claude:graphi-billing-api", BeforeDigest: plan.BeforeConfigDigest,
		TargetDigest: plan.TargetConfigDigest, Receipt: receipt,
	}}
	manifestPath := mcpregistration.ManifestPath(state.StateDir())
	if err := mcpregistration.SaveManifest(manifestPath, manifest, false); err != nil {
		t.Fatal(err)
	}
	if _, err := client.ApplyEntryObserved("graphi-billing-api", entry, plan.BeforeConfigDigest, false); err != nil {
		t.Fatal(err)
	}

	request := mcpregistration.ServiceRequest{Roots: []string{root}, Clients: []mcpconfig.Client{client}, Unregister: true}
	if _, err := (mcpregistration.Service{StateDir: state.StateDir(), Binary: "/opt/graphi"}).Execute(request); err != nil {
		t.Fatalf("unregister after recovered add failed: %v", err)
	}
	entries, err := client.Entries()
	if err != nil {
		t.Fatal(err)
	}
	if entries["graphi-billing-api"] != nil {
		t.Fatalf("unregister skipped recovered add entry: %#v", entries)
	}
	got, err := mcpregistration.LoadManifest(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Pending) != 0 || len(got.Receipts) != 0 || len(got.Registrations) != 0 {
		t.Fatalf("unregister retained recovered add state: %#v", got)
	}
}

func TestDirectUnregisterRecoversCompletedPendingRemoval(t *testing.T) {
	paths := testenv.Isolate(t)
	root, _ := indexedRepo(t, filepath.Join(paths.Root, "work"), "billing-api")
	config := filepath.Join(paths.Root, "clients", "claude.json")
	client, _ := mcpconfig.ClientByID("claude")
	client = client.WithConfigPath(config)
	service := mcpregistration.Service{StateDir: state.StateDir(), Binary: "/opt/graphi"}
	request := mcpregistration.ServiceRequest{Roots: []string{root}, Clients: []mcpconfig.Client{client}}
	if _, err := service.Execute(request); err != nil {
		t.Fatal(err)
	}
	manifestPath := mcpregistration.ManifestPath(state.StateDir())
	manifest, err := mcpregistration.LoadManifest(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := client.PlanRemoveEntryState("graphi-billing-api")
	if err != nil {
		t.Fatal(err)
	}
	manifest.Pending = []mcpregistration.PendingChange{{
		ID: manifest.Receipts[0].CheckoutID + ":claude:graphi-billing-api", BeforeDigest: plan.BeforeConfigDigest,
		TargetDigest: plan.TargetConfigDigest, Receipt: manifest.Receipts[0], Remove: true,
	}}
	if err := mcpregistration.SaveManifest(manifestPath, manifest, false); err != nil {
		t.Fatal(err)
	}
	if _, err := client.RemoveEntryObserved("graphi-billing-api", plan.Current, plan.BeforeConfigDigest, false); err != nil {
		t.Fatal(err)
	}

	request.Unregister = true
	if _, err := service.Execute(request); err != nil {
		t.Fatalf("unregister restart did not recover completed removal: %v", err)
	}
	got, err := mcpregistration.LoadManifest(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Pending) != 0 || len(got.Receipts) != 0 || len(got.Registrations) != 0 {
		t.Fatalf("recovered removal manifest = %#v", got)
	}
}
