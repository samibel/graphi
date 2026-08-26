package opcatalog

import (
	"errors"
	"sort"
	"testing"

	"github.com/samibel/graphi/core/registry"
)

// spec builds a minimal valid spec for the lifecycle tests.
func spec(id string, ports ...Port) OperationSpec {
	if len(ports) == 0 {
		ports = []Port{PortGraphQuery}
	}
	sorted := append([]Port(nil), ports...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	determinism := DeterminismDeterministic
	for _, p := range sorted {
		switch {
		case externalPorts[p]:
			determinism = DeterminismExternal
		case nonDeterministicPorts[p] && determinism == DeterminismDeterministic:
			determinism = DeterminismEnvironmentDependent
		case p == PortsUnaudited:
			determinism = DeterminismUnaudited
		}
	}
	return OperationSpec{
		ID:            id,
		Version:       "1",
		Tier:          TierLabs,
		Advertisement: Advertisement{Description: "test operation " + id},
		Ports:         sorted,
		Permissions:   PermissionsFor(sorted),
		Determinism:   determinism,
		PortsEvidence: "catalog_test.go fixture",
	}
}

// AC-1 — the catalog is immutable after construction, and the refusal is a
// TYPED error a caller can match rather than a panic or a silent no-op.
func TestCatalog_AddAfterBuild_FailsWithErrFrozen(t *testing.T) {
	catalog, err := seeded(t, spec("alpha")).Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if !catalog.Frozen() {
		t.Fatal("Build did not freeze the catalog")
	}
	err = catalog.Add(spec("beta"))
	if err == nil {
		t.Fatal("Add after Build succeeded; the catalog is not immutable")
	}
	if !errors.Is(err, registry.ErrFrozen) {
		t.Fatalf("Add after Build returned %v, want a registry.ErrFrozen-typed error", err)
	}
	if got := catalog.Len(); got != 1 {
		t.Fatalf("a refused Add still changed the catalog: len = %d, want 1", got)
	}
	if _, ok := catalog.Lookup("beta"); ok {
		t.Fatal("a refused Add still registered the spec")
	}
}

// AC-1 — the freeze must NOT inherit core/registry's GRAPHI_REGISTRY_FREEZE
// rollback switch. That switch restores pre-AX-02 behaviour for registries that
// used to be mutable; the catalog never was, so disarming enforcement must not
// open a door that never existed.
func TestCatalog_FreezeIgnoresTheRegistryRollbackSwitch(t *testing.T) {
	restore := registry.SetFreezeEnforced(false)
	defer restore()

	catalog, err := seeded(t, spec("alpha")).Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if err := catalog.Add(spec("beta")); !errors.Is(err, registry.ErrFrozen) {
		t.Fatalf("with freeze enforcement disabled, Add after Build returned %v; "+
			"the catalog must stay immutable regardless", err)
	}
}

// AC-3's duplicate half at the catalog level: an id registered twice is
// ErrDuplicate, not last-wins. An operation id is a frozen wire identifier, so
// silently superseding one would be exactly the shadowing ADR 0013 T5 names.
func TestCatalog_DuplicateID_IsRejected(t *testing.T) {
	catalog := New()
	if err := catalog.Add(spec("alpha")); err != nil {
		t.Fatalf("first Add: %v", err)
	}
	err := catalog.Add(spec("alpha", PortGraphSearch))
	if !errors.Is(err, registry.ErrDuplicate) {
		t.Fatalf("duplicate Add returned %v, want registry.ErrDuplicate", err)
	}
	got, ok := catalog.Lookup("alpha")
	if !ok {
		t.Fatal("the first registration was lost")
	}
	if len(got.Ports) != 1 || got.Ports[0] != PortGraphQuery {
		t.Fatalf("first-wins violated: ports = %v", got.Ports)
	}
}

// Determinism: iteration order is canonical (sorted by id) regardless of
// registration order. A projection built in map order would produce a different
// tools/list on every run.
func TestCatalog_IterationOrderIsCanonical(t *testing.T) {
	catalog := seeded(t, spec("zulu"), spec("alpha"), spec("mike"))
	want := []string{"alpha", "mike", "zulu"}
	for i, id := range catalog.IDs() {
		if id != want[i] {
			t.Fatalf("IDs() = %v, want %v", catalog.IDs(), want)
		}
	}
	all := catalog.All()
	for i, s := range all {
		if s.ID != want[i] {
			t.Fatalf("All() order = %v, want %v", ids(all), want)
		}
	}
}

// Accessors hand out copies: a consumer editing what it got back cannot reach
// the catalog another consumer is reading.
func TestCatalog_AccessorsReturnCopies(t *testing.T) {
	catalog, err := seeded(t, spec("alpha", PortGraphQuery, PortSourceRead)).Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	got, _ := catalog.Lookup("alpha")
	got.Ports[0] = PortForgePublish
	got.Permissions[0] = PermissionNetworkEgress

	again, _ := catalog.Lookup("alpha")
	if again.Ports[0] == PortForgePublish || again.Permissions[0] == PermissionNetworkEgress {
		t.Fatalf("mutating a returned spec reached the catalog: %v %v", again.Ports, again.Permissions)
	}

	list := catalog.IDs()
	list[0] = "tampered"
	if catalog.IDs()[0] != "alpha" {
		t.Fatal("mutating the returned id slice reached the catalog")
	}
}

func TestCatalog_BuildIsIdempotent(t *testing.T) {
	catalog := seeded(t, spec("alpha"))
	if _, err := catalog.Build(); err != nil {
		t.Fatalf("first Build: %v", err)
	}
	if _, err := catalog.Build(); err != nil {
		t.Fatalf("second Build: %v", err)
	}
}

func TestCatalog_BuildRejectsAnInvalidSpec(t *testing.T) {
	catalog := New()
	bad := spec("alpha")
	bad.Tier = "preview"
	if err := catalog.Add(bad); err == nil {
		t.Fatal("Add accepted an unknown tier")
	}
	if catalog.Frozen() {
		t.Fatal("a rejected Add froze the catalog")
	}
}

func TestCatalog_IDsWithTier(t *testing.T) {
	stable := spec("alpha")
	stable.Tier = TierStable
	catalog := seeded(t, stable, spec("beta"))
	if got := catalog.IDsWithTier(TierStable); len(got) != 1 || got[0] != "alpha" {
		t.Fatalf("IDsWithTier(stable) = %v, want [alpha]", got)
	}
	if got := catalog.IDsWithTier(TierLabs); len(got) != 1 || got[0] != "beta" {
		t.Fatalf("IDsWithTier(labs) = %v, want [beta]", got)
	}
}

func TestCatalog_PolicyIsFirstWins(t *testing.T) {
	if got := New().Policy(); got != registry.PolicyFirstWins {
		t.Fatalf("Policy() = %s, want %s", got, registry.PolicyFirstWins)
	}
}

func seeded(t *testing.T, specs ...OperationSpec) *Catalog {
	t.Helper()
	catalog := New()
	for _, s := range specs {
		if err := catalog.Add(s); err != nil {
			t.Fatalf("Add(%s): %v", s.ID, err)
		}
	}
	return catalog
}

func ids(specs []OperationSpec) []string {
	out := make([]string, len(specs))
	for i, s := range specs {
		out[i] = s.ID
	}
	return out
}

// TestCatalog_Supports pins the version-aware lookup the AX-04 executor rejects
// unknown work with. The three negative cases are the ones AC-5 of SW-224 names:
// an id nobody registered, a version nobody declared, and — deliberately — an
// EMPTY version, which is not "no opinion" but "no declared version" and must
// not silently resolve to whatever the catalog happens to hold.
func TestCatalog_Supports(t *testing.T) {
	catalog := seeded(t, spec("alpha"))
	if _, err := catalog.Build(); err != nil {
		t.Fatalf("Build: %v", err)
	}
	for _, tc := range []struct {
		name    string
		id      string
		version string
		want    bool
	}{
		{"declared id and version", "alpha", "1", true},
		{"unknown id", "beta", "1", false},
		{"unsupported version", "alpha", "2", false},
		{"empty version is not a wildcard", "alpha", "", false},
		{"empty id", "", "1", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := catalog.Supports(tc.id, tc.version); got != tc.want {
				t.Fatalf("Supports(%q, %q) = %v, want %v", tc.id, tc.version, got, tc.want)
			}
		})
	}
}

// TestCatalog_VersionOf reports the declared version so a caller can name the
// supported one in its rejection message instead of saying only "no".
func TestCatalog_VersionOf(t *testing.T) {
	catalog := seeded(t, spec("alpha"))
	if got, ok := catalog.VersionOf("alpha"); !ok || got != "1" {
		t.Fatalf("VersionOf(alpha) = %q, %v; want \"1\", true", got, ok)
	}
	if got, ok := catalog.VersionOf("beta"); ok || got != "" {
		t.Fatalf("VersionOf(beta) = %q, %v; want \"\", false", got, ok)
	}
}
