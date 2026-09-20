package retrieval

import (
	"context"
	"testing"
)

func TestExactNamePrecedesMoreSimilarAndHigherLexicalScoringNames(t *testing.T) {
	for _, query := range []string{"BuildTree", "pkg.BuildTree"} {
		t.Run(query, func(t *testing.T) {
			e := newEngine(&fakeLexical{hits: []lexicalHit{
				{NodeID: "wrong", QualifiedName: "pkg.BuildTreeCustom", Path: "tree.go", Line: 10, Score: 2000},
				{NodeID: "exact", QualifiedName: "pkg.BuildTree", Path: "tree.go", Line: 30, Score: 1000},
			}}, &fakeSemantic{available: true, state: StateReady, hits: []semanticHit{
				{NodeID: "wrong", QualifiedName: "pkg.BuildTreeCustom", Path: "tree.go", Line: 10, CosineScore: .95},
				{NodeID: "exact", QualifiedName: "pkg.BuildTree", Path: "tree.go", Line: 30, CosineScore: .5},
			}}, nil)
			res, err := e.Retrieve(context.Background(), Request{Query: query, Limit: 1})
			if err != nil {
				t.Fatal(err)
			}
			if len(res.Rows) != 1 || res.Rows[0].NodeID != "exact" {
				t.Fatalf("exact name lost to approximate match: %+v", res.Rows)
			}
		})
	}
}
