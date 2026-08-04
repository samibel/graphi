package trust_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/samibel/graphi/core/graphstore"
	"github.com/samibel/graphi/core/model"
	"github.com/samibel/graphi/engine/trust"
)

// seedScopeStore builds a MemStore with one uniquely-named symbol and seven
// nodes sharing the qualified name "pkg.Dup" (distinct source paths keep their
// NodeIds distinct), so the ambiguous branch has more candidates than the
// bounded list shows.
func seedScopeStore(t *testing.T) (*graphstore.MemStore, model.Node) {
	t.Helper()
	ctx := context.Background()
	store := graphstore.NewMemStore()
	put := func(kind, qn, path string) model.Node {
		n, err := model.NewNode(kind, qn, path, 1, 1)
		if err != nil {
			t.Fatalf("NewNode(%s): %v", qn, err)
		}
		if err := store.PutNode(ctx, n); err != nil {
			t.Fatalf("PutNode(%s): %v", qn, err)
		}
		return n
	}
	alpha := put("function", "pkg.Alpha", "a/alpha.go")
	for i := 0; i < 7; i++ {
		put("function", "pkg.Dup", fmt.Sprintf("d%02d/dup.go", i))
	}
	return store, alpha
}

func findingCodes(fs []trust.Finding) []string {
	codes := make([]string, 0, len(fs))
	for _, f := range fs {
		codes = append(codes, f.Code)
	}
	return codes
}

func TestResolveScope(t *testing.T) {
	ctx := context.Background()
	store, alpha := seedScopeStore(t)

	cases := []struct {
		name      string
		raw       string
		wantScope trust.ScopeRef
		wantCodes []string
	}{
		{
			name:      "empty raw is repository scope",
			raw:       "",
			wantScope: trust.ScopeRef{Kind: trust.ScopeRepository},
			wantCodes: []string{},
		},
		{
			name:      "whitespace raw is repository scope",
			raw:       "   ",
			wantScope: trust.ScopeRef{Kind: trust.ScopeRepository},
			wantCodes: []string{},
		},
		{
			name: "unique qualified name is symbol scope",
			raw:  "pkg.Alpha",
			wantScope: trust.ScopeRef{
				Kind: trust.ScopeSymbol, ID: string(alpha.ID()),
				Path: "a/alpha.go", Symbol: "pkg.Alpha",
			},
			wantCodes: []string{},
		},
		{
			name:      "ambiguous qualified name keeps symbol kind with empty id",
			raw:       "pkg.Dup",
			wantScope: trust.ScopeRef{Kind: trust.ScopeSymbol, Symbol: "pkg.Dup"},
			wantCodes: []string{trust.FindingTargetAmbiguous},
		},
		{
			name:      "source path is file scope",
			raw:       "a/alpha.go",
			wantScope: trust.ScopeRef{Kind: trust.ScopeFile, Path: "a/alpha.go"},
			wantCodes: []string{},
		},
		{
			name:      "unnormalized source path is normalized before lookup",
			raw:       "./a/../a/alpha.go",
			wantScope: trust.ScopeRef{Kind: trust.ScopeFile, Path: "a/alpha.go"},
			wantCodes: []string{},
		},
		{
			name:      "bare miss is symbol-shaped not-found",
			raw:       "pkg.Missing",
			wantScope: trust.ScopeRef{Kind: trust.ScopeSymbol, Symbol: "pkg.Missing"},
			wantCodes: []string{trust.FindingTargetNotFound},
		},
		{
			name:      "package-looking miss is not-found with evidence gap",
			raw:       "missing/pkg",
			wantScope: trust.ScopeRef{Kind: trust.ScopePackage, Package: "missing/pkg"},
			wantCodes: []string{trust.FindingTargetNotFound, trust.FindingScopeEvidenceUnavailable},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			scope, findings, err := trust.ResolveScope(ctx, store, tc.raw)
			if err != nil {
				t.Fatalf("ResolveScope(%q): %v", tc.raw, err)
			}
			if scope != tc.wantScope {
				t.Errorf("scope = %+v, want %+v", scope, tc.wantScope)
			}
			if findings == nil {
				t.Fatal("findings = nil, want non-nil (empty slices, never null)")
			}
			if got := findingCodes(findings); len(got) != len(tc.wantCodes) || !equalStrings(got, tc.wantCodes) {
				t.Errorf("finding codes = %v, want %v", got, tc.wantCodes)
			}
			for _, f := range findings {
				if f.Scope != scope {
					t.Errorf("finding %s scope = %+v, want the resolved scope %+v", f.Code, f.Scope, scope)
				}
			}
		})
	}
}

func TestResolveScopeAmbiguousCandidates(t *testing.T) {
	ctx := context.Background()
	store, _ := seedScopeStore(t)

	_, findings, err := trust.ResolveScope(ctx, store, "pkg.Dup")
	if err != nil {
		t.Fatalf("ResolveScope: %v", err)
	}
	if len(findings) != 1 || findings[0].Code != trust.FindingTargetAmbiguous {
		t.Fatalf("findings = %+v, want exactly one TARGET_AMBIGUOUS", findings)
	}
	f := findings[0]
	if f.Severity != trust.SeverityError {
		t.Errorf("severity = %q, want error", f.Severity)
	}
	if f.Observed != "7" || f.Threshold != "1" {
		t.Errorf("observed/threshold = %q/%q, want 7/1", f.Observed, f.Threshold)
	}
	// Bounded candidate list: the five lexically-first candidates in sorted
	// order, then an explicit truncation marker; the remaining two absent.
	for i := 0; i < 5; i++ {
		want := fmt.Sprintf("function d%02d/dup.go", i)
		if !strings.Contains(f.Message, want) {
			t.Errorf("message misses candidate %q: %s", want, f.Message)
		}
	}
	for i := 5; i < 7; i++ {
		if strings.Contains(f.Message, fmt.Sprintf("d%02d/dup.go", i)) {
			t.Errorf("message leaks candidate beyond the bound of 5: %s", f.Message)
		}
	}
	if !strings.Contains(f.Message, "...") {
		t.Errorf("message misses the truncation marker: %s", f.Message)
	}
	if idx0, idx4 := strings.Index(f.Message, "d00/dup.go"), strings.Index(f.Message, "d04/dup.go"); idx0 > idx4 {
		t.Errorf("candidates not sorted ascending: %s", f.Message)
	}
}

// portlessStore hides every non-interface method of the wrapped store, so the
// SymbolLookupPort assertion fails.
type portlessStore struct{ graphstore.Graphstore }

func TestResolveScopeWithoutLookupPort(t *testing.T) {
	ctx := context.Background()
	store, _ := seedScopeStore(t)
	_, _, err := trust.ResolveScope(ctx, portlessStore{store}, "pkg.Alpha")
	if !errors.Is(err, trust.ErrSelectiveLookupUnavailable) {
		t.Errorf("err = %v, want ErrSelectiveLookupUnavailable", err)
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
