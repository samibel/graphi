package parse

import (
	"errors"
	"fmt"
	"sync/atomic"

	gts "github.com/odvcencio/gotreesitter"
)

// pkgMaxParseDepth is the package-level fail-closed CST nesting bound every
// gotreesitter extractor reads at newCSTWalk time. It defaults to
// DefaultResourceBounds().MaxDepth and may be overridden process-wide via
// SetMaxParseDepth (e.g. by the ingest boundary applying its configured
// ResourceBounds). It is an atomic so concurrent parses observe a consistent value
// without locking the hot path.
var pkgMaxParseDepth atomic.Int64

func init() { pkgMaxParseDepth.Store(int64(DefaultResourceBounds().MaxDepth)) }

// SetMaxParseDepth sets the process-wide fail-closed CST nesting bound applied to
// untrusted inputs by every gotreesitter extractor. A value <= 0 disables the
// bound (unbounded). It returns the previous value. The ingest boundary calls this
// to apply its configured ResourceBounds.MaxDepth to the parse path.
func SetMaxParseDepth(d int) int {
	prev := pkgMaxParseDepth.Swap(int64(d))
	return int(prev)
}

// maxParseDepth returns the current process-wide parse-depth bound (0 = unbounded).
func maxParseDepth() int {
	d := pkgMaxParseDepth.Load()
	if d < 0 {
		return 0
	}
	return int(d)
}

// ResourceBounds is the SINGLE SOURCE OF TRUTH for parse-time resource limits
// applied fail-closed to untrusted inputs (SW-055 AC#6). It is introduced by this
// slice — no parse-time ingest guard existed to reuse (the engine/analysis Caps are
// POST-parse graph caps, not input bounds). It is intentionally a small, copyable
// value type designed to be REUSABLE: SW-056 wires the same type into the
// graphi-broad (CGO) parse path without redefining it.
//
// All three bounds FAIL CLOSED: on any breach the offending file is skipped with a
// structured diagnostic and ingestion continues — never parse-anyway, never
// silently truncate.
type ResourceBounds struct {
	// MaxFileSize is the largest source file (in bytes) the parser will accept.
	// A file whose size exceeds this is rejected BEFORE its bytes are read into
	// memory (checked against FileInfo.Size() at the ingest boundary). 0 disables
	// the size bound.
	MaxFileSize int64

	// ParseTimeout bounds the wall-clock time a single Parse call may take. It is
	// applied via context.WithTimeout on the Parse ctx; on expiry the parse is
	// abandoned fail-closed. 0 disables the timeout bound.
	ParseTimeout int64 // nanoseconds (time.Duration); kept as int64 to avoid a time import here

	// MaxDepth bounds the recursion/nesting depth the CST walk may descend to,
	// guarding against deeply-nested (billion-laughs-style) inputs that would
	// otherwise overflow the stack. On breach the walk stops fail-closed and the
	// file is skipped. 0 disables the depth bound.
	MaxDepth int
}

// DefaultResourceBounds returns conservative, fail-closed default bounds for the
// default tier. The values are deliberately generous for real source files yet
// firmly cap adversarial inputs (multi-GB file, deep recursion). They are the
// single default consumed by the ingest boundary unless overridden.
func DefaultResourceBounds() ResourceBounds {
	return ResourceBounds{
		MaxFileSize:  16 << 20,              // 16 MiB — larger than any sane source file
		ParseTimeout: int64(30_000_000_000), // 30s
		MaxDepth:     2_000,                 // deep enough for real ASTs, caps pathological nesting
	}
}

// Sentinel errors for each fail-closed breach mode. Callers match with errors.Is
// to route the file to the skip-with-diagnostic path. They are typed so the ingest
// layer can distinguish a bound breach (skip + continue) from a genuine parse
// failure (also skipped, but a different diagnostic class).
var (
	// ErrFileTooLarge is returned when a file exceeds ResourceBounds.MaxFileSize.
	ErrFileTooLarge = errors.New("parse: file exceeds max size bound")
	// ErrParseTimeout is returned when a parse exceeds ResourceBounds.ParseTimeout.
	ErrParseTimeout = errors.New("parse: parse exceeded timeout bound")
	// ErrMaxDepthExceeded is returned when the CST walk exceeds ResourceBounds.MaxDepth.
	ErrMaxDepthExceeded = errors.New("parse: input exceeds max nesting depth bound")
)

// Provenance is the ONLY information a parser error/log path may carry about a
// source location (SW-055 AC#6, default-deny source sanitization). It holds
// structured provenance — file, language, byte-span, node-kind — and NEVER raw
// source bytes. Every error the parse boundary constructs for an untrusted input
// is routed through SanitizedError so a secret embedded in source can never leak
// into an error string or log line.
type Provenance struct {
	File      string // source file path (provenance, not content)
	Language  string // canonical language identifier
	ByteStart int    // 0-based start byte of the cited span (-1 if unknown)
	ByteEnd   int    // exclusive end byte of the cited span (-1 if unknown)
	NodeKind  string // grammar node kind at the span (e.g. "function_definition")
}

// SanitizedError wraps cause with ONLY the structured provenance in p — it never
// embeds raw source bytes. This is the central, default-deny error choke point:
// constructing parse errors through it guarantees no source content (and therefore
// no secret) escapes into any error/log path beyond the cited provenance span.
//
// The returned error wraps cause (errors.Is/As still work for the sentinels above)
// while its message is built solely from p's structured fields.
func SanitizedError(p Provenance, cause error) error {
	start, end := p.ByteStart, p.ByteEnd
	if start < 0 {
		start = 0
	}
	kind := p.NodeKind
	if kind == "" {
		kind = "n/a"
	}
	return &sanitizedError{
		msg: fmt.Sprintf("parse: %s [file=%s lang=%s bytes=%d-%d node=%s]",
			causeSummary(cause), p.File, p.Language, start, end, kind),
		cause: cause,
	}
}

// sanitizedError is the concrete error type returned by SanitizedError. It carries
// only the pre-rendered, source-free message plus the wrapped cause for errors.Is.
type sanitizedError struct {
	msg   string
	cause error
}

func (e *sanitizedError) Error() string { return e.msg }
func (e *sanitizedError) Unwrap() error { return e.cause }

// causeSummary renders a short, source-free summary of a cause. For the typed
// bound sentinels it uses their fixed message; for any other error it uses the
// error's own message UNCHANGED — callers MUST only pass causes that are themselves
// source-free (the sentinels, or grammar/runtime errors that do not echo input).
// This keeps the choke point honest: it does not attempt to scrub an arbitrary
// string, it requires the cause to already be provenance-only.
func causeSummary(cause error) string {
	switch {
	case cause == nil:
		return "error"
	case errors.Is(cause, ErrFileTooLarge):
		return "rejected: file too large"
	case errors.Is(cause, ErrParseTimeout):
		return "rejected: parse timed out"
	case errors.Is(cause, ErrMaxDepthExceeded):
		return "rejected: input too deeply nested"
	default:
		return cause.Error()
	}
}

// guardCSTDepth fail-closes a gotreesitter CST against deeply-nested
// (billion-laughs / stack-overflow) inputs (SW-055 AC#6). It measures the nesting
// depth of root ITERATIVELY (an explicit work stack — it never recurses, so the
// guard itself cannot overflow) and returns a SanitizedError wrapping
// ErrMaxDepthExceeded the moment maxDepth is exceeded. The error carries ONLY
// structured provenance (file/language/node-kind), never raw source. maxDepth <= 0
// disables the bound. It is the shared guard used by every gotreesitter extractor
// (the shared cstWalk and the TS reference walk alike).
func guardCSTDepth(root *gts.Node, lang *gts.Language, maxDepth int, filename, language string) error {
	if maxDepth <= 0 || root == nil {
		return nil
	}
	type frame struct {
		n     *gts.Node
		depth int
	}
	stack := []frame{{n: root, depth: 1}}
	for len(stack) > 0 {
		f := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if f.depth > maxDepth {
			return SanitizedError(Provenance{
				File:      filename,
				Language:  language,
				ByteStart: -1,
				ByteEnd:   -1,
				NodeKind:  f.n.Type(lang),
			}, ErrMaxDepthExceeded)
		}
		for i := 0; i < f.n.ChildCount(); i++ {
			if c := f.n.Child(i); c != nil {
				stack = append(stack, frame{n: c, depth: f.depth + 1})
			}
		}
	}
	return nil
}
