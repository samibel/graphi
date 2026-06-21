package context

// Candidate is one match from the EP-001 query/search layer, normalized into the
// shape the context engine consumes. It is local to this package so the assembly
// transform stays decoupled from the exact upstream result types.
//
// StartLine/EndLine are 1-based, inclusive, and describe the match span. A point
// match (a single line) has StartLine == EndLine. The winnow step may expand
// this span by bounded context padding; the emitted Snippet's Citation reflects
// the EXPANDED span, not this raw match span.
type Candidate struct {
	Path      string  // source file path (local)
	StartLine int     // 1-based, inclusive
	EndLine   int     // 1-based, inclusive (== StartLine for a point match)
	Rank      float64 // upstream rank; better (smaller for search) first. Query-derived candidates use 0.
	Symbol    string  // optional qualified symbol name (best-effort, for callers)
	Kind      string  // optional node kind (best-effort)
}

// Citation is the exact source location of a snippet's bytes. It is produced at
// extraction time and carried VERBATIM; nothing downstream mutates it. Re-reading
// Path lines [StartLine, EndLine] (1-based, inclusive) reproduces the snippet
// text exactly (the citation round-trips to the bytes it labels).
type Citation struct {
	Path      string `json:"path"`
	StartLine int    `json:"start_line"`
	EndLine   int    `json:"end_line"`
}

// Span is a 1-based inclusive line range. It is the internal line-window type
// used by the SourceReader and the winnow step.
type Span struct {
	Start int
	End   int
}

// Snippet is one winnowed evidence fragment in a bundle. Text is exactly the
// bytes of the cited span; Tokens is the deterministic token count of Text; Rank
// is the candidate's upstream rank, preserved for traceability.
type Snippet struct {
	Citation Citation `json:"citation"`
	Text     string   `json:"text"`
	Rank     float64  `json:"rank"`
	Tokens   int      `json:"tokens"`
}

// Bundle is the canonical, surface-agnostic result of context assembly. It
// carries the originating query, the configured budget, the total tokens
// actually used (always <= Budget), the ranked included snippets, and the
// assembly method version stamp.
//
// Snippets are ordered by the deterministic total order (best rank first) and
// were included greedily until the budget was reached; the remainder was
// dropped. A bundle with Budget <= 0 contains zero snippets.
type Bundle struct {
	Query         string    `json:"query"`
	Budget        int       `json:"budget"`
	Tokens        int       `json:"tokens"`
	Snippets      []Snippet `json:"snippets"`
	MethodVersion string    `json:"method_version"`
}

// Options parameterizes assembly. Budget is the hard token ceiling (<= 0 yields
// an empty bundle). ContextLines is the bounded padding added on each side of a
// candidate's match span before winnowing (<= 0 means exact span, no padding);
// padding is clamped to file bounds.
type Options struct {
	Budget       int
	ContextLines int
}

func (o Options) withDefaults() Options {
	if o.ContextLines < 0 {
		o.ContextLines = 0
	}
	return o
}

// engineFor is the single SourceReader method the assembly depends on. It keeps
// the engine decoupled from any concrete reader (tests inject a fake; production
// uses the disk-backed LocalReader).
type engineFor interface {
	ReadSpan(path string, want Span) (text string, got Span, err error)
}
