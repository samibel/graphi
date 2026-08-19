package main

// W0.g — abstention must be visible on the CLI, not merely reachable from the
// shared composition.
//
// This file also pins a wiring defect the story surfaced and fixed: the CLI
// used to resolve the repository root ONLY when -policy was given, leaving the
// composition to fall back to the PROCESS working directory for every
// evidence-backed read. That is the right store only when the two happen to
// coincide, which they do in an interactive shell and do NOT in a test, a
// daemon, or any `-root`-style invocation — so the abstention record read as
// unavailable exactly where a harness would have caught it. The test therefore
// runs the CLI against a fixture repo that is NOT the process cwd, which is the
// condition the defect needed.

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/samibel/graphi/core/graphstore"
	"github.com/samibel/graphi/engine/semantic"
)

// javaStrictFixture writes and indexes a Java repository whose binder is
// FORCED to abstain, through the real `graphi sync` path and the auto-managed
// store. Returns the repo root and the node id of tax.value.
func javaStrictFixture(t *testing.T) (repo, target string) {
	t.Helper()
	stateHome := t.TempDir()
	t.Setenv("XDG_STATE_HOME", stateHome)
	t.Setenv("GRAPHI_EMBEDDER", "")
	// The JVM registrants are default-off (WP-J11); the abstention record only
	// exists where they run, so the opt-in is part of the fixture and is
	// stated rather than assumed.
	t.Setenv(semantic.EnvJVM, "1")

	repo = t.TempDir()
	files := map[string]string{
		"com/tax/Rate.java": `package com.tax;
public class Rate {
    public void value() {}
}
`,
		"com/shop/Shop.java": `package com.shop;
import com.tax.Rate;
public class Shop {
    public void run(Rate param) {
        param.value();
        var inferred = param;
        inferred.value();
        param.missing();
        java.util.List<String> ext = null;
        ext.size();
        Unknown u = null;
        u.thing();
    }
}
`,
	}
	for name, content := range files {
		p := filepath.Join(repo, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	gitRepo(t, repo, "main")
	if code := runSyncAt(repo, nil, new(bytes.Buffer)); code != 0 {
		t.Fatal("seed sync failed")
	}

	matches, _ := filepath.Glob(filepath.Join(stateHome, "graphi", "*", "db.sqlite"))
	if len(matches) != 1 {
		t.Fatalf("expected one auto-managed db, got %v", matches)
	}
	store, err := graphstore.OpenSQLiteReadOnly(matches[0])
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	nodes, err := store.Nodes(t.Context(), graphstore.Query{})
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range nodes {
		if n.QualifiedName() == "tax.value" {
			target = string(n.ID())
		}
	}
	if target == "" {
		t.Fatal("fixture node tax.value not found")
	}
	return repo, target
}

// TestQueryStrict_CLISurfacesAbstention is the CLI half of AC-9, and the
// regression pin for the root-resolution fix: NO -db, NO -meta, NO -policy —
// the plain invocation a user makes — must still find the repository's
// evidence and report what the binder refused.
func TestQueryStrict_CLISurfacesAbstention(t *testing.T) {
	repo, target := javaStrictFixture(t)

	var out bytes.Buffer
	if code := runQueryStrictAt(repo, []string{"callers", "-symbol", target}, &out); code != 0 {
		t.Fatalf("exit = %d\n%s", code, out.String())
	}
	var env struct {
		Limitations []string `json:"limitations"`
	}
	if err := json.Unmarshal(out.Bytes(), &env); err != nil {
		t.Fatalf("envelope is not JSON: %v\n%s", err, out.String())
	}

	var notice string
	for _, l := range env.Limitations {
		if strings.Contains(l, "abstention") {
			notice = l
		}
	}
	if notice == "" {
		t.Fatalf("the CLI carried no abstention notice for a Java result: %#v", env.Limitations)
	}
	// Named reasons with their counts, and the scope limit beside them — the
	// same text the MCP tool emits, because both ride one composition.
	for _, want := range []string{
		"java_var_inferred 1",
		"java_lookup_not_found 1",
		"java_receiver_external 2",
		"repository-global",
	} {
		if !strings.Contains(notice, want) {
			t.Errorf("CLI notice does not carry %q: %s", want, notice)
		}
	}
	// The failing side of the fix, stated: an UNAVAILABLE notice here would
	// mean the CLI handed the composition the wrong repository again.
	if strings.Contains(notice, "UNAVAILABLE") {
		t.Errorf("the CLI could not read its own repository's abstention record: %s", notice)
	}
}
