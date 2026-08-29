// SW-227 (AX-07): the evidence that the RuntimeBuilder composition and the
// composition it replaced are indistinguishable, and that what it hands the
// surfaces is immutable.
//
// The characterization harness lives in startpath_characterization_test.go and
// was written and committed BEFORE this refactor. Here it is replayed under both
// composition modes over identical inputs.
package runtime

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/samibel/graphi/core/parse"
	"github.com/samibel/graphi/core/registry"
	"github.com/samibel/graphi/engine/module"
)

// withCompositionMode installs a composition mode for one test.
func withCompositionMode(t *testing.T, mode CompositionMode) {
	t.Helper()
	restore, err := SetCompositionMode(mode)
	if err != nil {
		t.Fatalf("SetCompositionMode(%q): %v", mode, err)
	}
	t.Cleanup(restore)
}

// TestAX07_EveryStartPathIsIdenticalAcrossCompositions is AC-4 and AC-6 at once.
//
// The baseline is captured in LEGACY position — the pre-AX-07 composition, which
// is compiled in verbatim — and the builder position is compared against THAT,
// not against itself. A third legacy capture runs afterwards as the control: if
// the two legacy captures already disagreed, an agreement between legacy and
// builder would prove nothing, and this test would be comparing noise.
func TestAX07_EveryStartPathIsIdenticalAcrossCompositions(t *testing.T) {
	repo := charRepository(t)

	capture := func(mode CompositionMode) string {
		restore, err := SetCompositionMode(mode)
		if err != nil {
			t.Fatalf("SetCompositionMode(%q): %v", mode, err)
		}
		defer restore()
		if CompositionModeSetting() != mode {
			t.Fatalf("composition mode did not take: %q", CompositionModeSetting())
		}
		return renderRecords(t, captureStartPaths(t, repo, t.TempDir()))
	}

	legacy := capture(CompositionLegacy)
	builder := capture(CompositionBuilder)
	control := capture(CompositionLegacy)

	if legacy != control {
		t.Fatalf("the LEGACY composition is not reproducible across two captures, so the "+
			"comparison below would prove nothing\n--- first ---\n%s\n--- control ---\n%s", legacy, control)
	}
	if legacy != builder {
		t.Fatalf("the builder composition differs from the legacy composition\n"+
			"--- legacy (pre-AX-07) ---\n%s\n--- builder (AX-07) ---\n%s", legacy, builder)
	}
}

// TestAX07_BothEntryPointsComposeThroughTheBuilder is AC-1: the capability
// wiring reachable from Attach and from OpenSession goes through the module set,
// once per runtime, and the client the surfaces receive is the one the
// composition produced — not a second client built beside it.
func TestAX07_BothEntryPointsComposeThroughTheBuilder(t *testing.T) {
	withCompositionMode(t, CompositionBuilder)
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	repo := charRepository(t)

	session, err := OpenSession(context.Background(), Options{Cwd: repo})
	if err != nil {
		t.Fatalf("OpenSession: %v", err)
	}
	defer session.Close()

	attached, err := Attach(session.DBPath, "", session.MetaDir)
	if err != nil {
		t.Fatalf("Attach: %v", err)
	}
	defer attached.Close()

	for _, tc := range []struct {
		name string
		rt   *Runtime
	}{
		{"OpenSession", session},
		{"Attach", attached},
	} {
		t.Run(tc.name, func(t *testing.T) {
			comp := tc.rt.Composition()
			if comp == nil {
				t.Fatal("no composition: this entry point did not go through the builder")
			}
			// Four since SW-255 (AX-15): engine.deadcode joined the three
			// AX-07 modules, ordered before engine.operations.
			if got, want := comp.Modules(), 4; len(got) != want {
				t.Fatalf("composed %d modules, want %d", len(got), want)
			}
			ids := comp.Contributions().ModuleIDs()
			for i, want := range []string{module.IDParse, module.IDAnalysis, module.IDDeadCode, module.IDOperations} {
				if ids[i] != want {
					t.Fatalf("module order = %v, want the deterministic built-in order", ids)
				}
			}
			// The client the Runtime handed out IS the composition's client;
			// composing a second one beside it is the failure this catches.
			first := comp.Client()
			if tc.rt.Client != first {
				t.Fatal("the runtime's client is not the composition's client")
			}
			if comp.Client() != first {
				t.Fatal("Composition.Client is not memoized")
			}
		})
	}

	// The daemon-socket attach composes nothing local, and says so rather than
	// pretending to have a composition.
	socketOnly, err := Attach("", filepath.Join(t.TempDir(), "absent.sock"), "")
	if err != nil {
		t.Fatalf("Attach(socket): %v", err)
	}
	defer socketOnly.Close()
	if socketOnly.Composition() != nil {
		t.Error("a daemon attach reported a local composition")
	}
}

// TestAX07_TheCompositionIsFrozenAndHasNoMutationPath is AC-3.
//
// "Immutable" is asserted mechanically rather than by reading the type: every
// registry the composition hands out refuses a post-build mutation with a
// registry.ErrFrozen-typed error, and the module set that produced it refuses to
// build again.
func TestAX07_TheCompositionIsFrozenAndHasNoMutationPath(t *testing.T) {
	withCompositionMode(t, CompositionBuilder)
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	repo := charRepository(t)

	rt, err := OpenSession(context.Background(), Options{Cwd: repo})
	if err != nil {
		t.Fatalf("OpenSession: %v", err)
	}
	defer rt.Close()

	comp := rt.Composition()
	if comp == nil {
		t.Fatal("no composition")
	}
	if !comp.Frozen() {
		t.Fatal("Composition.Frozen() = false")
	}
	if perr := comp.Parsers().Register(parse.NewGoParser()); !errors.Is(perr, registry.ErrFrozen) {
		t.Errorf("the session parser registry accepted a post-build Register: %v", perr)
	}
	if !comp.Analysis().Frozen() {
		t.Error("the session analysis service is not frozen")
	}
	if !comp.Contributions().Operations().Frozen() {
		t.Error("the session operation catalog is not frozen")
	}
	if !comp.Contributions().Resolvers().Frozen() {
		t.Error("the session resolver registry is not frozen")
	}

	// A builder is single-use: it cannot be kept and re-run against the same
	// store to produce a second, competing set of registries.
	b := NewBuilder(rt.Store()).WithMetaDir(rt.MetaDir).WithRepoRoot(rt.Root)
	if _, berr := b.Build(); berr != nil {
		t.Fatalf("first Build: %v", berr)
	}
	if _, berr := b.Build(); berr == nil {
		t.Error("a builder composed twice")
	}
}

// TestAX07_TheIngesterParsesThroughTheComposedRegistry proves the parser
// registry the session ingests with is the module set's — not a second default
// registry constructed inline beside it. Without this, "the parsers are a module
// contribution" would be true of a registry nothing used.
func TestAX07_TheIngesterParsesThroughTheComposedRegistry(t *testing.T) {
	withCompositionMode(t, CompositionBuilder)
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	repo := charRepository(t)

	rt, err := OpenSession(context.Background(), Options{Cwd: repo})
	if err != nil {
		t.Fatalf("OpenSession: %v", err)
	}
	defer rt.Close()

	if rt.ing == nil {
		t.Fatal("the session has no ingester")
	}
	composed := rt.Composition().Parsers()
	if sessionParsers(rt.Composition()) != composed {
		t.Fatal("sessionParsers did not return the composed registry")
	}
	// And the legacy branch of the same helper is the stand-alone default
	// registry, so the rollback path still gets parsers at all.
	if fallback := sessionParsers(nil); fallback == nil || !fallback.Frozen() {
		t.Fatal("the legacy parser fallback is missing or unfrozen")
	}
}

// TestAX07_CompositionModeFailsClosed: an unknown mode is refused rather than
// silently resolved to the default. Same discipline as SW-226's canary switch —
// a caller who asked for a composition that does not exist must find out.
func TestAX07_CompositionModeFailsClosed(t *testing.T) {
	if got := defaultCompositionMode; got != CompositionBuilder {
		t.Fatalf("the shipped composition default is %q; AX-07's position of record is %q", got, CompositionBuilder)
	}
	for _, bad := range []string{"", "Builder", "legacy ", "lecacy", "none"} {
		if _, err := ParseCompositionMode(bad); err == nil {
			t.Errorf("ParseCompositionMode(%q) accepted an unknown mode", bad)
		}
	}
	for _, good := range []CompositionMode{CompositionBuilder, CompositionLegacy} {
		restore, err := SetCompositionMode(good)
		if err != nil {
			t.Fatalf("SetCompositionMode(%q): %v", good, err)
		}
		if CompositionModeSetting() != good {
			t.Fatalf("mode = %q, want %q", CompositionModeSetting(), good)
		}
		restore()
	}
	if CompositionModeSetting() != defaultCompositionMode {
		t.Fatalf("restore left the mode at %q", CompositionModeSetting())
	}
}

// TestAX07_LegacyCompositionRemainsExecutable is AC-6's direct half: the prior
// path is not merely compiled in, it runs and serves.
func TestAX07_LegacyCompositionRemainsExecutable(t *testing.T) {
	withCompositionMode(t, CompositionLegacy)
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	repo := charRepository(t)

	rt, err := OpenSession(context.Background(), Options{Cwd: repo})
	if err != nil {
		t.Fatalf("OpenSession on the legacy composition: %v", err)
	}
	defer rt.Close()
	if rt.Composition() != nil {
		t.Error("the legacy composition reported a builder composition")
	}
	body, err := rt.Client.Search(context.Background(), "Greet", 10)
	if err != nil || len(body) == 0 {
		t.Fatalf("the legacy composition did not answer: %v (%d bytes)", err, len(body))
	}

	attached, err := Attach(rt.DBPath, "", rt.MetaDir)
	if err != nil {
		t.Fatalf("Attach on the legacy composition: %v", err)
	}
	defer attached.Close()
	if attached.Composition() != nil {
		t.Error("the legacy attach reported a builder composition")
	}
	if body, aerr := attached.Client.Search(context.Background(), "Greet", 10); aerr != nil || len(body) == 0 {
		t.Fatalf("the legacy attach did not answer: %v (%d bytes)", aerr, len(body))
	}
}
