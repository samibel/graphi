package ingest_test

// WP-14 gate (gate-first, per-language). The WP-03 external-node materialization
// that gave Go taint a real sink node to match is rolled out to the other
// clause/FQN-keyed languages via engine/link/resolve_common.go. This E2E test
// drives the PRODUCTION ingest+link pipeline (real parsers, real resolvers) and
// asserts that an import-path-keyed reference to a stdlib / 3rd-party symbol whose
// package clause is absent from the repo is materialized as ONE interned
// `external` node with the EXACT fully-qualified name — for Java, Python, and the
// TypeScript family. It also pins the two safety properties: external nodes stay
// out of structural query results (query hygiene), and an in-repo import is never
// fabricated as external (no WP-03-style flood).

import (
	"context"
	"testing"

	"github.com/samibel/graphi/core/graphstore"
	"github.com/samibel/graphi/core/model"
	"github.com/samibel/graphi/core/parse"
	"github.com/samibel/graphi/engine/analysis/taint"
	"github.com/samibel/graphi/engine/query"
)

// externalQNs returns the qualified names of every interned `external` node in the
// store, sorted-insensitive (caller compares against a set).
func externalQNs(t *testing.T, store *graphstore.MemStore) []string {
	t.Helper()
	ctx := context.Background()
	nodes, err := store.Nodes(ctx, graphstore.Query{})
	if err != nil {
		t.Fatalf("read nodes: %v", err)
	}
	var out []string
	for _, n := range nodes {
		if n.Kind() == parse.KindExternal {
			out = append(out, n.QualifiedName())
		}
	}
	return out
}

func externalNodeID(t *testing.T, store *graphstore.MemStore, qn string) (model.NodeId, bool) {
	t.Helper()
	ctx := context.Background()
	nodes, err := store.Nodes(ctx, graphstore.Query{})
	if err != nil {
		t.Fatalf("read nodes: %v", err)
	}
	for _, n := range nodes {
		if n.Kind() == parse.KindExternal && n.QualifiedName() == qn {
			return n.ID(), true
		}
	}
	return "", false
}

func hasExternal(qns []string, want string) bool {
	for _, q := range qns {
		if q == want {
			return true
		}
	}
	return false
}

func TestExternalNodes_Java(t *testing.T) {
	ctx := context.Background()
	// A static utility call on an imported 3rd-party type: the canonical Java shape
	// the WP-14 external FQN is built for (import binds the type FQN; the selector
	// member appends). commons-lang3 is not in the repo, so it must materialize.
	repo := writeRepo(t, map[string]string{
		"src/main/java/com/app/Svc.java": `package com.app;

import org.apache.commons.lang3.StringUtils;

public class Svc {
    public String shout(String in) {
        return StringUtils.upperCase(in);
    }
}
`,
	})
	store := graphstore.NewMemStore()
	t.Cleanup(func() { _ = store.Close() })
	ing := newIngester(t, store, parse.NewDefaultRegistry())
	if err := ing.IngestAll(ctx, repo); err != nil {
		t.Fatalf("IngestAll: %v", err)
	}

	const want = "org.apache.commons.lang3.StringUtils.upperCase"
	qns := externalQNs(t, store)
	if !hasExternal(qns, want) {
		t.Fatalf("missing external node %q; got externals %v", want, qns)
	}
	assertExternalHygiene(t, store, want)
}

func TestExternalNodes_Python(t *testing.T) {
	ctx := context.Background()
	// import subprocess; subprocess.run(cmd) — a real command-execution sink. The
	// external node lets a taint config key a sink on "subprocess.run", the concrete
	// cross-language security payoff the rollout unlocks.
	repo := writeRepo(t, map[string]string{
		"app/runner.py": `import subprocess

def run_it(cmd):
    return subprocess.run(cmd, shell=True)
`,
		// An in-repo module import that MUST resolve internally, never as external.
		"app/main.py": `from app import runner

def main(cmd):
    return runner.run_it(cmd)
`,
	})
	store := graphstore.NewMemStore()
	t.Cleanup(func() { _ = store.Close() })
	ing := newIngester(t, store, parse.NewDefaultRegistry())
	if err := ing.IngestAll(ctx, repo); err != nil {
		t.Fatalf("IngestAll: %v", err)
	}

	const want = "subprocess.run"
	qns := externalQNs(t, store)
	if !hasExternal(qns, want) {
		t.Fatalf("missing external node %q; got externals %v", want, qns)
	}
	// No in-repo import is fabricated as external (no flood): only the genuine
	// stdlib reference is present, "app.*" internal refs are not external.
	for _, q := range qns {
		if q != want {
			t.Errorf("unexpected external node %q (in-repo reference wrongly materialized?)", q)
		}
	}
	assertExternalHygiene(t, store, want)

	// Security payoff: a taint config declaring subprocess.run as a command sink
	// classifies the materialized external node — impossible before it existed.
	cfg := taint.Config{
		Version: "wp14",
		Sources: []taint.SourceDef{{ID: "s", Label: "user_input", NamePatterns: []string{"argv"}}},
		Sinks:   []taint.SinkDef{{ID: "py_exec", Category: "command_injection", NamePatterns: []string{"subprocess.run"}}},
	}
	if _, cat := cfg.MatchSink("call", want); cat != "command_injection" {
		t.Errorf("taint config did not classify external sink %q (category %q)", want, cat)
	}
}

func TestExternalNodes_TypeScript(t *testing.T) {
	ctx := context.Background()
	// import { exec } from "child_process"; exec(cmd) — a non-relative package
	// import whose named binding resolves to no repo file, so it materializes as the
	// package-qualified external FQN.
	repo := writeRepo(t, map[string]string{
		"src/run.ts": `import { exec } from "child_process";

export function runIt(cmd: string) {
    return exec(cmd);
}
`,
	})
	store := graphstore.NewMemStore()
	t.Cleanup(func() { _ = store.Close() })
	ing := newIngester(t, store, parse.NewDefaultRegistry())
	if err := ing.IngestAll(ctx, repo); err != nil {
		t.Fatalf("IngestAll: %v", err)
	}

	const want = "child_process.exec"
	qns := externalQNs(t, store)
	if !hasExternal(qns, want) {
		t.Fatalf("missing external node %q; got externals %v", want, qns)
	}
	assertExternalHygiene(t, store, want)
}

// assertExternalHygiene pins that a materialized external node, while present in
// the store for name-keyed analyses (taint), never leaks into a structural query
// result — the WP-03 query-hygiene contract, which is kind-based and so already
// covers every language's external nodes.
func assertExternalHygiene(t *testing.T, store *graphstore.MemStore, extQN string) {
	t.Helper()
	ctx := context.Background()
	extID, ok := externalNodeID(t, store, extQN)
	if !ok {
		t.Fatalf("external node %q not found", extQN)
	}
	svc := query.New(store)
	// Find a real caller that has the external node as a callee, and assert the
	// external node is filtered out of the structural Callees result.
	edges, err := store.Edges(ctx, graphstore.Query{})
	if err != nil {
		t.Fatalf("read edges: %v", err)
	}
	var caller model.NodeId
	for _, e := range edges {
		if e.To() == extID && (e.Kind() == "calls" || e.Kind() == "references") {
			if e.Tier() == model.TierConfirmed {
				t.Errorf("external edge is confirmed tier: %s", e.ID())
			}
			caller = e.From()
			break
		}
	}
	if caller == "" {
		t.Fatalf("no calls/references edge targets external node %q", extQN)
	}
	res, err := svc.Callees(ctx, caller)
	if err != nil {
		t.Fatalf("Callees: %v", err)
	}
	if containsNode(res, extID) {
		t.Errorf("external node %q leaked into structural Callees result", extQN)
	}
}
