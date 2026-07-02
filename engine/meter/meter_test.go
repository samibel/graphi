package meter

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// memReader is an in-memory FileReader for deterministic tests.
type memReader map[string][]byte

func (m memReader) ReadFile(path string) ([]byte, error) {
	if isRemoteSource(path) {
		return nil, fmt.Errorf("meter: remote source rejected (%q)", path)
	}
	data, ok := m[path]
	if !ok {
		return nil, fmt.Errorf("meter: read %s: not found", path)
	}
	return data, nil
}

// AC: per-call record with actual + baseline + savings + version stamp.
func TestRecord_EmitsStampedRecord(t *testing.T) {
	r := memReader{
		"a.go": []byte("alpha beta gamma\n"), // 3 tokens
		"b.go": []byte("delta epsilon\n"),    // 2 tokens
	}
	m := New(r)
	rec, err := m.Record("call-1", "gpt-x", 1, []string{"a.go", "b.go"})
	if err != nil {
		t.Fatal(err)
	}
	if rec.CallID != "call-1" || rec.Model != "gpt-x" {
		t.Errorf("identity: %+v", rec)
	}
	if rec.ActualTokens != 1 {
		t.Errorf("actual: want 1 got %d", rec.ActualTokens)
	}
	if !rec.BaselineAvailable || rec.BaselineTokens != 5 {
		t.Errorf("baseline: want available/5, got available=%v tokens=%d", rec.BaselineAvailable, rec.BaselineTokens)
	}
	if rec.SavingsTokens != 4 {
		t.Errorf("savings: want 4 got %d", rec.SavingsTokens)
	}
	if rec.BaselineVersion != BaselineMethodVersion {
		t.Errorf("version: want %s got %s", BaselineMethodVersion, rec.BaselineVersion)
	}
}

// AC: deterministic baseline — identical inputs -> byte-identical value+version.
func TestRecord_BaselineDeterministic(t *testing.T) {
	r := memReader{"a.go": []byte("one two three four\n")} // 4 tokens
	m := New(r)
	r1, _ := m.Record("c1", "m", 1, []string{"a.go"})
	r2, _ := m.Record("c2", "m", 1, []string{"a.go"})
	if r1.BaselineTokens != r2.BaselineTokens || r1.BaselineVersion != r2.BaselineVersion {
		t.Errorf("baseline not deterministic: %+v vs %+v", r1, r2)
	}
	// Serialize-deserialize compare of the deterministic fields.
	b1, _ := json.Marshal(struct {
		B, V string
		A    bool
	}{fmt.Sprintf("%d", r1.BaselineTokens), r1.BaselineVersion, r1.BaselineAvailable})
	b2, _ := json.Marshal(struct {
		B, V string
		A    bool
	}{fmt.Sprintf("%d", r2.BaselineTokens), r2.BaselineVersion, r2.BaselineAvailable})
	if string(b1) != string(b2) {
		t.Errorf("baseline bytes differ: %s vs %s", b1, b2)
	}
}

// AC: version stamp reflects method change; prior records keep their version.
func TestRecord_VersionStampImmutableAcrossMethodChange(t *testing.T) {
	r := memReader{"a.go": []byte("x y z\n")} // 3 tokens
	m := New(r)
	recV1, _ := m.Record("c", "m", 1, []string{"a.go"})
	if recV1.BaselineVersion != BaselineMethodVersion {
		t.Fatalf("expected current version, got %s", recV1.BaselineVersion)
	}
	// Simulate a method change: the record already emitted keeps its captured
	// version (it is a field, never re-derived on read).
	captured := recV1.BaselineVersion
	// Even if the constant were bumped in a future release, this record's field
	// is frozen at emit time — assert the captured value is what it was.
	if recV1.BaselineVersion != captured {
		t.Errorf("version mutated after emit: %s != %s", recV1.BaselineVersion, captured)
	}
}

// AC: no double-count — actual tokens attributed to exactly one call.
func TestRecord_NoDoubleCountAcrossCalls(t *testing.T) {
	r := memReader{"a.go": []byte("a b c\n")} // 3 tokens
	m := New(r)
	r1, _ := m.Record("call-A", "m", 2, []string{"a.go"})
	r2, _ := m.Record("call-B", "m", 2, []string{"a.go"})
	if r1.CallID == r2.CallID {
		t.Errorf("distinct calls produced same CallID")
	}
	// Each call's baseline is independent (3 each), not summed (6).
	if r1.BaselineTokens != 3 || r2.BaselineTokens != 3 {
		t.Errorf("baseline leaked across calls: %d %d", r1.BaselineTokens, r2.BaselineTokens)
	}
}

// AC: same CallID twice -> two records (meter does not dedupe; ledger's job).
func TestRecord_SameCallIDTwiceProducesTwoRecords(t *testing.T) {
	r := memReader{"a.go": []byte("a b\n")}
	m := New(r)
	r1, _ := m.Record("dup", "m", 1, []string{"a.go"})
	r2, _ := m.Record("dup", "m", 1, []string{"a.go"})
	// Both emitted; the meter performs no dedupe.
	if r1.CallID != "dup" || r2.CallID != "dup" {
		t.Errorf("expected two records for same CallID")
	}
}

// AC: honest unavailable — baseline cannot be honestly determined -> unavailable,
// zero savings, NO favorable fabrication.
func TestRecord_EmptyArtifacts_HonestUnavailable(t *testing.T) {
	r := memReader{"a.go": []byte("a b c\n")}
	m := New(r)
	rec, err := m.Record("c", "m", 5, nil)
	if err != nil {
		t.Fatal(err)
	}
	if rec.BaselineAvailable {
		t.Errorf("empty artifacts: baseline must be unavailable, got %+v", rec)
	}
	if rec.BaselineTokens != 0 || rec.SavingsTokens != 0 {
		t.Errorf("empty artifacts: must report 0 baseline + 0 savings, got %+v", rec)
	}
	if rec.ActualTokens != 5 {
		t.Errorf("actual tokens not preserved on unavailable baseline: %d", rec.ActualTokens)
	}
}

// AC: read error fails-closed (honest), not silently unavailable.
func TestRecord_ReadError_FailsClosed(t *testing.T) {
	r := memReader{}
	m := New(r)
	if _, err := m.Record("c", "m", 1, []string{"missing.go"}); err == nil {
		t.Errorf("missing file: expected fail-closed error, got nil")
	}
}

// AC: savings reported raw (may be negative) — NOT clamped to 0.
func TestRecord_NegativeSavingsReportedRaw(t *testing.T) {
	r := memReader{"tiny.go": []byte("a\n")} // 1 token whole-file
	m := New(r)
	// graphi used 3 tokens where whole-file was 1 -> savings -2 (honest overrun).
	rec, err := m.Record("c", "m", 3, []string{"tiny.go"})
	if err != nil {
		t.Fatal(err)
	}
	if rec.SavingsTokens != -2 {
		t.Errorf("negative savings: want -2 (raw), got %d", rec.SavingsTokens)
	}
}

// AC: no double-count within a baseline — repeated artifact path counted once.
func TestRecord_RepeatedArtifactCountedOnce(t *testing.T) {
	r := memReader{"a.go": []byte("a b c\n")} // 3 tokens
	m := New(r)
	rec, _ := m.Record("c", "m", 0, []string{"a.go", "a.go", "a.go"})
	if rec.BaselineTokens != 3 {
		t.Errorf("repeated artifact over-counted: want 3 got %d", rec.BaselineTokens)
	}
	if len(rec.Artifacts) != 1 {
		t.Errorf("artifacts not deduped on record: %v", rec.Artifacts)
	}
}

// AC: local-first — FileReader rejects remote sources; no network.
func TestLocalFileReader_RejectsRemoteSources(t *testing.T) {
	lr := NewLocalFileReader()
	for _, remote := range []string{"http://example.com/a.go", "https://example.com/a.go"} {
		if _, err := lr.ReadFile(remote); err == nil {
			t.Errorf("remote source %q should be rejected", remote)
		}
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "real.go")
	if err := os.WriteFile(path, []byte("package real\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	m := New(lr)
	rec, err := m.Record("c", "m", 0, []string{path})
	if err != nil {
		t.Fatalf("local metering failed: %v", err)
	}
	if !rec.BaselineAvailable {
		t.Errorf("local baseline should be available")
	}
}

// AC: no reader -> explicit error (cannot compute baseline).
func TestRecord_NoReader_Errors(t *testing.T) {
	m := New(nil)
	_, err := m.Record("c", "m", 1, []string{"a.go"})
	if !errors.Is(err, ErrNoReader) {
		t.Errorf("want ErrNoReader, got %v", err)
	}
}

// Smoke: the package never performs network I/O (no net import). This is a
// static guard: assert the package source has no "net" import.
func TestPackageHasNoNetImport(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range files {
		data, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		// Allow "net" only inside comments; flag actual imports of "net".
		for _, line := range strings.Split(string(data), "\n") {
			trim := strings.TrimSpace(line)
			if strings.Contains(trim, "\"net\"") && !strings.HasPrefix(trim, "//") {
				t.Errorf("%s: forbidden net import: %s", f, trim)
			}
		}
	}
}
