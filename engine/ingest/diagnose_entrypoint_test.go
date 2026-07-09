package ingest_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/samibel/graphi/core/graphstore"
	"github.com/samibel/graphi/core/parse"
	"github.com/samibel/graphi/engine/diagnostic"
)

// TestDiagnose_EntryPointsNotDead is the WP-10+WP-11 gate: a real Java ingest
// through the SQLite backend must attach source annotations/flags to nodes,
// persist them in the nodes.meta column, and have the dead_symbol analyzer read
// them off the STORED graph so framework/language entry points are NOT flagged
// as dead. Only the genuinely-dead private method survives as a warning.
//
// It also exercises persistence end to end: the cache is evicted after ingest so
// Diagnose reads nodes (and their meta) back through the SQL layer.
func TestDiagnose_EntryPointsNotDead(t *testing.T) {
	ctx := context.Background()

	// One Spring-flavored class: a @Configuration class with a @Bean method, a
	// @Test method, a main entry point, and a genuinely-dead private helper.
	src := `package com.example.app;

@Configuration
public class App {
    @Bean
    public String dataSource() {
        return "ds";
    }

    @Test
    public void checksSomething() {
    }

    public static void main(String[] args) {
    }

    private void unusedHelper() {
    }
}
`
	root := writeRepo(t, map[string]string{"app/App.java": src})

	store, err := graphstore.OpenSQLite(filepath.Join(t.TempDir(), "graphi.db"))
	if err != nil {
		t.Fatalf("OpenSQLite: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	ing := newIngester(t, store, parse.NewDefaultRegistry())
	if err := ing.IngestAll(ctx, root); err != nil {
		t.Fatalf("IngestAll: %v", err)
	}
	// Force the diagnose pass to read meta back through SQLite (not a hot cache
	// that still holds the freshly-ingested in-memory nodes).
	if err := store.EvictCache(ctx); err != nil {
		t.Fatalf("EvictCache: %v", err)
	}

	// Primary gate: the DEFAULT diagnose output (what a user sees) must carry
	// exactly one dead_symbol WARNING — the genuinely-dead private helper — and
	// zero warnings on the @Bean/@Test/main entry points.
	res, err := diagnostic.Diagnose(ctx, store, []string{diagnostic.KindDeadSymbol})
	if err != nil {
		t.Fatalf("Diagnose: %v", err)
	}
	var deadWarnings []diagnostic.Diagnostic
	for _, d := range res.Diagnostics {
		if d.Code == "dead_symbol" && d.Severity == diagnostic.SeverityWarning {
			deadWarnings = append(deadWarnings, d)
		}
	}
	if len(deadWarnings) != 1 {
		t.Fatalf("want exactly 1 dead_symbol warning by default, got %d: %+v", len(deadWarnings), deadWarnings)
	}
	if got := qnOf(deadWarnings[0].Message); got != "app.unusedHelper" {
		t.Fatalf("dead warning should be app.unusedHelper, got %q (%+v)", got, deadWarnings[0])
	}

	// Corroborate with --all (unfiltered): every entry point is reported as an
	// info-severity entrypoint_candidate rather than a warning. --all keeps
	// suppressed findings visible so the @Configuration class shows too.
	all, err := diagnostic.DiagnoseWithOptions(ctx, store, []string{diagnostic.KindDeadSymbol}, diagnostic.DiagnoseOptions{All: true})
	if err != nil {
		t.Fatalf("DiagnoseWithOptions(--all): %v", err)
	}
	entryPointInfos := map[string]bool{}
	for _, d := range all.Diagnostics {
		if d.Code != "dead_symbol" {
			continue
		}
		qn := qnOf(d.Message)
		isEntryPoint := qn == "app.dataSource" || qn == "app.checksSomething" || qn == "app.main" || qn == "app.App"
		if isEntryPoint && d.Severity == diagnostic.SeverityWarning {
			t.Errorf("entry point %q wrongly flagged as a dead_symbol WARNING", qn)
		}
		if d.Severity == diagnostic.SeverityInfo && d.Reason == diagnostic.ReasonEntrypointCandidate {
			entryPointInfos[qn] = true
		}
	}
	for _, want := range []string{"app.dataSource", "app.checksSomething", "app.main", "app.App"} {
		if !entryPointInfos[want] {
			t.Errorf("entry point %q should be an info entrypoint_candidate under --all, got none (infos=%v)", want, entryPointInfos)
		}
	}
}

// TestDiagnose_KotlinOverrideNotDead is the WP-14 follow-up gate: a Kotlin
// `override fun` with no in-graph caller (its supertype method is invoked
// polymorphically, an edge the static graph resolves to the base type) must NOT
// be flagged as a dead_symbol warning — it is exempt via the extractor-attached
// "override" flag, surfacing as an info entrypoint_candidate instead. Java's
// `@Override` annotation already covered the annotation-based languages; this
// covers the `override`-keyword languages through the real ingest+SQLite path.
func TestDiagnose_KotlinOverrideNotDead(t *testing.T) {
	ctx := context.Background()

	src := `package com.example.app

class Widget : Base {
    override fun render(): Int {
        return 1
    }

    fun deadHelper(): Int {
        return 2
    }
}
`
	root := writeRepo(t, map[string]string{"app/Widget.kt": src})

	store, err := graphstore.OpenSQLite(filepath.Join(t.TempDir(), "graphi.db"))
	if err != nil {
		t.Fatalf("OpenSQLite: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	ing := newIngester(t, store, parse.NewDefaultRegistry())
	if err := ing.IngestAll(ctx, root); err != nil {
		t.Fatalf("IngestAll: %v", err)
	}
	if err := store.EvictCache(ctx); err != nil {
		t.Fatalf("EvictCache: %v", err)
	}

	// --all keeps suppressed findings visible, so the override method surfaces as
	// an info entrypoint_candidate; it must never be a warning.
	all, err := diagnostic.DiagnoseWithOptions(ctx, store, []string{diagnostic.KindDeadSymbol}, diagnostic.DiagnoseOptions{All: true})
	if err != nil {
		t.Fatalf("DiagnoseWithOptions(--all): %v", err)
	}
	var sawOverrideInfo bool
	for _, d := range all.Diagnostics {
		if d.Code != "dead_symbol" {
			continue
		}
		if qnOf(d.Message) != "app.render" {
			continue
		}
		if d.Severity == diagnostic.SeverityWarning {
			t.Errorf("Kotlin override app.render wrongly flagged as a dead_symbol WARNING")
		}
		if d.Severity == diagnostic.SeverityInfo && d.Reason == diagnostic.ReasonEntrypointCandidate {
			sawOverrideInfo = true
		}
	}
	if !sawOverrideInfo {
		t.Errorf("Kotlin override app.render should be an info entrypoint_candidate (override exemption)")
	}
}

// TestDiagnose_OverrideKeywordNotDead extends the Kotlin override gate to the
// other `override`-keyword languages (C#, TypeScript): an override member with no
// in-graph caller must surface as an info entrypoint_candidate, never a
// dead_symbol warning, through the real ingest+SQLite path.
func TestDiagnose_OverrideKeywordNotDead(t *testing.T) {
	cases := []struct {
		name string
		file string
		src  string
		qn   string
	}{
		{
			name: "csharp",
			file: "app/Widget.cs",
			src: `namespace app;

class Widget : Base {
    public override int Render() { return 1; }
}
`,
			qn: "app.Render",
		},
		{
			name: "typescript",
			file: "app/widget.ts",
			src: `class Widget extends Base {
    override render(): number { return 1; }
}
`,
			qn: "app.render",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			root := writeRepo(t, map[string]string{tc.file: tc.src})

			store, err := graphstore.OpenSQLite(filepath.Join(t.TempDir(), "graphi.db"))
			if err != nil {
				t.Fatalf("OpenSQLite: %v", err)
			}
			t.Cleanup(func() { _ = store.Close() })

			ing := newIngester(t, store, parse.NewDefaultRegistry())
			if err := ing.IngestAll(ctx, root); err != nil {
				t.Fatalf("IngestAll: %v", err)
			}
			if err := store.EvictCache(ctx); err != nil {
				t.Fatalf("EvictCache: %v", err)
			}

			all, err := diagnostic.DiagnoseWithOptions(ctx, store, []string{diagnostic.KindDeadSymbol}, diagnostic.DiagnoseOptions{All: true})
			if err != nil {
				t.Fatalf("DiagnoseWithOptions(--all): %v", err)
			}
			var sawInfo bool
			for _, d := range all.Diagnostics {
				if d.Code != "dead_symbol" || qnOf(d.Message) != tc.qn {
					continue
				}
				if d.Severity == diagnostic.SeverityWarning {
					t.Errorf("%s override %q wrongly flagged as a dead_symbol WARNING", tc.name, tc.qn)
				}
				if d.Severity == diagnostic.SeverityInfo && d.Reason == diagnostic.ReasonEntrypointCandidate {
					sawInfo = true
				}
			}
			if !sawInfo {
				t.Errorf("%s override %q should be an info entrypoint_candidate", tc.name, tc.qn)
			}
		})
	}
}

// TestDiagnose_TSDecoratedNotDead is the WP-14 follow-up gate: a decorated
// TypeScript class/method with no in-graph caller (the framework instantiates or
// invokes it via the decorator's registration) must surface as an info
// entrypoint_candidate, never a dead_symbol warning, through the real
// ingest+SQLite path.
func TestDiagnose_TSDecoratedNotDead(t *testing.T) {
	ctx := context.Background()
	src := `@Controller("/widgets")
class WidgetController {
    @Get("/")
    list(): number { return 1; }
}
`
	root := writeRepo(t, map[string]string{"app/widget.controller.ts": src})

	store, err := graphstore.OpenSQLite(filepath.Join(t.TempDir(), "graphi.db"))
	if err != nil {
		t.Fatalf("OpenSQLite: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	ing := newIngester(t, store, parse.NewDefaultRegistry())
	if err := ing.IngestAll(ctx, root); err != nil {
		t.Fatalf("IngestAll: %v", err)
	}
	if err := store.EvictCache(ctx); err != nil {
		t.Fatalf("EvictCache: %v", err)
	}

	all, err := diagnostic.DiagnoseWithOptions(ctx, store, []string{diagnostic.KindDeadSymbol}, diagnostic.DiagnoseOptions{All: true})
	if err != nil {
		t.Fatalf("DiagnoseWithOptions(--all): %v", err)
	}
	// Both the decorated class and the decorated handler method must be exempt.
	want := map[string]bool{"app.WidgetController": false, "app.list": false}
	for _, d := range all.Diagnostics {
		if d.Code != "dead_symbol" {
			continue
		}
		qn := qnOf(d.Message)
		if _, tracked := want[qn]; !tracked {
			continue
		}
		if d.Severity == diagnostic.SeverityWarning {
			t.Errorf("decorated TS symbol %q wrongly flagged as a dead_symbol WARNING", qn)
		}
		if d.Severity == diagnostic.SeverityInfo && d.Reason == diagnostic.ReasonEntrypointCandidate {
			want[qn] = true
		}
	}
	for qn, ok := range want {
		if !ok {
			t.Errorf("decorated TS symbol %q should be an info entrypoint_candidate", qn)
		}
	}
}

// qnOf extracts the quoted qualified name from a diagnostic message.
func qnOf(msg string) string {
	start := -1
	for i := 0; i < len(msg); i++ {
		if msg[i] == '"' {
			start = i + 1
			break
		}
	}
	if start < 0 {
		return ""
	}
	for i := start; i < len(msg); i++ {
		if msg[i] == '"' {
			return msg[start:i]
		}
	}
	return ""
}
