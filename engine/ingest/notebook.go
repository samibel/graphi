package ingest

// Notebook (.ipynb) ingestion adapter (SW-100, EP-017 G11).
//
// Layering: this is an ENGINE-layer decorator over the core/parse Registry. It
// preserves the CI-enforced pure-leaf boundary of core/parse — that package is
// NOT edited; the adapter reuses its per-language parsers via ParserForLang. The
// decorator implements the same Parse(ctx, path, src) shape the Ingester already
// drives once per file: a `.ipynb` path is decoded + extracted + re-dispatched to
// the matching core parser; every other extension delegates straight through.
//
// Determinism: cells are processed strictly in nbformat array order, language
// resolution is pure (explicit precedence, no map iteration), the synthetic
// per-language buffer uses a fixed single-newline concatenation policy, and the
// per-language buffers are visited in sorted order. No wall-clock / PID /
// map-iteration leakage enters the produced graph, so a full ingest and an
// incremental re-ingest of an unchanged notebook are byte-identical.
//
// Security: CGo-free (stdlib encoding/json only), zero-egress, no eval/exec/shell.

import (
	"context"
	"encoding/json"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/samibel/graphi/core/model"
	"github.com/samibel/graphi/core/parse"
)

// notebookExt is the Jupyter notebook file extension this adapter intercepts.
const notebookExt = ".ipynb"

// notebookLanguageTag is the Meta.Language tag used when a notebook's kernel
// language cannot be resolved (or has no registered parser). It is intentionally
// not a real source language, so the linker treats such a result as non-linkable.
const notebookLanguageTag = "notebook"

// NotebookCellEdgeKind is the edge kind that carries per-symbol notebook cell
// provenance into the COMMITTED graph. For every symbol extracted from a code
// cell, the adapter emits exactly one edge from the symbol node to the owning
// notebook file node tagged with this kind; its evidence encodes the source cell
// index, the nbformat >=4.5 cell id (when present), and the 1-based intra-cell
// line. The kind is deliberately distinct from the call/reference/define/import
// vocabulary, so the query-layer graph traversals (which each filter by their own
// kind) never observe it and surfaces stay notebook-agnostic (AC6). This is the
// layer-legal channel that delivers AC1's "(cell index, cell id)" pair into the
// graph without editing the pure-leaf core/parse or the core/model Node (which
// has no metadata slot): provenance rides an Edge, the only graph element with a
// free-form, fully-provenanced evidence carrier.
const NotebookCellEdgeKind = "notebook_cell"

// cellProvenanceReason is the fixed provenance reason on every cell-provenance
// edge (kept constant so equal provenance serializes to identical bytes).
const cellProvenanceReason = "notebook cell provenance"

// NotebookParser is the engine-layer decorator that adds `.ipynb` support over a
// core/parse Registry without editing the pure-leaf parse package. It is safe for
// concurrent use: it holds only the (concurrency-safe) Registry and no mutable
// per-parse state.
type NotebookParser struct {
	reg *parse.Registry
}

// NewNotebookParser wraps reg so `.ipynb` files are decoded into per-language
// synthetic buffers and re-parsed through the registry's per-language parsers,
// while every other extension delegates straight to reg. Passing a nil registry
// yields a parser that degrades every file to a no-op result.
func NewNotebookParser(reg *parse.Registry) *NotebookParser {
	return &NotebookParser{reg: reg}
}

// Parse implements the ingest Parser interface. `.ipynb` paths are handled as
// notebooks (graceful no-op on any malformed input, never an error); all other
// paths delegate to the wrapped registry unchanged.
func (p *NotebookParser) Parse(ctx context.Context, path string, src []byte) (*parse.ParseResult, error) {
	if !strings.EqualFold(filepath.Ext(path), notebookExt) {
		if p.reg == nil {
			return nil, parse.ErrNoParser
		}
		return p.reg.Parse(ctx, path, src)
	}
	return p.parseNotebook(ctx, path, src), nil
}

// cellProvenanceEvidence renders the single canonical evidence string for a
// notebook-cell provenance edge:
//
//	"<path>#cell=<index>[;id=<id>];line=<intraLine>"
//
// The 0-based cell array index is always present; the id segment is included only
// for nbformat >=4.5 cells that carry one. The pair (cell index, cell id) plus the
// 1-based intra-cell line is exactly AC1's "tagged with its source cell index (and
// cell id when present)" — now persisted on the committed edge rather than thrown
// away. The format is pure-string and deterministic.
func cellProvenanceEvidence(path string, index int, id string, intraLine int) string {
	var b strings.Builder
	b.WriteString(path)
	b.WriteString("#cell=")
	b.WriteString(strconv.Itoa(index))
	if id != "" {
		b.WriteString(";id=")
		b.WriteString(id)
	}
	b.WriteString(";line=")
	b.WriteString(strconv.Itoa(intraLine))
	return b.String()
}

// --- nbformat decoding (stdlib encoding/json; defensive against missing fields) ---

type nbNotebook struct {
	Cells    []nbCell   `json:"cells"`
	Metadata nbMetadata `json:"metadata"`
}

type nbMetadata struct {
	Kernelspec   nbKernelspec   `json:"kernelspec"`
	LanguageInfo nbLanguageInfo `json:"language_info"`
}

type nbKernelspec struct {
	Language string `json:"language"`
}

type nbLanguageInfo struct {
	Name string `json:"name"`
}

type nbCell struct {
	CellType string          `json:"cell_type"`
	Source   json.RawMessage `json:"source"`
	ID       string          `json:"id"`
	Metadata nbCellMetadata  `json:"metadata"`
}

type nbCellMetadata struct {
	Vscode nbVscode `json:"vscode"`
}

type nbVscode struct {
	LanguageID string `json:"languageId"`
}

// decodeNotebook parses the nbformat JSON envelope. A decode error (invalid JSON)
// is surfaced so the caller degrades the whole file to a file-node-only result.
func decodeNotebook(src []byte) (*nbNotebook, error) {
	var nb nbNotebook
	if err := json.Unmarshal(src, &nb); err != nil {
		return nil, err
	}
	return &nb, nil
}

// --- language resolution (pure; explicit precedence; no map iteration) ---

// resolveNotebookLanguage reads the notebook-level kernel language with the fixed
// precedence kernelspec.language -> language_info.name.
func resolveNotebookLanguage(nb *nbNotebook) string {
	if l := normalizeLang(nb.Metadata.Kernelspec.Language); l != "" {
		return l
	}
	return normalizeLang(nb.Metadata.LanguageInfo.Name)
}

// resolveCellLanguage applies the per-cell vscode override on top of the
// notebook-level language.
func resolveCellLanguage(cell nbCell, nbLang string) string {
	if l := normalizeLang(cell.Metadata.Vscode.LanguageID); l != "" {
		return l
	}
	return nbLang
}

// normalizeLang lowercases/trims a language identifier and folds the common
// Jupyter aliases onto graphi's canonical registry language ids. The switch is
// explicit (no map iteration) so resolution is deterministic.
func normalizeLang(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	switch s {
	case "ipython", "ipython3", "python2", "python3":
		return "python"
	case "node", "nodejs":
		return "javascript"
	case "ts":
		return "typescript"
	default:
		return s
	}
}

// --- synthetic per-language buffer + position remap ---

// cellSeg records where one code cell begins in its language's synthetic buffer
// (1-based line) together with its nbformat array index and stable id, forming
// the synthetic-line -> (cell index, cell id, intra-cell line) remap basis.
type cellSeg struct {
	startLine int
	index     int
	id        string
}

// langBuf accumulates the synthetic source buffer for one language plus its
// ordered cell segments.
type langBuf struct {
	b         strings.Builder
	lineCount int // number of '\n' currently written (== last line index)
	segs      []cellSeg
}

// buildCellPlan walks cells in array order and accumulates one synthetic buffer
// per resolved language. Every cell advances the index counter (so code-cell
// indices stay stable across markdown/raw interleaving); only code cells with a
// resolvable language and a well-formed source contribute to a buffer. The fixed
// concatenation policy appends each cell verbatim and guarantees exactly one
// trailing newline between cells (no language-affecting boundary tokens).
func buildCellPlan(nb *nbNotebook, nbLang string) map[string]*langBuf {
	bufs := make(map[string]*langBuf)
	for idx, cell := range nb.Cells {
		if cell.CellType != "code" {
			continue // markdown / raw / unknown: no symbols, index still advances
		}
		lang := resolveCellLanguage(cell, nbLang)
		if lang == "" {
			continue // unresolved language: skip cell, never fatal
		}
		text, ok := cellSourceText(cell.Source)
		if !ok {
			continue // missing / malformed source: skip cell, never fatal
		}
		lb := bufs[lang]
		if lb == nil {
			lb = &langBuf{}
			bufs[lang] = lb
		}
		startLine := lb.lineCount + 1
		added := strings.Count(text, "\n")
		lb.b.WriteString(text)
		if !strings.HasSuffix(text, "\n") {
			lb.b.WriteByte('\n')
			added++ // count the synthesized boundary newline
		}
		// Track the line count incrementally (F3): each cell contributes only the
		// newlines it (or the boundary) adds, avoiding an O(n^2) rescan of the whole
		// growing buffer once per cell.
		lb.lineCount += added
		lb.segs = append(lb.segs, cellSeg{startLine: startLine, index: idx, id: cell.ID})
	}
	return bufs
}

// cellSourceText materializes an nbformat `source` field, which is either a JSON
// string or an array of line-strings (joined verbatim — nbformat lines already
// carry their own newline). A missing/unparseable source returns ok=false so the
// cell degrades to a no-op.
func cellSourceText(raw json.RawMessage) (string, bool) {
	if len(raw) == 0 {
		return "", false
	}
	var arr []string
	if err := json.Unmarshal(raw, &arr); err == nil {
		return strings.Join(arr, ""), true
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s, true
	}
	return "", false
}

// remapLine translates a 1-based synthetic-buffer line to its owning cell (array
// index, stable id) and the 1-based intra-cell line. segs must be ascending by
// startLine (buildCellPlan emits them in order).
func remapLine(segs []cellSeg, synthLine int) (index int, id string, intraLine int) {
	sel := -1
	for i := range segs {
		if segs[i].startLine <= synthLine {
			sel = i
			continue
		}
		break
	}
	if sel < 0 {
		return -1, "", synthLine
	}
	s := segs[sel]
	return s.index, s.id, synthLine - s.startLine + 1
}

// --- notebook parse assembly ---

// parseNotebook is the graceful core: it never returns an error. On any malformed
// input (invalid JSON, no contributing cells, a language with no registered
// parser, a sub-parse failure) it falls back to a file-node-only result so the
// notebook file node is still registered and ingestion never aborts.
func (p *NotebookParser) parseNotebook(ctx context.Context, path string, src []byte) *parse.ParseResult {
	meta := parse.SourceMeta{
		Path:        path,
		Language:    notebookLanguageTag,
		ContentHash: hashBytes(src),
		Size:        len(src),
	}

	nb, err := decodeNotebook(src)
	if err != nil || nb == nil {
		return fileNodeResult(meta, path)
	}

	nbLang := resolveNotebookLanguage(nb)
	if nbLang != "" {
		meta.Language = nbLang
	}

	bufs := buildCellPlan(nb, nbLang)
	if len(bufs) == 0 {
		return fileNodeResult(meta, path)
	}

	langs := make([]string, 0, len(bufs))
	for lang := range bufs {
		langs = append(langs, lang)
	}
	sort.Strings(langs) // deterministic language processing order

	out := &parse.ParseResult{Meta: meta}
	nodeSeen := make(map[model.NodeId]struct{})
	edgeSeen := make(map[model.EdgeId]struct{})

	// fileID is the identity of the notebook file node every sub-parse emits
	// (Kind=file, QN=SourcePath=path). It is the deterministic anchor each
	// cell-provenance edge points at. Computed independently here so the edge
	// endpoint is known before the (deduped) file node is appended below; the file
	// node IS committed (graphstore PutEdge requires both endpoints to exist).
	fileNode, _ := model.NewNode(parse.KindFile, path, path, 1, 1)
	fileID := fileNode.ID()

	for _, lang := range langs {
		if p.reg == nil {
			break
		}
		parser, perr := p.reg.ParserForLang(lang)
		if perr != nil {
			continue // unsupported cell language: graceful skip
		}
		lb := bufs[lang]
		sub, serr := parser.Parse(ctx, path, []byte(lb.b.String()))
		if serr != nil || sub == nil {
			continue // sub-parse failure: graceful skip for this language
		}
		for _, n := range sub.Nodes {
			rn := remapNode(n, lb.segs)
			if _, dup := nodeSeen[rn.ID()]; dup {
				continue
			}
			nodeSeen[rn.ID()] = struct{}{}
			out.Nodes = append(out.Nodes, rn)
			if n.Kind() == parse.KindFile {
				continue // the notebook file node represents the whole notebook, not a cell
			}
			// Deliver AC1 cell provenance into the committed graph: one edge from
			// the symbol to the notebook file node, tagged with the (cell index,
			// cell id) pair and the intra-cell line. n.Line() is still the synthetic
			// buffer line here, so remapLine recovers the owning cell + intra line.
			ci, cid, intra := remapLine(lb.segs, n.Line())
			pe, eerr := model.NewEdge(
				rn.ID(), fileID, NotebookCellEdgeKind,
				model.TierConfirmed, 1.0, cellProvenanceReason,
				[]string{cellProvenanceEvidence(path, ci, cid, intra)},
			)
			if eerr != nil {
				continue // never fatal: provenance is best-effort, extraction still ships
			}
			if _, dup := edgeSeen[pe.ID()]; dup {
				continue
			}
			edgeSeen[pe.ID()] = struct{}{}
			out.Edges = append(out.Edges, pe)
		}
		for _, e := range sub.Edges {
			re := remapEdge(e, path, lb.segs)
			if _, dup := edgeSeen[re.ID()]; dup {
				continue
			}
			edgeSeen[re.ID()] = struct{}{}
			out.Edges = append(out.Edges, re)
		}
		for _, pr := range sub.PendingRefs {
			_, _, intra := remapLine(lb.segs, pr.Line)
			pr.Line = intra
			out.PendingRefs = append(out.PendingRefs, pr)
		}
		out.Imports = append(out.Imports, sub.Imports...)
		out.References = append(out.References, sub.References...)
	}

	if len(out.Nodes) == 0 {
		return fileNodeResult(meta, path)
	}
	return out
}

// fileNodeResult returns an empty-but-valid result carrying only the notebook
// file node, so the notebook is still registered as a graph node even when no
// cell could be extracted (graceful degradation).
func fileNodeResult(meta parse.SourceMeta, path string) *parse.ParseResult {
	res := &parse.ParseResult{Meta: meta}
	if fn, err := model.NewNode(parse.KindFile, path, path, 1, 1); err == nil {
		res.Nodes = []model.Node{fn}
	}
	return res
}

// remapNode rebuilds n with its line translated from the synthetic buffer to the
// intra-cell line. Line is a non-identity attribute, so the NodeId is unchanged
// (the remap never alters graph identity, only provenance position).
func remapNode(n model.Node, segs []cellSeg) model.Node {
	_, _, intra := remapLine(segs, n.Line())
	rn, err := model.NewNode(n.Kind(), n.QualifiedName(), n.SourcePath(), intra, n.Column())
	if err != nil {
		return n
	}
	return rn
}

// remapEdge rebuilds e with any "<path>:<synthetic-line>" evidence translated to
// "<path>:<intra-cell-line>". Evidence is not part of edge identity, so the
// EdgeId is unchanged. Only evidence whose prefix is exactly the notebook path is
// touched; any other evidence is preserved verbatim.
func remapEdge(e model.Edge, path string, segs []cellSeg) model.Edge {
	ev := e.Evidence()
	prefix := path + ":"
	remapped := make([]string, len(ev))
	changed := false
	for i, s := range ev {
		if line, ok := parseEvidenceLine(s, prefix); ok {
			_, _, intra := remapLine(segs, line)
			remapped[i] = prefix + strconv.Itoa(intra)
			changed = true
		} else {
			remapped[i] = s
		}
	}
	if !changed {
		return e
	}
	re, err := model.NewEdge(e.From(), e.To(), e.Kind(), e.Tier(), e.Confidence(), e.Reason(), remapped)
	if err != nil {
		return e
	}
	return re
}

// parseEvidenceLine extracts the trailing integer line of a "<prefix><int>"
// evidence string, returning ok=false when the shape does not match.
func parseEvidenceLine(s, prefix string) (int, bool) {
	if !strings.HasPrefix(s, prefix) {
		return 0, false
	}
	n, err := strconv.Atoi(s[len(prefix):])
	if err != nil {
		return 0, false
	}
	return n, true
}
