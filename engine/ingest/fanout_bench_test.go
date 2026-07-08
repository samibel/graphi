package ingest_test

// RED GATE (WP-00b / gate-first). This fixture reproduces the Spring-Boot
// field-finding's edge explosion in miniature: Java imports are collapsed to
// their penultimate package segment and byClause then groups EVERY directory
// with that basename repo-wide (engine/link/resolve_java.go + resolve_common.go
// + index.go), so a single `import ...service.Foo` fans out to file->file edges
// against every `service`/`model`/`repo` directory in the repository. That is
// the 4.27M-edge multiplier observed on the real monorepo.
//
// The gate measures the edges-per-node ratio on a synthetic repo with many
// same-basename package directories. It is RED today (high ratio from the
// fan-out) and DISARMED (budgetArmed=false): it logs the measured ratio every
// run without failing CI. WP-01 replaces the file->file fan-out with a single
// `file -> package` edge; WP-08 arms this budget as a hard CI gate.

import (
	"context"
	"fmt"
	"testing"

	"github.com/samibel/graphi/core/graphstore"
	"github.com/samibel/graphi/core/parse"
)

// budgetArmed is the WP-08 switch. While false, the ratio is measured and
// logged but not enforced. WP-08 sets it true and the assertion below becomes a
// hard gate.
const budgetArmed = true

// maxEdgesPerNode is the RED budget: after WP-01 the fan-out collapses to one
// package edge per import, so the ratio must fall below this. The current
// fan-out blows well past it (measured and logged below).
const maxEdgesPerNode = 8.0

// genJavaFanoutRepo builds a synthetic multi-module Java repository whose
// package directories deliberately repeat the same basenames (service, model,
// repo) across modules — exactly the shape that triggers the byClause fan-out.
func genJavaFanoutRepo(modules, filesPerPkg int) map[string]string {
	files := map[string]string{}
	pkgs := []string{"service", "model", "repo"}
	for m := 0; m < modules; m++ {
		base := fmt.Sprintf("module%d/src/main/java/com/example%d", m, m)
		for _, pkg := range pkgs {
			for f := 0; f < filesPerPkg; f++ {
				cls := fmt.Sprintf("%s%d_%d", pkg, m, f)
				// Every class imports a `service` clause. Because the resolver
				// collapses the import to its penultimate segment ("service")
				// and byClause groups every `service` directory repo-wide, each
				// of these imports fans out to file->file edges against every
				// service file in every module — the quadratic multiplier the
				// real monorepo exhibited (many importers x many same-basename
				// files).
				files[fmt.Sprintf("%s/%s/%s.java", base, pkg, cls)] =
					fmt.Sprintf("package com.example%d.%s;\n\nimport com.example%d.service.service%d_0;\n\npublic class %s {\n    public void run() {\n        new service%d_0().run();\n    }\n}\n",
						m, pkg, m, m, cls, m)
			}
		}
		// An app class that imports one class from each same-basename package.
		imports := ""
		body := ""
		for _, pkg := range pkgs {
			imports += fmt.Sprintf("import com.example%d.%s.%s%d_0;\n", m, pkg, pkg, m)
			body += fmt.Sprintf("        new %s%d_0().run();\n", pkg, m)
		}
		files[fmt.Sprintf("%s/app/App%d.java", base, m)] =
			fmt.Sprintf("package com.example%d.app;\n\n%s\npublic class App%d {\n    public void main() {\n%s    }\n}\n",
				m, imports, m, body)
	}
	return files
}

func TestLinkFanout_EdgeExplosionBudget(t *testing.T) {
	ctx := context.Background()

	store := graphstore.NewMemStore()
	t.Cleanup(func() { _ = store.Close() })

	repo := writeRepo(t, genJavaFanoutRepo(8, 5))
	ing := newIngester(t, store, parse.NewDefaultRegistry())
	if err := ing.IngestAll(ctx, repo); err != nil {
		t.Fatalf("ingest java fan-out fixture: %v", err)
	}

	nodes, err := store.Nodes(ctx, graphstore.Query{})
	if err != nil {
		t.Fatalf("read nodes: %v", err)
	}
	edges, err := store.Edges(ctx, graphstore.Query{})
	if err != nil {
		t.Fatalf("read edges: %v", err)
	}

	nodeCount := len(nodes)
	if nodeCount == 0 {
		// Guard against a silent pass: if ingestion produced no nodes (a parse or
		// config regression), ratio would be 0.0 < budget and the gate would pass
		// without actually measuring the fan-out.
		t.Fatalf("no nodes ingested from the java fan-out fixture — cannot measure edges/node")
	}
	edgeCount := len(edges)
	ratio := float64(edgeCount) / float64(nodeCount)

	t.Logf("[RED GATE WP-08] java fan-out: nodes=%d, edges=%d, edges/node=%.2f, budget=%.2f, armed=%v",
		nodeCount, edgeCount, ratio, maxEdgesPerNode, budgetArmed)

	if !budgetArmed {
		if ratio <= maxEdgesPerNode {
			t.Logf("edges/node within budget — arm the gate: set budgetArmed=true (WP-08)")
		}
		return
	}

	if ratio > maxEdgesPerNode {
		t.Errorf("edges/node = %.2f exceeds budget %.2f (import fan-out not collapsed to package nodes)",
			ratio, maxEdgesPerNode)
	}
}
