package retrieval

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"runtime"
	"runtime/debug"
	"strings"
)

const (
	fieldParityTransformSourceFile = "internal/eval/retrieval/full_document_lexical.go"
	moderncSQLiteModulePath        = "modernc.org/sqlite"
	moderncSQLiteVersion           = "v1.52.0"
	moderncSQLiteSum               = "h1:p4dhYh2tXZCiyaqHwRVJDjIGKWyXayiQpThxgDzJaxo="
	moderncSQLiteGoModSum          = "h1:tcNzv5p84E0skkmJn038y+hWJbLQXQqEnQfeh5r2JLM="

	fieldParityControlStatement = "fts5_or_control is an operator-control experiment: it changes only the MATCH expression joiner from implicit AND to explicit OR; it is NOT a CoIR-compatible reference and is not a reference implementation."
)

// FieldParityProvenance is the deterministic audit record for SW-272's
// evaluator-only operator control.
type FieldParityProvenance struct {
	ControlStatement string                    `json:"control_statement"`
	CorpusSHA        string                    `json:"corpus_sha"`
	DatasetSHA256    string                    `json:"dataset_sha256"`
	QueryTransform   FieldParityQueryTransform `json:"query_transform"`
	SQLite           FieldParitySQLite         `json:"sqlite"`
	Queries          []FieldParityQuery        `json:"queries"`
	NameOnly         FieldParityCorpus         `json:"qualified_name_only"`
	FullDocument     FieldParityCorpus         `json:"full_v3_document"`
}

type FieldParityQueryTransform struct {
	SourceFile        string `json:"source_file"`
	SourceSHA256      string `json:"source_sha256"`
	FrozenAtCommit    string `json:"frozen_at_commit"`
	FrozenAtCandidate string `json:"frozen_at_candidate"`
}

type FieldParitySQLite struct {
	DriverName     string `json:"driver_name"`
	ModulePath     string `json:"module_path"`
	ModuleVersion  string `json:"module_version"`
	ModuleSum      string `json:"module_sum"`
	GoModSum       string `json:"go_mod_sum"`
	RuntimeVersion string `json:"runtime_version"`
}

type FieldParityQuery struct {
	ID             string `json:"id"`
	Text           string `json:"text"`
	AllTermsPrefix string `json:"all_terms_prefix_match"`
	ExplicitOR     string `json:"explicit_or_match"`
}

type FieldParityCorpus struct {
	Representation       string                `json:"representation"`
	Table                string                `json:"table"`
	Schema               string                `json:"schema"`
	TokenizerDeclaration string                `json:"tokenizer_declaration"`
	AllTermsQuerySQL     string                `json:"all_terms_query_sql"`
	QuerySQL             string                `json:"or_control_query_sql"`
	Documents            []FieldParityDocument `json:"ordered_documents"`
}

type FieldParityDocument struct {
	NodeID           string `json:"node_id"`
	DocumentID       string `json:"document_id,omitempty"`
	SemanticTextHash string `json:"semantic_text_hash,omitempty"`
	TextSHA256       string `json:"text_sha256"`
}

func buildFieldParityProvenance(ctx context.Context, idx *index, ds *Dataset, datasetSHA, corpusSHA, candidate string) (*FieldParityProvenance, error) {
	if idx == nil || idx.nameOnlyFTS5Control == nil || idx.fullDocumentLexical == nil {
		return nil, fmt.Errorf("retrieval: complete field-parity controls are not configured")
	}
	sourceSHA, err := queryTransformSourceSHA256()
	if err != nil {
		return nil, err
	}
	nameSchema, err := sqliteSchema(ctx, idx.nameOnlyFTS5Control.db, "search")
	if err != nil {
		return nil, err
	}
	fullSchema, err := sqliteSchema(ctx, idx.fullDocumentLexical.db, "documents")
	if err != nil {
		return nil, err
	}
	nameDocuments, err := nameOnlyDocuments(ctx, idx.nameOnlyFTS5Control.db)
	if err != nil {
		return nil, err
	}
	runtimeVersion, err := sqliteRuntimeVersion(ctx, idx.nameOnlyFTS5Control.db)
	if err != nil {
		return nil, err
	}
	if err := checkSQLiteBuildDependency(); err != nil {
		return nil, err
	}
	queries := make([]FieldParityQuery, 0, len(ds.Queries))
	for _, query := range ds.Queries {
		queries = append(queries, FieldParityQuery{
			ID: query.ID, Text: query.Text,
			AllTermsPrefix: fts5MatchExpression(query.Text, ftsAllTermsPrefix),
			ExplicitOR:     fts5MatchExpression(query.Text, ftsExplicitOR),
		})
	}
	const tokenizer = "no tokenize= clause; both tables use SQLite FTS5's implicit default tokenizer"
	return &FieldParityProvenance{
		ControlStatement: fieldParityControlStatement,
		CorpusSHA:        corpusSHA,
		DatasetSHA256:    datasetSHA,
		QueryTransform: FieldParityQueryTransform{
			SourceFile: fieldParityTransformSourceFile, SourceSHA256: sourceSHA,
			FrozenAtCommit: strings.TrimSuffix(candidate, "+dirty"), FrozenAtCandidate: candidate,
		},
		SQLite: FieldParitySQLite{
			DriverName: "sqlite", ModulePath: moderncSQLiteModulePath,
			ModuleVersion: moderncSQLiteVersion, ModuleSum: moderncSQLiteSum,
			GoModSum: moderncSQLiteGoModSum, RuntimeVersion: runtimeVersion,
		},
		Queries: queries,
		NameOnly: FieldParityCorpus{
			Representation: "qualified name only", Table: "search", Schema: nameSchema,
			TokenizerDeclaration: tokenizer, AllTermsQuerySQL: nameOnlyFTS5ControlSQL,
			QuerySQL: nameOnlyFTS5ControlSQL, Documents: nameDocuments,
		},
		FullDocument: FieldParityCorpus{
			Representation: "exact admitted SemanticDocument v3 Text", Table: "documents", Schema: fullSchema,
			TokenizerDeclaration: tokenizer, AllTermsQuerySQL: fullDocumentFTSSQL,
			QuerySQL: fullDocumentFTSSQL, Documents: append([]FieldParityDocument(nil), idx.fullDocumentLexical.documents...),
		},
	}, nil
}

func queryTransformSourceSHA256() (string, error) {
	_, currentFile, _, ok := runtime.Caller(0)
	if !ok {
		return "", fmt.Errorf("retrieval: locate field-parity provenance source")
	}
	path := strings.TrimSuffix(currentFile, "fieldparity_provenance.go") + "full_document_lexical.go"
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("retrieval: hash field-parity query-transform source %s: %w", fieldParityTransformSourceFile, err)
	}
	return SHA256Hex(raw), nil
}

func sqliteSchema(ctx context.Context, db *sql.DB, table string) (string, error) {
	var schema string
	if err := db.QueryRowContext(ctx, "SELECT sql FROM sqlite_master WHERE type = 'table' AND name = ?", table).Scan(&schema); err != nil {
		return "", fmt.Errorf("retrieval: read FTS5 schema for %s: %w", table, err)
	}
	return schema, nil
}

func nameOnlyDocuments(ctx context.Context, db *sql.DB) ([]FieldParityDocument, error) {
	rows, err := db.QueryContext(ctx, "SELECT owner_id, text FROM search WHERE owner_kind = 'node' ORDER BY owner_id ASC")
	if err != nil {
		return nil, fmt.Errorf("retrieval: enumerate qualified-name FTS5 documents: %w", err)
	}
	defer rows.Close()
	documents := []FieldParityDocument{}
	for rows.Next() {
		var nodeID, text string
		if err := rows.Scan(&nodeID, &text); err != nil {
			return nil, fmt.Errorf("retrieval: scan qualified-name FTS5 document: %w", err)
		}
		documents = append(documents, FieldParityDocument{NodeID: nodeID, TextSHA256: SHA256Hex([]byte(text))})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("retrieval: iterate qualified-name FTS5 documents: %w", err)
	}
	return documents, nil
}

func sqliteRuntimeVersion(ctx context.Context, db *sql.DB) (string, error) {
	var version string
	if err := db.QueryRowContext(ctx, "SELECT sqlite_version()").Scan(&version); err != nil {
		return "", fmt.Errorf("retrieval: read SQLite runtime version: %w", err)
	}
	return version, nil
}

func checkSQLiteBuildDependency() error {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return nil
	}
	for _, dep := range info.Deps {
		if dep.Path != moderncSQLiteModulePath {
			continue
		}
		if dep.Version != moderncSQLiteVersion || dep.Sum != moderncSQLiteSum {
			return fmt.Errorf("retrieval: SQLite dependency is %s %s %s, want frozen %s %s %s", dep.Path, dep.Version, dep.Sum, moderncSQLiteModulePath, moderncSQLiteVersion, moderncSQLiteSum)
		}
		return nil
	}
	// Some `go test` and stripped binary modes omit dependency records from
	// ReadBuildInfo. The report still records the go.mod/go.sum pins above;
	// when build information is present, a mismatch fails closed.
	return nil
}
