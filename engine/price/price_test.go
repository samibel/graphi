package price

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// helper: build an in-memory table without touching disk.
func mkTable(t *testing.T, version string, models map[string]Rate) *PriceTable {
	t.Helper()
	pt := &PriceTable{Version: version, Models: models}
	if err := pt.validate(); err != nil {
		t.Fatal(err)
	}
	return pt
}

// AC: deterministic USD = delta x local rate, no inflationary rounding.
func TestSavings_ExactIntegerMultiply(t *testing.T) {
	pt := mkTable(t, "v1", map[string]Rate{"gpt-4o": {InputPerTokenMicroUSD: 2500}})
	// 1000 tokens * 2500 micro/token = 2_500_000 micro == $2.50
	r := Savings(pt, "gpt-4o", 1000)
	if !r.Priced || r.MicroUSD != 2_500_000 {
		t.Errorf("want priced 2500000, got %+v", r)
	}
	if r.TableVersion != "v1" {
		t.Errorf("version not stamped: %s", r.TableVersion)
	}
}

// AC: same (model, delta, version) -> byte-identical figure (reproducibility).
func TestSavings_Reproducible(t *testing.T) {
	pt := mkTable(t, "v1", map[string]Rate{"m": {InputPerTokenMicroUSD: 1234}})
	r1 := Savings(pt, "m", 4242)
	r2 := Savings(pt, "m", 4242)
	b1, _ := json.Marshal(r1)
	b2, _ := json.Marshal(r2)
	if string(b1) != string(b2) {
		t.Errorf("not byte-identical: %s vs %s", b1, b2)
	}
}

// AC: unknown model -> flagged unpriced, zero USD, no error, no fabrication.
func TestSavings_UnknownModel_HonestUnpriced(t *testing.T) {
	pt := mkTable(t, "v1", map[string]Rate{"m": {InputPerTokenMicroUSD: 1000}})
	r := Savings(pt, "does-not-exist", 9999)
	if r.Priced {
		t.Errorf("unknown model must be unpriced, got %+v", r)
	}
	if r.MicroUSD != 0 {
		t.Errorf("unpriced must be zero USD, got %d", r.MicroUSD)
	}
	if r.TableVersion != "v1" {
		t.Errorf("unpriced result still stamped with table version: %s", r.TableVersion)
	}
}

// AC: negative delta -> negative USD (honest overrun), not clamped.
func TestSavings_NegativeDelta(t *testing.T) {
	pt := mkTable(t, "v1", map[string]Rate{"m": {InputPerTokenMicroUSD: 1000}})
	r := Savings(pt, "m", -500)
	if !r.Priced || r.MicroUSD != -500_000 {
		t.Errorf("negative delta: want -500000, got %+v", r)
	}
}

// AC: malformed table fails fast (missing version / empty / negative rate / bad JSON).
func TestLoad_FailFast(t *testing.T) {
	cases := []struct {
		name string
		json string
	}{
		{"missing version", `{"models":{"m":{"input_per_token_microusd":1,"output_per_token_microusd":1}}}`},
		{"empty models", `{"version":"v1","models":{}}`},
		{"negative rate", `{"version":"v1","models":{"m":{"input_per_token_microusd":-5,"output_per_token_microusd":1}}}`},
		{"bad json", `{not json`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := LoadFromBytes([]byte(tc.json))
			if err == nil {
				t.Errorf("malformed table (%s) should fail-fast", tc.name)
			}
			if !errors.Is(err, ErrMalformedTable) && tc.name != "bad json" {
				t.Errorf("expected ErrMalformedTable, got %v", err)
			}
		})
	}
}

// AC: embedded Load() returns a valid, version-stamped table (sole source).
func TestLoad_EmbeddedIsValid(t *testing.T) {
	pt, err := Load()
	if err != nil {
		t.Fatalf("embedded load failed: %v", err)
	}
	if pt.Version == "" || len(pt.Models) == 0 {
		t.Errorf("embedded table invalid: %+v", pt)
	}
	// A known model prices; an unknown model is unpriced.
	if r := Savings(pt, "gpt-4o-mini", 1000); !r.Priced {
		t.Errorf("gpt-4o-mini should price from embedded table")
	}
	if r := Savings(pt, "nope", 1000); r.Priced {
		t.Errorf("unknown model should be unpriced")
	}
}

// AC: LoadFromPath rejects remote sources (local-only).
func TestLoadFromPath_RejectsRemote(t *testing.T) {
	for _, remote := range []string{"http://e.com/p.json", "https://e.com/p.json"} {
		if _, err := LoadFromPath(remote); err == nil {
			t.Errorf("remote %q should be rejected", remote)
		}
	}
	// A real local file works.
	dir := t.TempDir()
	path := filepath.Join(dir, "p.json")
	src := `{"version":"local","models":{"m":{"input_per_token_microusd":7,"output_per_token_microusd":7}}}`
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	pt, err := LoadFromPath(path)
	if err != nil {
		t.Fatalf("local load failed: %v", err)
	}
	if r := Savings(pt, "m", 3); r.MicroUSD != 21 {
		t.Errorf("local table computation: want 21 got %d", r.MicroUSD)
	}
}

// AC: per-session accumulation is a deterministic integer sum.
func TestSession_PerSessionTotal(t *testing.T) {
	pt := mkTable(t, "v1", map[string]Rate{"m": {InputPerTokenMicroUSD: 1000}})
	s := NewSession()
	s.Add(Savings(pt, "m", 100))     // 100_000
	s.Add(Savings(pt, "m", 50))      // 50_000
	s.Add(Savings(pt, "nope", 9999)) // unpriced -> contributes 0
	s.Add(Savings(pt, "m", -10))     // -10_000
	if tot := s.Total(); tot != 140_000 {
		t.Errorf("session total: want 140000 got %d", tot)
	}
	if m := s.TotalForModel("m"); m != 140_000 {
		t.Errorf("model total: want 140000 got %d", m)
	}
}

// AC: no-network — the package performs zero outbound I/O. Static guard.
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
		for _, line := range strings.Split(string(data), "\n") {
			trim := strings.TrimSpace(line)
			if strings.Contains(trim, "\"net\"") && !strings.HasPrefix(trim, "//") {
				t.Errorf("%s: forbidden net import: %s", f, trim)
			}
		}
	}
}

// Sanity: FormatUSD never inflates (round-down toward zero).
func TestFormatUSD_NoInflation(t *testing.T) {
	// 2_500_000 micro == $2.50 exactly
	if got := FormatUSD(2_500_000); got != "$2.50" {
		t.Errorf("FormatUSD(2500000) = %q, want $2.50", got)
	}
	// 2_500_099 micro truncates to $2.50 (never $2.51)
	if got := FormatUSD(2_500_099); got != "$2.50" {
		t.Errorf("FormatUSD rounds up: %q", got)
	}
	// Negative preserved.
	if got := FormatUSD(-500_000); got != "-$0.50" {
		t.Errorf("FormatUSD negative: %q", got)
	}
}
