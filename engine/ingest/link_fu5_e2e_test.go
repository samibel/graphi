package ingest_test

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/samibel/graphi/core/graphstore"
	"github.com/samibel/graphi/core/model"
	"github.com/samibel/graphi/core/parse"
	"github.com/samibel/graphi/engine/query"
)

// fu5XfileCase is one language's cross-file e2e + golden fixture.
type fu5XfileCase struct {
	name        string
	initial     map[string]string // repo for the cross-file query assertion
	fromQN      string            // caller
	toQN        string            // expected cross-file callee
	mutate      map[string]string // files to rewrite for the golden mutation (rename target)
	changed     []string          // changed paths for IngestChanged
	deletePath  string            // optional file to delete in the mutation
	fullMutated map[string]string // the full mutated repo (must equal incremental)
}

// TestLink_FU5_CrossFileAndGolden runs, for the primary language of each FU-5
// language group (Rust, Java, C, PHP, Bash), a real-parser cross-file query
// assertion plus a byte-identical full-vs-incremental golden check across a
// rename-of-target cascade.
func TestLink_FU5_CrossFileAndGolden(t *testing.T) {
	cases := []fu5XfileCase{
		{
			name: "rust",
			initial: map[string]string{
				"app/main.rs": "use crate::shop::price;\nfn checkout() -> i32 { price() }\n",
				"shop/lib.rs": "pub fn price() -> i32 { 10 }\n",
			},
			fromQN: "app.checkout", toQN: "shop.price",
			mutate: map[string]string{
				"shop/lib.rs": "pub fn cost() -> i32 { 10 }\n",
				"app/main.rs": "use crate::shop::cost;\nfn checkout() -> i32 { cost() }\n",
			},
			changed: []string{"app/main.rs", "shop/lib.rs"},
			fullMutated: map[string]string{
				"app/main.rs": "use crate::shop::cost;\nfn checkout() -> i32 { cost() }\n",
				"shop/lib.rs": "pub fn cost() -> i32 { 10 }\n",
			},
		},
		{
			name: "java",
			initial: map[string]string{
				"com/app/Main.java":   "package com.app;\nimport com.shop.Price;\npublic class Main { int checkout(){ return Price.of(); } }\n",
				"com/shop/Price.java": "package com.shop;\npublic class Price { static int of(){ return 10; } }\n",
			},
			fromQN: "app.checkout", toQN: "shop.of",
			mutate: map[string]string{
				"com/shop/Price.java": "package com.shop;\npublic class Price { static int make(){ return 10; } }\n",
				"com/app/Main.java":   "package com.app;\nimport com.shop.Price;\npublic class Main { int checkout(){ return Price.make(); } }\n",
			},
			changed: []string{"com/app/Main.java", "com/shop/Price.java"},
			fullMutated: map[string]string{
				"com/app/Main.java":   "package com.app;\nimport com.shop.Price;\npublic class Main { int checkout(){ return Price.make(); } }\n",
				"com/shop/Price.java": "package com.shop;\npublic class Price { static int make(){ return 10; } }\n",
			},
		},
		{
			name: "c",
			initial: map[string]string{
				"app/main.c":     "#include \"lib/util.h\"\nint checkout(){ return helper(); }\n",
				"app/lib/util.h": "int helper();\n",
				"app/lib/util.c": "int helper(){ return 1; }\n",
			},
			fromQN: "app.checkout", toQN: "lib.helper",
			mutate: map[string]string{
				"app/lib/util.c": "int work(){ return 1; }\n",
				"app/lib/util.h": "int work();\n",
				"app/main.c":     "#include \"lib/util.h\"\nint checkout(){ return work(); }\n",
			},
			changed: []string{"app/main.c", "app/lib/util.c", "app/lib/util.h"},
			fullMutated: map[string]string{
				"app/main.c":     "#include \"lib/util.h\"\nint checkout(){ return work(); }\n",
				"app/lib/util.h": "int work();\n",
				"app/lib/util.c": "int work(){ return 1; }\n",
			},
		},
		{
			name: "php",
			initial: map[string]string{
				"app/main.php":        "<?php\nrequire 'lib/helpers.php';\nfunction checkout(){ return helper(); }\n",
				"app/lib/helpers.php": "<?php\nfunction helper(){ return 1; }\n",
			},
			fromQN: "app.checkout", toQN: "lib.helper",
			mutate: map[string]string{
				"app/lib/helpers.php": "<?php\nfunction work(){ return 1; }\n",
				"app/main.php":        "<?php\nrequire 'lib/helpers.php';\nfunction checkout(){ return work(); }\n",
			},
			changed: []string{"app/main.php", "app/lib/helpers.php"},
			fullMutated: map[string]string{
				"app/main.php":        "<?php\nrequire 'lib/helpers.php';\nfunction checkout(){ return work(); }\n",
				"app/lib/helpers.php": "<?php\nfunction work(){ return 1; }\n",
			},
		},
		{
			name: "bash",
			initial: map[string]string{
				"app/main.sh":     "source ./lib/util.sh\ncheckout(){ helper; }\n",
				"app/lib/util.sh": "helper(){ echo 1; }\n",
			},
			fromQN: "app.checkout", toQN: "lib.helper",
			mutate: map[string]string{
				"app/lib/util.sh": "work(){ echo 1; }\n",
				"app/main.sh":     "source ./lib/util.sh\ncheckout(){ work; }\n",
			},
			changed: []string{"app/main.sh", "app/lib/util.sh"},
			fullMutated: map[string]string{
				"app/main.sh":     "source ./lib/util.sh\ncheckout(){ work; }\n",
				"app/lib/util.sh": "work(){ echo 1; }\n",
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name+"_crossfile", func(t *testing.T) {
			ctx := context.Background()
			repo := writeRepo(t, tc.initial)
			store := graphstore.NewMemStore()
			t.Cleanup(func() { _ = store.Close() })
			ing := newIngester(t, store, parse.NewDefaultRegistry())
			if err := ing.IngestAll(ctx, repo); err != nil {
				t.Fatalf("IngestAll: %v", err)
			}
			nodes, _ := store.Nodes(ctx, graphstore.Query{})
			id := func(qn string) model.NodeId {
				for _, n := range nodes {
					if n.QualifiedName() == qn {
						return n.ID()
					}
				}
				t.Fatalf("symbol %q not found among %d nodes", qn, len(nodes))
				return ""
			}
			svc := query.New(store)
			callees, err := svc.Callees(ctx, id(tc.fromQN))
			if err != nil {
				t.Fatal(err)
			}
			if !containsNode(callees, id(tc.toQN)) {
				t.Errorf("[%s] Callees(%s) missing cross-file %s: %+v", tc.name, tc.fromQN, tc.toQN, callees.Nodes)
			}
			// The cross-file edge is heuristic and targets a committed node.
			edges, _ := store.Edges(ctx, graphstore.Query{EdgeKind: "calls"})
			for _, e := range edges {
				if e.From() == id(tc.fromQN) && e.To() == id(tc.toQN) {
					if e.Tier() != model.TierHeuristic {
						t.Errorf("[%s] cross-file edge tier=%q want heuristic", tc.name, e.Tier())
					}
					if len(e.Evidence()) == 0 {
						t.Errorf("[%s] cross-file edge has no evidence", tc.name)
					}
				}
				if _, err := store.GetNode(ctx, e.To()); err != nil {
					t.Errorf("[%s] calls edge to absent target %s", tc.name, e.To())
				}
			}
		})

		t.Run(tc.name+"_golden", func(t *testing.T) {
			ctx := context.Background()
			storeInc := graphstore.NewMemStore()
			t.Cleanup(func() { _ = storeInc.Close() })
			iInc := newIngester(t, storeInc, parse.NewDefaultRegistry())
			repo := writeRepo(t, tc.initial)
			if err := iInc.IngestAll(ctx, repo); err != nil {
				t.Fatalf("[%s] inc IngestAll: %v", tc.name, err)
			}
			for rel, content := range tc.mutate {
				mustWrite(t, repo, rel, content)
			}
			if tc.deletePath != "" {
				_ = os.Remove(filepath.Join(repo, tc.deletePath))
			}
			if err := iInc.IngestChanged(ctx, repo, tc.changed); err != nil {
				t.Fatalf("[%s] incremental: %v", tc.name, err)
			}
			incSnap := filepath.Join(t.TempDir(), "inc")
			if err := storeInc.Snapshot(ctx, incSnap); err != nil {
				t.Fatalf("[%s] inc snapshot: %v", tc.name, err)
			}
			incBytes, _ := os.ReadFile(incSnap)
			fullBytes := fullSnapshotBytes(t, tc.fullMutated)
			if !bytes.Equal(incBytes, fullBytes) {
				t.Fatalf("[%s] incremental != full (byte-level):\ninc =%s\nfull=%s", tc.name, incBytes, fullBytes)
			}
		})
	}
}
