package exthost

// Attack-shaped tests: every one tries to make the host accept something it
// promised to refuse. They follow the P1 convention (*_attack_test.go) because
// they serve the same purpose — a claim like "a mismatch aborts with no
// extension output entering any result" needs a test that TRIES to get output in.
//
// Two properties are asserted everywhere, and the second is the one that is easy
// to forget: the call fails, AND no result is returned. A host that returned a
// value alongside its error would have laundered exactly what it refused.

import (
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

// AC-2 — the manifest's SHA-256 is verified BEFORE anything runs.
//
// The strong form of the claim, and the one the test asserts: the altered binary
// is never EXECUTED. If verification happened after spawn, the analyzer's own
// startup would have run first — so the test corrupts a binary that would
// otherwise answer, and requires the failure to name hash verification.
func TestSW231Attack_AC2_AlteredArtifactIsNeverExecuted(t *testing.T) {
	descriptor := stageExtension(t, descriptorOptions{CorruptBinary: true})
	ext, _, err := tryStart(t, descriptor, "")
	if ext != nil {
		t.Fatal("a corrupted artifact must not yield a running extension")
	}
	if !errors.Is(err, ErrArtifactMismatch) {
		t.Fatalf("Start with a corrupted artifact = %v, want ErrArtifactMismatch", err)
	}
	for _, want := range []string{"sha256 mismatch", "nothing was executed"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the error must contain %q so the user can tell a wrong --sha256 from a wrong "+
				"file; got: %v", want, err)
		}
	}
}

// AC-2 — the handshake negotiates protocol and API version, and every
// disagreement aborts before any request is written.
//
// Table-driven over the four ways the far side can be the wrong far side. Each
// row also asserts the SENTINEL, not just failure: "the handshake failed" is not
// a diagnosis, and a user staring at a version problem needs to be told it is a
// version problem.
func TestSW231Attack_AC2_HandshakeRefusesEveryDisagreement(t *testing.T) {
	for _, tc := range []struct {
		name   string
		fault  string
		want   error
		detail string
	}{
		{"protocol version mismatch", "bad-protocol", ErrProtocolMismatch, "graphi-ext/99"},
		{"api version outside the declared range", "bad-api", ErrAPIVersionUnsupported, "9.0"},
		{"identity does not match the pinned descriptor", "bad-identity", ErrIdentityMismatch, "someone-else"},
		{"advertises an operation the descriptor never declared", "extra-operation", ErrProtocolViolation, "example_undeclared_extra"},
		{"exits before saying hello", "silent-exit", ErrCrashed, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ext, _, err := tryStart(t, stageExtension(t, descriptorOptions{}), tc.fault)
			if ext != nil {
				t.Fatal("a failed handshake must not yield a running extension")
			}
			if !errors.Is(err, tc.want) {
				t.Fatalf("Start = %v, want %v", err, tc.want)
			}
			if tc.detail != "" && !strings.Contains(err.Error(), tc.detail) {
				t.Errorf("the error must quote the offending value %q; got: %v", tc.detail, err)
			}
		})
	}
}

// AC-2 — a superseded schema spelling FAILS. It is not read best-effort and it
// is not accepted alongside the current one.
//
// This is the standards rule ("superseded contract spellings get rejected, not
// aliased") applied to the descriptor, and it is the reason the schema check is
// an exact string comparison rather than a prefix or a range.
func TestSW231Attack_AC2_DescriptorSchemaAndKindAreExactMatches(t *testing.T) {
	for _, tc := range []struct {
		name    string
		mutate  func(*Descriptor)
		wantSub string
	}{
		{"an older schema version", func(d *Descriptor) { d.SchemaVersion = "graphi.extension/v1" }, "schema_version"},
		{"no schema version at all", func(d *Descriptor) { d.SchemaVersion = "" }, "schema_version"},
		{"a tier-A pack kind", func(d *Descriptor) { d.Kind = string(extpack.KindTaintRules) }, "kind"},
		{"an unknown kind", func(d *Descriptor) { d.Kind = "wasm-module" }, "kind"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, _, err := tryStart(t, stageExtension(t, descriptorOptions{Mutate: tc.mutate}), "")
			if !errors.Is(err, ErrDescriptor) {
				t.Fatalf("Start = %v, want ErrDescriptor", err)
			}
			if !strings.Contains(err.Error(), tc.wantSub) {
				t.Errorf("the error must name the offending field %q; got: %v", tc.wantSub, err)
			}
		})
	}
}

// AC-2/AC-4 — the descriptor cannot describe an extension outside the ADR 0013
// V1 envelope, and cannot raise its own limits.
func TestSW231Attack_DescriptorCannotEscapeItsEnvelope(t *testing.T) {
	base := func() Descriptor {
		return Descriptor{
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
	}
	for _, tc := range []struct {
		name    string
		mutate  func(*Descriptor)
		wantSub string
	}{
		{"a write port", func(d *Descriptor) {
			d.Ports = []opcatalog.Port{opcatalog.PortSourceWrite}
			d.Permissions = opcatalog.PermissionsFor(d.Ports)
		}, "I3"},
		{"an egress port", func(d *Descriptor) {
			d.Ports = []opcatalog.Port{opcatalog.PortForgeEnumerate}
			d.Permissions = opcatalog.PermissionsFor(d.Ports)
		}, "I3"},
		{"a direct store open", func(d *Descriptor) {
			d.Ports = []opcatalog.Port{opcatalog.PortGraphStore}
			d.Permissions = opcatalog.PermissionsFor(d.Ports)
		}, "N4"},
		{"the ports_unaudited marker", func(d *Descriptor) {
			d.Ports = []opcatalog.Port{opcatalog.PortsUnaudited}
			d.Permissions = opcatalog.PermissionsFor(d.Ports)
		}, "not a host port"},
		{"permissions that contradict the ports", func(d *Descriptor) {
			d.Permissions = []opcatalog.Permission{}
		}, "do not match"},
		{"no ports at all", func(d *Descriptor) {
			d.Ports = nil
			d.Permissions = nil
		}, "ports is empty"},
		{"a response limit above the ceiling", func(d *Descriptor) {
			d.Limits.MaxResponseBytes = MaxResponseCeiling + 1
		}, "cannot raise its own ceiling"},
		{"an unbounded timeout", func(d *Descriptor) { d.Limits.TimeoutMS = 0 }, "no unbounded budget"},
		{"a timeout above the ceiling", func(d *Descriptor) { d.Limits.TimeoutMS = MaxTimeoutMS + 1 }, "no unbounded budget"},
		{"a self-declared non-deterministic analyzer", func(d *Descriptor) {
			d.Determinism = opcatalog.DeterminismEnvironmentDependent
		}, "determinism"},
		{"no advertised operation", func(d *Descriptor) { d.Capabilities.Provides = nil }, "capabilities.provides"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := base()
			tc.mutate(&d)
			err := d.Validate()
			if err == nil {
				t.Fatalf("Validate accepted %s", tc.name)
			}
			if !strings.Contains(err.Error(), tc.wantSub) {
				t.Errorf("the refusal must say %q; got: %v", tc.wantSub, err)
			}
		})
	}
}

// AC-2 — an unknown field is REJECTED, not dropped. A descriptor field this
// build does not understand may be the one carrying the meaning.
func TestSW231Attack_UnknownDescriptorFieldIsRejected(t *testing.T) {
	_, err := ParseDescriptor([]byte("schema_version: \"x\"\nsandboxed: true\n"))
	if !errors.Is(err, ErrDescriptor) {
		t.Fatalf("ParseDescriptor = %v, want ErrDescriptor", err)
	}
	if !strings.Contains(err.Error(), "sandboxed") {
		t.Fatalf("the error must name the unknown field; got: %v", err)
	}
}

// AC-3 — the artifact path is a bare name resolved beside the descriptor, and
// nothing else. The value decides what gets EXECUTED, so every escape shape is
// pinned.
func TestSW231Attack_ArtifactPathCannotEscapeTheDescriptorDirectory(t *testing.T) {
	for _, name := range []string{
		"../graphi", "/bin/sh", "sub/analyzer", `..\graphi`, "", ".", "..",
		"file:///bin/sh", "C:analyzer",
	} {
		t.Run(name, func(t *testing.T) {
			d := Descriptor{Artifact: extpack.Artifact{Path: name}}
			err := validateArtifactName(d.Artifact.Path)
			if !errors.Is(err, ErrUnsafeBinary) {
				t.Fatalf("artifact.path %q = %v, want ErrUnsafeBinary", name, err)
			}
		})
	}
}

// AC-3 — the host refuses to execute a Go test binary or itself.
//
// This is the 2026-08-27 lesson (commit 4328a5c), applied before this package
// could repeat it: surfaces/daemon defaulted its binary to os.Args[0], which
// under `go test` is the test binary, and spawning it re-ran the suite — 374
// live processes and four kernel panics. Nothing here DEFAULTS a binary at all,
// and the two shapes that would recreate the explosion are refused explicitly.
func TestSW231Attack_RefusesToSpawnATestBinaryOrItself(t *testing.T) {
	t.Run("a .test binary", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "exthost.test")
		if err := os.WriteFile(path, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
			t.Fatalf("stage: %v", err)
		}
		err := refuseUnsafeBinary(path)
		if !errors.Is(err, ErrUnsafeBinary) {
			t.Fatalf("refuseUnsafeBinary(%q) = %v, want ErrUnsafeBinary", path, err)
		}
		if !strings.Contains(err.Error(), "4328a5c") {
			t.Errorf("the refusal should cite the incident it exists for; got: %v", err)
		}
	})
	t.Run("this very test binary, by its real path", func(t *testing.T) {
		self, err := os.Executable()
		if err != nil {
			t.Skipf("os.Executable unavailable: %v", err)
		}
		if err := refuseUnsafeBinary(self); !errors.Is(err, ErrUnsafeBinary) {
			t.Fatalf("refuseUnsafeBinary(os.Executable()) = %v, want ErrUnsafeBinary", err)
		}
	})
	t.Run("a descriptor pointing at the test binary", func(t *testing.T) {
		self, err := os.Executable()
		if err != nil {
			t.Skipf("os.Executable unavailable: %v", err)
		}
		payload, err := os.ReadFile(self)
		if err != nil {
			t.Skipf("cannot read the test binary: %v", err)
		}
		dir := t.TempDir()
		name := "runner.test"
		if err := os.WriteFile(filepath.Join(dir, name), payload, 0o755); err != nil {
			t.Fatalf("stage: %v", err)
		}
		d := Descriptor{
			SchemaVersion: extpack.SchemaVersion, ID: exampleID, Version: exampleVersion,
			Kind: KindProcessAnalyzer, API: extpack.APIRange{Min: "1.0", Max: "1.0"},
			Artifact:     extpack.Artifact{Path: name, SHA256: extpack.HashBytes(payload)},
			Capabilities: extpack.Capabilities{Provides: []string{exampleOperation}},
			Ports:        []opcatalog.Port{opcatalog.PortGraphSearch},
			Permissions:  opcatalog.PermissionsFor([]opcatalog.Port{opcatalog.PortGraphSearch}),
			Determinism:  opcatalog.DeterminismDeterministic,
			Limits:       Limits{MaxResponseBytes: 64 << 10, TimeoutMS: 5_000},
		}
		path := filepath.Join(dir, "extension.yaml")
		writeDescriptor(t, path, d)
		port := &searchPort{}
		ext, err := Start(context.Background(), Config{
			Activated: true, DescriptorPath: path,
			Ports: map[opcatalog.Port]PortHandler{opcatalog.PortGraphSearch: port.handle},
		})
		if ext != nil {
			_ = ext.Close()
			t.Fatal("the host started a test binary as an extension — this is the process-explosion shape")
		}
		if !errors.Is(err, ErrUnsafeBinary) {
			t.Fatalf("Start = %v, want ErrUnsafeBinary", err)
		}
	})
}

// AC-4 — an extension that reaches for an undeclared port is refused with the
// SW-222 sentinel, the refusal is RECORDED, and the call fails closed.
//
// The recording matters independently of the error: engine/extpack/conformance's
// port gate records a request before answering it precisely so a handler that
// swallows the refusal is still caught. The same reasoning applies across a
// process boundary, where "swallowing it" is even easier.
func TestSW231Attack_AC4_UndeclaredPortIsRefusedRecordedAndFatal(t *testing.T) {
	ext, port := startExample(t, stageExtension(t, descriptorOptions{}), "undeclared-port")

	res, err := ext.Call(callCtx(t), exampleOperation, json.RawMessage(`{"symbol":"Hel"}`))
	if !errors.Is(err, registry.ErrMissingDependency) {
		t.Fatalf("Call = %v, want registry.ErrMissingDependency", err)
	}
	if len(res.Findings) != 0 || res.Provenance.ExtensionID != "" {
		t.Fatalf("a refused port must yield NO result; got %+v", res)
	}
	if !strings.Contains(err.Error(), "source.read") {
		t.Errorf("the refusal must name the port that was reached for; got: %v", err)
	}
	if got := ext.PortViolations(); len(got) != 1 || got[0] != "source.read" {
		t.Fatalf("PortViolations() = %v, want [source.read] recorded regardless of error handling", got)
	}
	if seen := port.seen(); len(seen) != 0 {
		t.Fatalf("the declared port must not have been touched on this path; saw %v", seen)
	}
}

// AC-4 — ADR 0013 D5: `confirmed` is closed to extensions, and the host REJECTS
// rather than downgrades.
//
// Downgrading would be the friendly behaviour and the wrong one — it produces a
// result whose tier nobody chose, and teaches an author that the ceiling is
// advisory. The test therefore asserts both halves: an error, and no result.
func TestSW231Attack_AC4_ExtensionCannotMintConfirmedConfidence(t *testing.T) {
	ext, _ := startExample(t, stageExtension(t, descriptorOptions{}), "confirmed")

	res, err := ext.Call(callCtx(t), exampleOperation, json.RawMessage(`{"symbol":"Hel"}`))
	if !errors.Is(err, ErrConfidenceLaundering) {
		t.Fatalf("Call = %v, want ErrConfidenceLaundering", err)
	}
	if len(res.Findings) != 0 {
		t.Fatalf("a laundered result must be REJECTED, not downgraded and returned; got %s", res.Findings)
	}
	if res.Provenance.Confidence != "" {
		t.Fatalf("provenance.confidence = %q; nothing may be minted from a rejected result",
			res.Provenance.Confidence)
	}
	if !strings.Contains(err.Error(), "not downgraded") {
		t.Errorf("the error should say the result was rejected rather than repaired; got: %v", err)
	}
}

// AC-3 — the maximum response size is enforced on the DECLARED length, before
// the body is read.
//
// A limit checked after buffering is not a limit; it is a post-mortem. The test
// hands the reader a header claiming more than the limit with NO body behind it:
// if the check happened after reading, this would block or fail as a truncated
// stream instead of as an oversize refusal.
func TestSW231Attack_AC3_OversizeIsRefusedBeforeTheBodyIsRead(t *testing.T) {
	stream := newBufReader(ProtocolVersion + " 999999\n")
	_, err := ReadFrame(stream, 1024)
	if !errors.Is(err, ErrResponseTooLarge) {
		t.Fatalf("ReadFrame = %v, want ErrResponseTooLarge", err)
	}
	if !strings.Contains(err.Error(), "999999") || !strings.Contains(err.Error(), "1024") {
		t.Errorf("the error must quote both the declared size and the limit; got: %v", err)
	}
}

// AC-2/AC-3 — a stream that is not this protocol is refused at the first frame,
// and an over-long header cannot make the reader run away.
func TestSW231Attack_ForeignStreamsAreRefusedAtTheFirstFrame(t *testing.T) {
	t.Run("another protocol", func(t *testing.T) {
		_, err := ReadFrame(newBufReader("graphi-ext/0 5\nhello"), 1024)
		if !errors.Is(err, ErrProtocolMismatch) {
			t.Fatalf("ReadFrame = %v, want ErrProtocolMismatch", err)
		}
	})
	t.Run("not a framed stream at all", func(t *testing.T) {
		_, err := ReadFrame(newBufReader("{\"jsonrpc\":\"2.0\"}\n"), 1024)
		if !errors.Is(err, ErrProtocolViolation) && !errors.Is(err, ErrProtocolMismatch) {
			t.Fatalf("ReadFrame = %v, want a protocol refusal", err)
		}
	})
	t.Run("an unbounded header", func(t *testing.T) {
		_, err := ReadFrame(newBufReader(strings.Repeat("x", 4096)), 1024)
		if !errors.Is(err, ErrProtocolViolation) {
			t.Fatalf("ReadFrame = %v, want ErrProtocolViolation", err)
		}
		if !strings.Contains(err.Error(), "without a newline") {
			t.Errorf("the error should say the header never terminated; got: %v", err)
		}
	})
	t.Run("a negative length", func(t *testing.T) {
		_, err := ReadFrame(newBufReader(ProtocolVersion+" -1\n"), 1024)
		if !errors.Is(err, ErrProtocolViolation) {
			t.Fatalf("ReadFrame = %v, want ErrProtocolViolation", err)
		}
	})
	t.Run("a lying header, shorter body", func(t *testing.T) {
		_, err := ReadFrame(newBufReader(ProtocolVersion+" 100\n{}"), 1024)
		if !errors.Is(err, ErrCrashed) {
			t.Fatalf("ReadFrame = %v, want ErrCrashed (the stream ended early)", err)
		}
	})
	t.Run("an unknown frame field", func(t *testing.T) {
		body := `{"t":"result","sandboxed":true}`
		_, err := ReadFrame(newBufReader(ProtocolVersion+" "+itoa(len(body))+"\n"+body), 1024)
		if !errors.Is(err, ErrProtocolViolation) {
			t.Fatalf("ReadFrame = %v, want ErrProtocolViolation", err)
		}
	})
	t.Run("a typeless frame", func(t *testing.T) {
		body := `{"id":1}`
		_, err := ReadFrame(newBufReader(ProtocolVersion+" "+itoa(len(body))+"\n"+body), 1024)
		if !errors.Is(err, ErrProtocolViolation) {
			t.Fatalf("ReadFrame = %v, want ErrProtocolViolation", err)
		}
	})
}

// AC-2 — calling an operation the extension does not advertise is refused
// locally, without a round trip.
func TestSW231Attack_UnadvertisedOperationIsRefused(t *testing.T) {
	ext, _ := startExample(t, stageExtension(t, descriptorOptions{}), "")
	_, err := ext.Call(callCtx(t), "example_something_else", json.RawMessage(`{}`))
	if !errors.Is(err, registry.ErrMissingDependency) {
		t.Fatalf("Call = %v, want registry.ErrMissingDependency", err)
	}
	if !strings.Contains(err.Error(), exampleOperation) {
		t.Errorf("the refusal should list what IS offered; got: %v", err)
	}
}
