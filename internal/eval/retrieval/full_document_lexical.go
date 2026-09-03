package retrieval

// The full-document lexical control is deliberately implemented inside the
// evaluator. Production's graphstore FTS5 table indexes qualified names and is
// part of the shipped database contract; changing that table for an experiment
// would change shipped bytes. This private index uses the same CGo-free SQLite
// driver already used by graphstore, the same FTS5 tokenizer and BM25 rank, and
// indexes the exact admitted SemanticDocument.Text bytes handed to the static
// embedder.

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
	"strings"

	"github.com/samibel/graphi/core/model"
	"github.com/samibel/graphi/engine/embed"
)

type fullDocumentLexical struct {
	db        *sql.DB
	nodes     map[model.NodeId]model.Node
	documents []FieldParityDocument
}

type nameOnlyFTS5Control struct {
	db *sql.DB
}

type ftsQueryOperator int

const (
	ftsAllTermsPrefix ftsQueryOperator = iota
	ftsExplicitOR
)

const (
	fullDocumentFTSSchema = `CREATE VIRTUAL TABLE documents USING fts5(
		node_id UNINDEXED,
		qualified_name UNINDEXED,
		text
	)`
	fullDocumentFTSSQL = `SELECT node_id, bm25(documents)
		FROM documents
		WHERE documents MATCH ?
		ORDER BY bm25(documents) ASC, qualified_name ASC, node_id ASC`
	nameOnlyFTS5ControlSQL = `SELECT n.kind, n.qualified_name, n.source_path, n.line, n.col, n.meta, rank
FROM search s JOIN nodes n ON s.owner_id = n.id
WHERE s.owner_kind = 'node' AND s.text MATCH ?
ORDER BY rank ASC, n.qualified_name ASC, n.id ASC`
)

func newFullDocumentLexical(ctx context.Context, path string, docs []embed.SemanticDocument, nodes []model.Node) (*fullDocumentLexical, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("retrieval: open evaluator full-document FTS5 index: %w", err)
	}
	closeOnError := func(err error) (*fullDocumentLexical, error) {
		_ = db.Close()
		return nil, err
	}
	if _, err := db.ExecContext(ctx, fullDocumentFTSSchema); err != nil {
		return closeOnError(fmt.Errorf("retrieval: create evaluator full-document FTS5 index: %w", err))
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return closeOnError(fmt.Errorf("retrieval: begin evaluator full-document FTS5 index: %w", err))
	}
	stmt, err := tx.PrepareContext(ctx, "INSERT INTO documents(node_id, qualified_name, text) VALUES (?, ?, ?)")
	if err != nil {
		_ = tx.Rollback()
		return closeOnError(fmt.Errorf("retrieval: prepare evaluator full-document FTS5 index: %w", err))
	}
	for _, doc := range docs {
		if _, err := stmt.ExecContext(ctx, string(doc.NodeID), doc.QualifiedName, doc.Text); err != nil {
			_ = stmt.Close()
			_ = tx.Rollback()
			return closeOnError(fmt.Errorf("retrieval: index full v3 document for node %s: %w", doc.NodeID, err))
		}
	}
	if err := stmt.Close(); err != nil {
		_ = tx.Rollback()
		return closeOnError(fmt.Errorf("retrieval: close evaluator full-document FTS5 statement: %w", err))
	}
	if err := tx.Commit(); err != nil {
		return closeOnError(fmt.Errorf("retrieval: commit evaluator full-document FTS5 index: %w", err))
	}

	byID := make(map[model.NodeId]model.Node, len(nodes))
	for _, n := range nodes {
		byID[n.ID()] = n
	}
	provenance := make([]FieldParityDocument, 0, len(docs))
	for _, doc := range docs {
		provenance = append(provenance, FieldParityDocument{
			NodeID: string(doc.NodeID), DocumentID: doc.DocumentID,
			SemanticTextHash: doc.TextHash, TextSHA256: SHA256Hex([]byte(doc.Text)),
		})
	}
	sort.Slice(provenance, func(i, j int) bool {
		if provenance[i].NodeID != provenance[j].NodeID {
			return provenance[i].NodeID < provenance[j].NodeID
		}
		return provenance[i].DocumentID < provenance[j].DocumentID
	})
	return &fullDocumentLexical{db: db, nodes: byID, documents: provenance}, nil
}

func (f *fullDocumentLexical) close() error {
	if f == nil || f.db == nil {
		return nil
	}
	return f.db.Close()
}

func (f *fullDocumentLexical) search(ctx context.Context, text string, limit int, operator ftsQueryOperator) ([]rawHit, error) {
	if f == nil || f.db == nil {
		return nil, fmt.Errorf("retrieval: evaluator full-document FTS5 index is not configured")
	}
	if strings.TrimSpace(text) == "" {
		return nil, nil
	}
	q := fullDocumentFTSSQL
	args := []any{fts5MatchExpression(text, operator)}
	if limit > 0 {
		q += " LIMIT ?"
		args = append(args, limit)
	}
	rows, err := f.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("retrieval: evaluator full-document FTS5 search: %w", err)
	}
	defer rows.Close()

	out := make([]rawHit, 0, limit)
	for rows.Next() {
		var id model.NodeId
		var score float64
		if err := rows.Scan(&id, &score); err != nil {
			return nil, fmt.Errorf("retrieval: scan evaluator full-document FTS5 hit: %w", err)
		}
		n, ok := f.nodes[id]
		if !ok {
			return nil, fmt.Errorf("retrieval: evaluator full-document FTS5 returned unknown node %s", id)
		}
		out = append(out, rawHit{
			path: n.SourcePath(), nodeID: string(n.ID()), kind: n.Kind(),
			qn: n.QualifiedName(), line: n.Line(), bm25Score: score, hasBM25Score: true,
		})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("retrieval: iterate evaluator full-document FTS5 hits: %w", err)
	}
	return out, nil
}

func newNameOnlyFTS5Control(path string) (*nameOnlyFTS5Control, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("retrieval: open evaluator name-only FTS5 OR control: %w", err)
	}
	return &nameOnlyFTS5Control{db: db}, nil
}

func (f *nameOnlyFTS5Control) close() error {
	if f == nil || f.db == nil {
		return nil
	}
	return f.db.Close()
}

func (f *nameOnlyFTS5Control) search(ctx context.Context, text string, limit int) ([]rawHit, error) {
	if f == nil || f.db == nil {
		return nil, fmt.Errorf("retrieval: evaluator name-only FTS5 OR control is not configured")
	}
	if strings.TrimSpace(text) == "" {
		return nil, nil
	}
	q := nameOnlyFTS5ControlSQL
	args := []any{fts5MatchExpression(text, ftsExplicitOR)}
	if limit > 0 {
		q += " LIMIT ?"
		args = append(args, limit)
	}
	rows, err := f.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("retrieval: evaluator name-only FTS5 OR search: %w", err)
	}
	defer rows.Close()

	out := make([]rawHit, 0, limit)
	for rows.Next() {
		var kind, qualifiedName, sourcePath, metaJSON string
		var line, column int
		var score float64
		if err := rows.Scan(&kind, &qualifiedName, &sourcePath, &line, &column, &metaJSON, &score); err != nil {
			return nil, fmt.Errorf("retrieval: scan evaluator name-only FTS5 OR hit: %w", err)
		}
		// Match engine/search.Service.Search exactly: structural nodes are
		// discarded after the backend's bounded ranking.
		if kind == "package" || kind == "external" {
			continue
		}
		node, err := model.NewNode(kind, qualifiedName, sourcePath, line, column)
		if err != nil {
			return nil, fmt.Errorf("retrieval: reconstruct evaluator name-only FTS5 OR hit: %w", err)
		}
		out = append(out, rawHit{
			path: sourcePath, nodeID: string(node.ID()), kind: kind, qn: qualifiedName, line: line,
			bm25Score: score, hasBM25Score: true,
		})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("retrieval: iterate evaluator name-only FTS5 OR hits: %w", err)
	}
	return out, nil
}

// fts5MatchExpression's all-terms form intentionally mirrors
// core/graphstore.ftsQuery. That
// helper is private to the core package; duplicating these six lines keeps the
// evaluator layered while ensuring both lexical cells apply the same safe,
// all-token prefix query to user text. The OR control changes only the joiner.
func fts5MatchExpression(text string, operator ftsQueryOperator) string {
	fields := strings.Fields(text)
	if len(fields) == 0 {
		return `""`
	}
	quoted := make([]string, 0, len(fields))
	for _, field := range fields {
		field = strings.ReplaceAll(field, `"`, `""`)
		quoted = append(quoted, `"`+field+`"*`)
	}
	joiner := " "
	if operator == ftsExplicitOR {
		joiner = " OR "
	}
	return strings.Join(quoted, joiner)
}
