package retrieval

import (
	"context"
	"reflect"
	"testing"
)

func TestLifecycleFocusedQueryIsNarrowAndDeterministic(t *testing.T) {
	if got, want := lifecycleFocusedQuery("How to access Persistent Flags values in init function?"), "values in init function"; got != want {
		t.Fatalf("lifecycleFocusedQuery = %q, want %q", got, want)
	}
	for _, query := range []string{
		"How to set flags in test?",
		"how does request dispatch reach completion logic",
		"initialize configuration values",
		"InitHook",
	} {
		if got := lifecycleFocusedQuery(query); got != "" {
			t.Fatalf("lifecycleFocusedQuery(%q) = %q, want disabled", query, got)
		}
	}
}

func TestLifecycleTailCanAdmitInitializationFunction(t *testing.T) {
	sem := &lifecycleSemantic{hits: map[string][]semanticHit{
		"How to access Persistent Flags values in init function?": {{NodeID: "broad", QualifiedName: "pkg.PersistentFlags", Path: "broad.go", Line: 1, CosineScore: 0.80}},
		"values in init function":                                 {{NodeID: "focused", QualifiedName: "pkg.OnInitialize", Path: "focused.go", Line: 2, CosineScore: 0.95}},
	}}
	e := newEngine(&fakeLexical{}, sem, nil)
	res, err := e.Retrieve(context.Background(), Request{Query: "How to access Persistent Flags values in init function?", Limit: 2})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := rowOrder(res.Rows), []string{"focused", "broad"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("rows = %v, want %v", got, want)
	}
	if got, want := sem.queries, []string{"How to access Persistent Flags values in init function?", "values in init function"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("semantic queries = %v, want %v", got, want)
	}
}

type lifecycleSemantic struct {
	hits    map[string][]semanticHit
	queries []string
}

func (s *lifecycleSemantic) search(_ context.Context, query string, _ int) (semanticOutcome, error) {
	s.queries = append(s.queries, query)
	return semanticOutcome{Available: true, State: StateReady, Hits: s.hits[query]}, nil
}
