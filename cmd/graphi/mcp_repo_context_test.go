package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/samibel/graphi/internal/mcpregistration"
	"github.com/samibel/graphi/internal/state"
	"github.com/samibel/graphi/internal/testenv"
)

func registeredAttachFixture(t *testing.T, name, label string) (string, state.Paths) {
	t.Helper()
	isolated := testenv.Isolate(t)
	root, paths := indexedSetupRepo(t, filepath.Join(isolated.Root, "work"), name)
	manifest := mcpregistration.NewManifest()
	manifest.Registrations = []mcpregistration.Registration{{
		CheckoutID: paths.Fingerprint,
		Name:       label,
		RepoFile:   paths.RepoFile,
	}}
	if err := mcpregistration.SaveManifest(mcpregistration.ManifestPath(state.StateDir()), manifest, false); err != nil {
		t.Fatal(err)
	}
	return root, paths
}

func TestManagedAttachContextResolution(t *testing.T) {
	root, paths := registeredAttachFixture(t, "billing-api", "graphi-billing-api")
	context, err := resolveAttachRepoContext(state.StateDir(), paths.DB, paths.Meta)
	if err != nil {
		t.Fatal(err)
	}
	if context.CheckoutID != paths.Fingerprint || context.Root != root || context.Label != "graphi-billing-api" {
		t.Fatalf("context = %#v", context)
	}
}

func TestStartCWDDoesNotChangePinnedStore(t *testing.T) {
	root, paths := registeredAttachFixture(t, "billing-api", "graphi-billing-api")
	other := filepath.Join(t.TempDir(), "catalog-api")
	if err := os.MkdirAll(filepath.Join(other, ".git"), 0o700); err != nil {
		t.Fatal(err)
	}
	oldCWD, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(other); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(oldCWD) })

	context, err := resolveAttachRepoContext(state.StateDir(), paths.DB, paths.Meta)
	if err != nil {
		t.Fatal(err)
	}
	if context.Root != root || context.Label != "graphi-billing-api" {
		t.Fatalf("start cwd rerouted pinned store: %#v", context)
	}
}

func TestManagedDescriptorMismatchFailsClosed(t *testing.T) {
	_, paths := registeredAttachFixture(t, "billing-api", "graphi-billing-api")
	descriptor := state.RepoDescriptor{AbsRoot: filepath.Join(t.TempDir(), "replacement"), Fingerprint: paths.Fingerprint, Created: "-"}
	raw, err := json.Marshal(descriptor)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(paths.RepoFile, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := resolveAttachRepoContext(state.StateDir(), paths.DB, paths.Meta); err == nil {
		t.Fatal("managed attach accepted contradictory repo metadata")
	}
}

func TestExternalAttachContextIsUnknown(t *testing.T) {
	testenv.Isolate(t)
	external := filepath.Join(t.TempDir(), "external.sqlite")
	context, err := resolveAttachRepoContext(state.StateDir(), external, filepath.Join(t.TempDir(), "meta"))
	if err != nil {
		t.Fatal(err)
	}
	if context.CheckoutID != "" || context.Root != "" || context.Label != "" {
		t.Fatalf("external attach invented provenance: %#v", context)
	}
}
