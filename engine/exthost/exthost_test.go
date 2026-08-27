package exthost

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/samibel/graphi/core/registry"
	"github.com/samibel/graphi/engine/extpack"
	"github.com/samibel/graphi/engine/opcatalog"
)

// AC-1 — one read-only example analyzer, as an external process, over a
// versioned stdio protocol.
//
// The assertion is deliberately end-to-end and byte-exact: a spike that proved
// "a process started" would not have proved that a USEFUL answer crossed the
// boundary. The census below is computed by a separate executable from data it
// could only have obtained through the host's graph.search port.
func TestSW231_AC1_ReadOnlyAnalyzerAnswersOverAnExternalProcess(t *testing.T) {
	ext, port := startExample(t, stageExtension(t, descriptorOptions{}), "")

	if got := ext.Operations(); len(got) != 1 || got[0] != exampleOperation {
		t.Fatalf("advertised operations = %v, want [%s]", got, exampleOperation)
	}
	res, err := ext.Call(callCtx(t), exampleOperation, json.RawMessage(`{"symbol":"Hel"}`))
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	const want = `{"symbol":"Hel","total":4,"by_path":[` +
		`{"path":"pkg/a/chart.go","count":1,"example":"Helm"},` +
		`{"path":"pkg/a/greet.go","count":2,"example":"Held"},` +
		`{"path":"pkg/b/util.go","count":1,"example":"Helper"}` +
		`],"analyzer":"example-analyzer@0.1.0"}`
	if string(res.Findings) != want {
		t.Fatalf("findings =\n%s\nwant\n%s", res.Findings, want)
	}
	if seen := port.seen(); len(seen) != 1 || seen[0] != `{"symbol":"Hel"}` {
		t.Fatalf("graph.search saw %v, want exactly one query for \"Hel\"", seen)
	}
}

// AC-1 — activation is explicit opt-in configuration, never discovery.
//
// Four shapes, because "explicit" has four ways of being violated and only the
// first is obvious.
func TestSW231_AC1_ActivationIsExplicitOptIn(t *testing.T) {
	descriptor := stageExtension(t, descriptorOptions{})

	t.Run("the zero config starts nothing", func(t *testing.T) {
		if _, err := Start(context.Background(), Config{}); !errors.Is(err, ErrNotActivated) {
			t.Fatalf("Start(Config{}) = %v, want ErrNotActivated", err)
		}
	})
	t.Run("a descriptor path without activation starts nothing", func(t *testing.T) {
		_, err := Start(context.Background(), Config{DescriptorPath: descriptor})
		if !errors.Is(err, ErrNotActivated) {
			t.Fatalf("Start(unactivated) = %v, want ErrNotActivated", err)
		}
	})
	t.Run("activation without a named descriptor starts nothing", func(t *testing.T) {
		_, err := Start(context.Background(), Config{Activated: true})
		if !errors.Is(err, ErrNotActivated) {
			t.Fatalf("Start(no descriptor) = %v, want ErrNotActivated", err)
		}
	})
	t.Run("a DIRECTORY of extensions is refused, not searched", func(t *testing.T) {
		dir := filepath.Dir(descriptor)
		_, err := Start(context.Background(), Config{Activated: true, DescriptorPath: dir})
		if !errors.Is(err, ErrDescriptor) {
			t.Fatalf("Start(directory) = %v, want ErrDescriptor", err)
		}
		if !strings.Contains(err.Error(), "never searches") {
			t.Fatalf("the refusal must say why a directory is not an input; got %v", err)
		}
	})
}

// AC-1 — ADR 0013 N2 ("graphi does not scan directories for extensions and does
// not run what it finds") is made STRUCTURAL rather than promised.
//
// A behavioural test can only prove that the discovery paths a test thought of
// are absent. This one proves the capability is absent: the package's own source
// contains no directory listing, no glob and no walk, so there is nothing to
// call. It is the same shape as cmd/graphi/binary_weight_test.go — assert the
// absence of a named, enumerable mechanism rather than the absence of a symptom.
func TestSW231_AC1_NoDirectoryDiscoveryMechanismExists(t *testing.T) {
	forbidden := map[string]string{
		"os.ReadDir":         "lists a directory",
		"ioutil.ReadDir":     "lists a directory",
		"filepath.Glob":      "expands a pattern into paths nobody named",
		"filepath.Walk":      "recurses a tree",
		"filepath.WalkDir":   "recurses a tree",
		"fs.WalkDir":         "recurses a tree",
		"(*os.File).Readdir": "lists a directory",
		"exec.LookPath":      "resolves a bare name against PATH, which is discovery by another name",
	}
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read package dir: %v", err)
	}
	for _, e := range entries {
		name := e.Name()
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		src, err := os.ReadFile(name)
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		for needle, why := range forbidden {
			if bytes.Contains(src, []byte(needle)) {
				t.Errorf("%s uses %s, which %s. ADR 0013 N2 closes discovery: an extension is "+
					"named by the user, one descriptor at a time", name, needle, why)
			}
		}
	}
}

// AC-4 — the extension reaches data EXCLUSIVELY through declared host ports, and
// never through the SQLite file.
//
// Two independent proofs, because "it did not open the database" is only
// convincing if the database was never reachable in the first place:
//
//  1. the port a request arrives on must be declared, or it is refused
//     (exercised in the attack suite);
//  2. no store path, database file name or directory ever crosses the wire —
//     asserted here over the ACTUAL bytes of every frame the host sends.
func TestSW231_AC4_NoStorePathEverCrossesTheWire(t *testing.T) {
	d := Descriptor{
		SchemaVersion: extpack.SchemaVersion,
		ID:            exampleID,
		Version:       exampleVersion,
		Kind:          KindProcessAnalyzer,
		API:           extpack.APIRange{Min: "1.0", Max: "1.0"},
		Artifact:      extpack.Artifact{Path: "example-analyzer", SHA256: strings.Repeat("ab", 32)},
		Capabilities:  extpack.Capabilities{Provides: []string{exampleOperation}},
		Ports:         []opcatalog.Port{opcatalog.PortGraphSearch},
		Permissions:   opcatalog.PermissionsFor([]opcatalog.Port{opcatalog.PortGraphSearch}),
		Determinism:   opcatalog.DeterminismDeterministic,
		Limits:        Limits{MaxResponseBytes: 64 << 10, TimeoutMS: 5_000},
	}
	var buf bytes.Buffer
	limits := d.Limits
	frames := []Frame{
		{Type: MsgHello, Protocol: ProtocolVersion, HostAPI: HostAPIVersion, ExtensionID: d.ID,
			ExtensionVersion: d.Version, ArtifactSHA256: d.Artifact.SHA256,
			Ports: []string{string(opcatalog.PortGraphSearch)}, Limits: &limits},
		{Type: MsgCall, ID: 1, Operation: exampleOperation, Arguments: json.RawMessage(`{"symbol":"Hel"}`)},
		{Type: MsgPortResult, ID: 1, OK: true, Payload: json.RawMessage(searchFixture)},
		{Type: MsgShutdown},
	}
	for _, f := range frames {
		if err := WriteFrame(&buf, f); err != nil {
			t.Fatalf("WriteFrame(%s): %v", f.Type, err)
		}
	}
	wire := buf.String()
	for _, forbidden := range []string{".db", ".sqlite", "graph.db", "meta/", ".graphi", "graph.store"} {
		if strings.Contains(wire, forbidden) {
			t.Errorf("the wire carries %q. ADR 0013 N4 holds by ABSENCE — an extension cannot open "+
				"a file whose path it was never told:\n%s", forbidden, wire)
		}
	}

	// And the descriptor schema cannot ask for one either: graph.store is on the
	// deny list even though its permission (graph.read) is inside the read-only
	// envelope.
	d.Ports = []opcatalog.Port{opcatalog.PortGraphStore}
	d.Permissions = opcatalog.PermissionsFor(d.Ports)
	if err := d.Validate(); !errors.Is(err, ErrDescriptor) || !strings.Contains(err.Error(), "N4") {
		t.Fatalf("declaring graph.store = %v, want an ErrDescriptor citing ADR 0013 N4", err)
	}
}

// AC-4 — every result an extension influences carries full provenance.
func TestSW231_AC4_ResultsCarryExtensionIdentityAndHash(t *testing.T) {
	descriptor := stageExtension(t, descriptorOptions{})
	loaded, err := LoadDescriptor(descriptor)
	if err != nil {
		t.Fatalf("LoadDescriptor: %v", err)
	}
	ext, _ := startExample(t, descriptor, "")
	res, err := ext.Call(callCtx(t), exampleOperation, json.RawMessage(`{"symbol":"Hel"}`))
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	p := res.Provenance
	switch {
	case p.ExtensionID != exampleID:
		t.Errorf("provenance.extension_id = %q, want %q", p.ExtensionID, exampleID)
	case p.ExtensionVersion != exampleVersion:
		t.Errorf("provenance.extension_version = %q, want %q", p.ExtensionVersion, exampleVersion)
	case p.ArtifactSHA256 != loaded.Descriptor.Artifact.SHA256:
		t.Errorf("provenance.artifact_sha256 = %q, want the pinned %q",
			p.ArtifactSHA256, loaded.Descriptor.Artifact.SHA256)
	case p.DescriptorSHA256 != loaded.DescriptorSHA256:
		t.Errorf("provenance.descriptor_sha256 = %q, want %q", p.DescriptorSHA256, loaded.DescriptorSHA256)
	case p.Protocol != ProtocolVersion:
		t.Errorf("provenance.protocol = %q, want %q", p.Protocol, ProtocolVersion)
	case p.HostAPI != HostAPIVersion:
		t.Errorf("provenance.host_api = %q, want %q", p.HostAPI, HostAPIVersion)
	case p.Operation != exampleOperation:
		t.Errorf("provenance.operation = %q, want %q", p.Operation, exampleOperation)
	case p.Confidence != ConfidenceDerived:
		t.Errorf("provenance.confidence = %q, want %q", p.Confidence, ConfidenceDerived)
	case p.Tier != "labs":
		t.Errorf("provenance.tier = %q; ADR 0013 I4 keeps tier C in Labs", p.Tier)
	}
	// ADR 0013 D3 obliges every surface offering a tier-C extension to say the
	// words. Carrying them in the record is how the obligation travels with the
	// data instead of relying on each consumer to remember it.
	if !strings.Contains(p.Trust, "not a sandbox") {
		t.Errorf("provenance.trust = %q, want the ADR 0013 D3 honesty statement", p.Trust)
	}
	canonical, err := res.CanonicalJSON()
	if err != nil {
		t.Fatalf("CanonicalJSON: %v", err)
	}
	if !bytes.Contains(canonical, []byte(`"artifact_sha256":"`+loaded.Descriptor.Artifact.SHA256+`"`)) {
		t.Fatalf("the canonical bytes must carry the hash at the point of consumption:\n%s", canonical)
	}
}

// AC-4 — the port wiring is checked in BOTH directions before anything is
// spawned. A missing handler is a host setup gap; a surplus handler is a grant
// nobody wrote down.
func TestSW231_AC4_PortWiringIsCheckedBothWays(t *testing.T) {
	descriptor := stageExtension(t, descriptorOptions{})

	t.Run("missing handler", func(t *testing.T) {
		_, err := Start(context.Background(), Config{
			Activated: true, DescriptorPath: descriptor,
			Ports: map[opcatalog.Port]PortHandler{},
		})
		if !errors.Is(err, registry.ErrMissingDependency) {
			t.Fatalf("Start with no handler = %v, want registry.ErrMissingDependency", err)
		}
	})
	t.Run("surplus handler", func(t *testing.T) {
		port := &searchPort{}
		_, err := Start(context.Background(), Config{
			Activated: true, DescriptorPath: descriptor,
			Ports: map[opcatalog.Port]PortHandler{
				opcatalog.PortGraphSearch: port.handle,
				opcatalog.PortSourceRead:  port.handle,
			},
		})
		if !errors.Is(err, registry.ErrUnsupportedOverride) {
			t.Fatalf("Start with a surplus handler = %v, want registry.ErrUnsupportedOverride", err)
		}
	})
}

// The frame codec's own contract: a round trip, and the ordering that makes the
// size limit a limit.
func TestSW231_FrameCodecRoundTripsAndBoundsBeforeReading(t *testing.T) {
	var buf bytes.Buffer
	in := Frame{Type: MsgCall, ID: 7, Operation: "x.y", Arguments: json.RawMessage(`{"a":1}`)}
	if err := WriteFrame(&buf, in); err != nil {
		t.Fatalf("WriteFrame: %v", err)
	}
	if !strings.HasPrefix(buf.String(), ProtocolVersion+" ") {
		t.Fatalf("frame does not start with the protocol token: %q", buf.String())
	}
	out, err := ReadFrame(bufio.NewReader(&buf), 1<<20)
	if err != nil {
		t.Fatalf("ReadFrame: %v", err)
	}
	if out.Type != in.Type || out.ID != in.ID || out.Operation != in.Operation ||
		string(out.Arguments) != string(in.Arguments) {
		t.Fatalf("round trip = %+v, want %+v", out, in)
	}
}
