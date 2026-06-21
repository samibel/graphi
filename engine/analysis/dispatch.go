package analysis

import (
	"context"
	"fmt"

	"github.com/samibel/graphi/engine/query"
)

// Service is graphi's single shared analysis dispatch service. It holds a
// read-only graph Reader and a Registry of analyzers, and exposes one Dispatch
// entry point — the ONLY place a named analyzer is routed — so surfaces hold no
// analysis logic of their own and can never diverge (parity by construction,
// mirroring query.Service.Dispatch). It is safe for concurrent use.
type Service struct {
	reader query.Reader
	reg    *Registry
}

// NewService constructs a Service over the given read-only Reader and Registry.
func NewService(reader query.Reader, reg *Registry) *Service {
	if reg == nil {
		reg = NewRegistry()
	}
	return &Service{reader: reader, reg: reg}
}

// NewDefaultService constructs a Service pre-registered with the built-in
// analyzers (impact in SW-022; sibling analyzers add themselves in their own
// stories). It is the convenience constructor surfaces use.
func NewDefaultService(reader query.Reader) *Service {
	reg := NewRegistry()
	// The built-in analyzer registration is best-effort at construction; a
	// duplicate-name error here would indicate a programming fault, so it panics
	// to surface the bug immediately rather than silently dropping an analyzer.
	mustRegister(reg, impactAnalyzer{}, callchainAnalyzer{}, metricsAnalyzer{})
	if s, ok := reader.(Searcher); ok {
		mustRegister(reg, conceptAnalyzer{searcher: s})
	}
	mustRegister(reg, batchedAnalyzer{
		impact:    impactAnalyzer{},
		callChain: callchainAnalyzer{},
		metrics:   metricsAnalyzer{},
	})
	return NewService(reader, reg)
}

func mustRegister(r *Registry, as ...Analyzer) {
	for _, a := range as {
		if err := r.Register(a); err != nil {
			panic(fmt.Sprintf("analysis: default registration: %v", err))
		}
	}
}

// Dispatch routes a named analyzer to its Analyze method with the given params.
// It is the single entry point both the CLI and MCP surfaces call. An unknown
// analyzer name is a caller error (returned as an error), distinct from an
// unresolved symbol (a typed not-found Analysis, never an error).
func (s *Service) Dispatch(ctx context.Context, name string, p Params) (Analysis, error) {
	a, ok := s.reg.Get(name)
	if !ok {
		return Analysis{}, fmt.Errorf("analysis: unknown analyzer %q (want one of %v)", name, s.reg.Names())
	}
	return a.Analyze(ctx, s.reader, p)
}

// Names returns the sorted list of registered analyzer names (delegates to the
// Registry so surfaces can advertise the available analyzers).
func (s *Service) Names() []string { return s.reg.Names() }

// Reader returns the read-only graph reader the service dispatches against.
// Exposed so callers (and tests) can build additional read-only services over
// the same store.
func (s *Service) Reader() query.Reader { return s.reader }
