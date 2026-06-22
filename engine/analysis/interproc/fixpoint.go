package interproc

import (
	"context"
	"fmt"
	"sort"
)

// DefaultWideningThreshold is the number of fixpoint iterations within an SCC
// before the widening operator is applied to force convergence on recursive
// procedures. After this many iterations without a fixpoint, the solver widens
// the abstract state to top (all input labels pass through), which guarantees
// termination at the cost of precision.
const DefaultWideningThreshold = 3

// ProcBody describes the intraprocedural transfer function for a single
// procedure, extracted from the call graph and data-flow edges. The solver
// invokes Transfer to compute the output labels given an input state and the
// current summaries of callees.
type ProcBody struct {
	// ID is the procedure identifier (graph node ID).
	ID string
	// Callees is the sorted list of procedures this procedure calls.
	Callees []string
	// InputLabels is the initial abstract input state for this procedure —
	// the set of labels reaching its formal parameters.
	InputLabels []string
	// Transfer computes the output labels given the input labels and a
	// function that looks up the current summary of any callee. If transfer
	// is nil, the default identity transfer is used (output = input).
	Transfer func(input []string, calleeSummary func(string) Summary) []string
}

// FixpointSolver is the worklist-based iterative fixpoint engine. It processes
// SCCs in the order provided (expected: reverse topological from TarjanSCC),
// iterates within each SCC until summaries stabilize, and applies widening
// after the configurable threshold.
type FixpointSolver struct {
	Caps              Caps
	WideningThreshold int
	Cache             *SummaryCache
}

// NewFixpointSolver creates a solver with the given caps, widening threshold,
// and summary cache. If wideningThreshold <= 0, DefaultWideningThreshold is
// used. If cache is nil, an unbounded cache is created.
func NewFixpointSolver(caps Caps, wideningThreshold int, cache *SummaryCache) *FixpointSolver {
	if wideningThreshold <= 0 {
		wideningThreshold = DefaultWideningThreshold
	}
	if cache == nil {
		cache = NewSummaryCache(caps.MaxSummaryEntries)
	}
	return &FixpointSolver{
		Caps:              caps,
		WideningThreshold: wideningThreshold,
		Cache:             cache,
	}
}

// SolveResult is the output of a full interprocedural fixpoint computation.
type SolveResult struct {
	// Summaries maps procedure ID to its computed summary.
	Summaries map[string]Summary
	// SCCs is the list of SCCs in reverse topological order.
	SCCs []SCC
	// Diagnostics collects cap-hit messages and other solver diagnostics.
	Diagnostics []string
	// CacheStats is a snapshot of the summary cache statistics at completion.
	CacheStats CacheStats
}

// Solve runs the interprocedural fixpoint over the given procedures and SCCs.
// Procedures are provided as a map from ID to ProcBody; SCCs must be in reverse
// topological order (callees before callers) as returned by TarjanSCC.
func (s *FixpointSolver) Solve(ctx context.Context, procs map[string]ProcBody, sccs []SCC) (SolveResult, error) {
	result := SolveResult{
		Summaries: make(map[string]Summary),
		SCCs:      sccs,
	}
	totalWork := 0

	// Check procedure-count cap.
	if hit, exceeded := s.Caps.checkProcedures(len(procs)); exceeded {
		result.Diagnostics = append(result.Diagnostics,
			fmt.Sprintf("cap exceeded: %s", hit))
		// Still proceed but mark all summaries as approximate.
	}

	for _, scc := range sccs {
		select {
		case <-ctx.Done():
			return result, ctx.Err()
		default:
		}

		// Check SCC size cap.
		if hit, exceeded := s.Caps.checkSCCSize(len(scc)); exceeded {
			result.Diagnostics = append(result.Diagnostics,
				fmt.Sprintf("cap exceeded for SCC %v: %s", scc, hit))
			// Conservative over-approximation: identity summaries for all
			// procedures in the oversized SCC.
			for _, procID := range scc {
				body, ok := procs[procID]
				if !ok {
					continue
				}
				summary := s.makeApproximateSummary(procID, body.InputLabels, 0)
				result.Summaries[procID] = summary
				key := ContentKey(procID, body.InputLabels)
				s.Cache.Put(key, summary)
			}
			continue
		}

		// Iterate within the SCC until fixpoint or cap.
		changed := true
		iteration := 0
		for changed {
			select {
			case <-ctx.Done():
				return result, ctx.Err()
			default:
			}

			iteration++
			changed = false

			// Check iteration cap.
			if hit, exceeded := s.Caps.checkIterations(iteration); exceeded {
				result.Diagnostics = append(result.Diagnostics,
					fmt.Sprintf("cap exceeded for SCC %v: %s", scc, hit))
				// Mark remaining summaries as approximate.
				for _, procID := range scc {
					if existing, ok := result.Summaries[procID]; ok {
						existing.Approximate = true
						result.Summaries[procID] = existing
					}
				}
				break
			}

			// Apply widening after threshold iterations.
			widening := iteration > s.WideningThreshold

			for _, procID := range scc {
				body, ok := procs[procID]
				if !ok {
					continue
				}

				totalWork++
				// Check total work cap.
				if hit, exceeded := s.Caps.checkTotalWork(totalWork); exceeded {
					result.Diagnostics = append(result.Diagnostics,
						fmt.Sprintf("cap exceeded: %s", hit))
					result.CacheStats = s.Cache.Stats()
					return result, nil
				}

				// Check content-addressed cache first.
				key := ContentKey(procID, body.InputLabels)
				if cached, ok := s.Cache.Get(key); ok {
					s.Cache.RecordHit()
					if _, exists := result.Summaries[procID]; !exists {
						result.Summaries[procID] = cached
					}
					continue
				}
				s.Cache.RecordMiss()

				// Compute the summary via the transfer function.
				var outputLabels []string
				if widening {
					// Widening: conservatively pass all input labels through
					// (identity / top). This guarantees termination.
					outputLabels = make([]string, len(body.InputLabels))
					copy(outputLabels, body.InputLabels)
				} else if body.Transfer != nil {
					calleeLookup := func(calleeID string) Summary {
						if s, ok := result.Summaries[calleeID]; ok {
							return s
						}
						return Summary{ProcID: calleeID, OutputLabels: nil}
					}
					outputLabels = body.Transfer(body.InputLabels, calleeLookup)
				} else {
					// Default identity transfer: all input labels reach output.
					outputLabels = make([]string, len(body.InputLabels))
					copy(outputLabels, body.InputLabels)
				}

				sort.Strings(outputLabels)

				newSummary := Summary{
					ProcID:       procID,
					InputHash:    key,
					InputLabels:  body.InputLabels,
					OutputLabels: outputLabels,
					Approximate:  widening,
					Iterations:   iteration,
				}

				// Check if summary changed from previous iteration.
				if prev, exists := result.Summaries[procID]; exists {
					if !prev.Equal(newSummary) {
						changed = true
					}
				} else {
					changed = true
				}

				result.Summaries[procID] = newSummary

				// Check summary entries cap before caching.
				if hit, exceeded := s.Caps.checkSummaryEntries(s.Cache.Stats().Size + 1); exceeded {
					result.Diagnostics = append(result.Diagnostics,
						fmt.Sprintf("cap exceeded: %s", hit))
				} else {
					s.Cache.Put(key, newSummary)
				}
			}
		}
	}

	result.CacheStats = s.Cache.Stats()
	return result, nil
}

// makeApproximateSummary creates a conservative identity summary (all input
// labels pass through) marked as approximate.
func (s *FixpointSolver) makeApproximateSummary(procID string, inputLabels []string, iterations int) Summary {
	output := make([]string, len(inputLabels))
	copy(output, inputLabels)
	sort.Strings(output)
	return Summary{
		ProcID:       procID,
		InputHash:    ContentKey(procID, inputLabels),
		InputLabels:  inputLabels,
		OutputLabels: output,
		Approximate:  true,
		Iterations:   iterations,
	}
}
