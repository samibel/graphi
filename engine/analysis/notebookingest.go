package analysis

// SW-104 (EP-017 capstone): surface the SW-100 notebook-ingest capability as the
// canonical `notebook-ingest` operation behind the ONE dispatch table. The
// analyzer holds no ingestion logic — the notebook (.ipynb) cell provenance is
// already committed into the graph by engine/ingest.NotebookParser as
// `notebook_cell` provenance edges (SW-100); this operation reads those committed
// edges back out and reports the per-symbol cells. Determinism is enforced at the
// encoder boundary (serialize.go sortNotebookCells): cells by (source path, cell
// index, symbol). No analysis/ingestion logic lives in surfaces.

import (
	"context"
	"strconv"
	"strings"

	"github.com/samibel/graphi/core/graphstore"
	"github.com/samibel/graphi/engine/query"
)

// NotebookIngestAnalyzerName is the canonical dispatch key for the notebook
// ingest surfacing operation.
const NotebookIngestAnalyzerName = "notebook-ingest"

// notebookCellEdgeKind mirrors engine/ingest.NotebookCellEdgeKind (SW-100). It is
// duplicated here as a local const so engine/analysis need not import
// engine/ingest (keeping the analysis package lean); the value is the SW-100
// provenance-edge kind and must stay in lock-step with it.
const notebookCellEdgeKind = "notebook_cell"

// NotebookReport is the canonical, byte-stable envelope payload for the
// `notebook-ingest` operation: the per-symbol notebook cells recovered from the
// committed `notebook_cell` provenance edges, ordered by cell index at the
// encoder.
type NotebookReport struct {
	Cells []NotebookCell `json:"cells"`
}

// NotebookCell is one symbol's notebook-cell provenance: the owning notebook
// file, the 0-based nbformat cell array index, the optional nbformat >=4.5 cell
// id, the 1-based intra-cell line, and the symbol node id the cell defined.
type NotebookCell struct {
	SourcePath string `json:"source_path"`
	CellIndex  int    `json:"cell_index"`
	CellID     string `json:"cell_id,omitempty"`
	Line       int    `json:"line"`
	Symbol     string `json:"symbol"`
}

// notebookAnalyzer routes the `notebook-ingest` operation to the committed
// notebook-cell provenance in the read-only graph. It is stateless per call.
type notebookAnalyzer struct{}

// Name implements Analyzer.
func (notebookAnalyzer) Name() string { return NotebookIngestAnalyzerName }

// Analyze scans the committed `notebook_cell` provenance edges and reports the
// per-symbol cells. A graph with no notebook provenance yields an empty report
// (never an error).
func (notebookAnalyzer) Analyze(ctx context.Context, r query.Reader, p Params) (Analysis, error) {
	report := &NotebookReport{Cells: []NotebookCell{}}
	if gs, ok := r.(graphstore.Graphstore); ok {
		edges, err := gs.Edges(ctx, graphstore.Query{})
		if err != nil {
			return Analysis{}, err
		}
		for _, e := range edges {
			if e.Kind() != notebookCellEdgeKind {
				continue
			}
			ev := e.Evidence()
			if len(ev) == 0 {
				continue
			}
			path, idx, id, line, ok := parseCellEvidence(ev[0])
			if !ok {
				continue
			}
			report.Cells = append(report.Cells, NotebookCell{
				SourcePath: path,
				CellIndex:  idx,
				CellID:     id,
				Line:       line,
				Symbol:     string(e.From()),
			})
		}
	}
	outcome := query.OutcomeFound
	if len(report.Cells) == 0 {
		outcome = query.OutcomeEmpty
	}
	return Analysis{
		Analyzer: NotebookIngestAnalyzerName,
		Outcome:  outcome,
		Symbol:   p.Symbol,
		Notebook: report,
	}, nil
}

// parseCellEvidence parses the SW-100 cell-provenance evidence string, whose
// canonical form (engine/ingest.cellProvenanceEvidence) is:
//
//	"<path>#cell=<index>[;id=<id>];line=<intraLine>"
//
// It returns ok=false on any shape it does not recognize (defensive; never
// panics).
func parseCellEvidence(s string) (path string, index int, id string, line int, ok bool) {
	const marker = "#cell="
	at := strings.Index(s, marker)
	if at < 0 {
		return "", 0, "", 0, false
	}
	path = s[:at]
	rest := s[at+len(marker):]
	segs := strings.Split(rest, ";")
	if len(segs) == 0 || segs[0] == "" {
		return "", 0, "", 0, false
	}
	idx, err := strconv.Atoi(segs[0])
	if err != nil {
		return "", 0, "", 0, false
	}
	line = -1
	for _, seg := range segs[1:] {
		switch {
		case strings.HasPrefix(seg, "id="):
			id = seg[len("id="):]
		case strings.HasPrefix(seg, "line="):
			if n, lerr := strconv.Atoi(seg[len("line="):]); lerr == nil {
				line = n
			}
		}
	}
	if line < 0 {
		return "", 0, "", 0, false
	}
	return path, idx, id, line, true
}
