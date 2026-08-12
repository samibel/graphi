package testintel

import (
	"context"
	"testing"

	"github.com/samibel/graphi/core/graphstore"
	"github.com/samibel/graphi/core/model"
	"github.com/samibel/graphi/engine/query"
)

// fixture builds:
//
//	util.Format           (util/format.go)
//	util.parseName        (util/format.go, unexported helper)
//	tests.TestFormat      (util/format_test.go) --calls(confirmed)--> util.Format   [direct]
//	tests.TestFormat_Edge (util/format_test.go)                                     [naming only]
//	main.Run              (cmd/app/main.go)     --calls(derived)-->   util.Format
//	tests.TestRun         (cmd/app/main_test.go) --calls(confirmed)--> main.Run     [transitive to Format]
//	other.TestUnrelated   (other/other_test.go)                                     [unrelated universe file]
func fixture(t *testing.T) (*query.Service, map[string]model.Node) {
	t.Helper()
	ctx := context.Background()
	store := graphstore.NewMemStore()
	nodes := map[string]model.Node{}

	mk := func(kind, qn, path string, line int) model.Node {
		n, err := model.NewNode(kind, qn, path, line, 1)
		if err != nil {
			t.Fatalf("node %s: %v", qn, err)
		}
		if err := store.PutNode(ctx, n); err != nil {
			t.Fatalf("put node %s: %v", qn, err)
		}
		nodes[qn] = n
		return n
	}
	format := mk("function", "util.Format", "util/format.go", 3)
	mk("function", "util.parseName", "util/format.go", 20)
	testFormat := mk("function", "tests.TestFormat", "util/format_test.go", 8)
	mk("function", "tests.TestFormat_Edge", "util/format_test.go", 30)
	run := mk("function", "main.Run", "cmd/app/main.go", 10)
	testRun := mk("function", "tests.TestRun", "cmd/app/main_test.go", 4)
	mk("function", "other.TestUnrelated", "other/other_test.go", 1)

	edge := func(from, to model.Node, kind string, tier model.ConfidenceTier, ev string) {
		e, err := model.NewEdge(from.ID(), to.ID(), kind, tier, 0.9, "test fixture", []string{ev})
		if err != nil {
			t.Fatalf("edge: %v", err)
		}
		if err := store.PutEdge(ctx, e); err != nil {
			t.Fatalf("put edge: %v", err)
		}
	}
	edge(testFormat, format, "calls", model.TierConfirmed, "util/format_test.go:9")
	edge(run, format, "calls", model.TierDerived, "cmd/app/main.go:12")
	edge(testRun, run, "calls", model.TierConfirmed, "cmd/app/main_test.go:5")

	return query.New(store), nodes
}

func linksBySignal(links []Link) map[Signal][]Link {
	out := map[Signal][]Link{}
	for _, l := range links {
		out[l.Signal] = append(out[l.Signal], l)
	}
	return out
}

func TestTestsForSignals(t *testing.T) {
	svc, nodes := fixture(t)
	res, err := TestsFor(context.Background(), svc, []model.Node{nodes["util.Format"]}, 2)
	if err != nil {
		t.Fatal(err)
	}
	by := linksBySignal(res.Links)

	// Direct: TestFormat calls Format.
	if len(by[SignalDirectCall]) != 1 || by[SignalDirectCall][0].Test.QualifiedName != "tests.TestFormat" {
		t.Fatalf("direct links = %+v", by[SignalDirectCall])
	}
	if by[SignalDirectCall][0].Depth != 1 || by[SignalDirectCall][0].Tier != model.TierConfirmed {
		t.Fatalf("direct link metadata wrong: %+v", by[SignalDirectCall][0])
	}

	// Transitive: TestRun → Run → Format at depth 2.
	if len(by[SignalTransitive]) != 1 || by[SignalTransitive][0].Test.QualifiedName != "tests.TestRun" || by[SignalTransitive][0].Depth != 2 {
		t.Fatalf("transitive links = %+v", by[SignalTransitive])
	}

	// Naming: TestFormat_Edge matches Test<Format> prefix in the same dir; the
	// plain TestFormat naming match is deduplicated into... no: dedup is per
	// (subject, test, signal), so TestFormat also appears as a naming link.
	names := map[string]bool{}
	for _, l := range by[SignalNaming] {
		names[l.Test.QualifiedName] = true
	}
	if !names["tests.TestFormat_Edge"] || !names["tests.TestFormat"] {
		t.Fatalf("naming links = %+v", by[SignalNaming])
	}

	// Universe and neighborhood.
	if len(res.AllTestFiles) != 3 {
		t.Fatalf("AllTestFiles = %v", res.AllTestFiles)
	}
	if len(res.NearbyTestFiles) != 1 || res.NearbyTestFiles[0] != "util/format_test.go" {
		t.Fatalf("NearbyTestFiles = %v", res.NearbyTestFiles)
	}
}

func TestTestsForDepthOneExcludesTransitive(t *testing.T) {
	svc, nodes := fixture(t)
	res, err := TestsFor(context.Background(), svc, []model.Node{nodes["util.Format"]}, 1)
	if err != nil {
		t.Fatal(err)
	}
	for _, l := range res.Links {
		if l.Signal == SignalTransitive {
			t.Fatalf("depth 1 must not produce transitive links: %+v", l)
		}
	}
}

func TestTestsForDeterministic(t *testing.T) {
	svc, nodes := fixture(t)
	subjects := []model.Node{nodes["util.Format"], nodes["main.Run"]}
	a, err := TestsFor(context.Background(), svc, subjects, 2)
	if err != nil {
		t.Fatal(err)
	}
	// Reversed subject order must not change the sorted output.
	b, err := TestsFor(context.Background(), svc, []model.Node{subjects[1], subjects[0]}, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(a.Links) != len(b.Links) {
		t.Fatalf("link counts differ: %d vs %d", len(a.Links), len(b.Links))
	}
	for i := range a.Links {
		if a.Links[i].Subject != b.Links[i].Subject || a.Links[i].Test.ID != b.Links[i].Test.ID || a.Links[i].Signal != b.Links[i].Signal {
			t.Fatalf("link %d differs: %+v vs %+v", i, a.Links[i], b.Links[i])
		}
	}
}

func TestMatchesNaming(t *testing.T) {
	cases := []struct {
		test, subject string
		want          bool
	}{
		{"TestFormat", "Format", true},
		{"TestFormat_Empty", "Format", true},
		{"BenchmarkFormat", "Format", true},
		{"FormatTest", "Format", true},
		{"test_format", "Format", true},
		{"test_format_empty", "Format", true},
		{"TestParse", "Format", false},
		{"TestFor", "Format", false}, // prefix of the subject, not the reverse
		{"", "Format", false},
		{"TestFormat", "", false},
	}
	for _, c := range cases {
		if got := matchesNaming(c.test, c.subject); got != c.want {
			t.Errorf("matchesNaming(%q, %q) = %v, want %v", c.test, c.subject, got, c.want)
		}
	}
}

func TestSubjectsFromDiff(t *testing.T) {
	svc, _ := fixture(t)
	subjects, unresolved, truncated, err := SubjectsFromDiff(context.Background(), svc,
		[]string{"util/format.go", "docs/readme.md"}, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if truncated {
		t.Fatal("small diff must not truncate")
	}
	if len(subjects) != 2 { // util.Format + util.parseName
		t.Fatalf("subjects = %d, want 2", len(subjects))
	}
	if len(unresolved) != 1 || unresolved[0] != "docs/readme.md" {
		t.Fatalf("unresolved = %v", unresolved)
	}
}
