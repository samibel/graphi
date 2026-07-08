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
