package client

import (
	"bytes"
	"context"
	"errors"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/samibel/graphi/core/model"
	"github.com/samibel/graphi/engine/analysis"
	"github.com/samibel/graphi/engine/query"
)

// This file is SW-239: the NEGATIVE half of the executor parity evidence.
//
// executor_parity_test.go proves that the adapter path and the legacy path
// agree. Agreement alone is not fidelity: two paths that both ignore an
// argument agree perfectly. The memory and search_semantic cases did exactly
// that until SW-239 — their optional service was unwired, so Direct returned a
// fixed document before it ever read the argument, and mutating the argument
// left the parity GREEN (backlog :864).
//
// The proof obligation here is therefore inverted. For every argument field a
// parity case carries, the field is mutated and the mutated call is compared
// against the UNMUTATED legacy baseline. A field the executor really forwards
// makes that comparison FAIL; a mutation that leaves it passing is the finding.
//
// Fields fall into exactly two classes, and both are pinned:
//
//	observable      mutating it MUST flip the parity. This is the real evidence.
//	not observable  the operation provably cannot reflect the field in its
//	                canonical response on this fixture (it belongs to a
//	                different op of the same argument struct, or the response
//	                does not echo it). Such a field carries a written reason,
//	                is asserted to leave the parity green — so that a field
//	                which LATER becomes observable fails here and must be
//	                reclassified — and still has to survive the request round
//	                trip, which is what proves the executor forwards it.
//
// TestExecutor_EveryArgumentFieldHasMutationCoverage then makes the whole thing
// mechanical: it reflects over each parity case's argument struct and fails on
// any field with no entry in the table below, so a field added later cannot
// ship unproven.

// legacyCall is the hand-written Client call a surface makes today. It is the
// independent baseline; nothing here computes a baseline through the adapter.
type legacyCall func(ctx context.Context, c Client) ([]byte, error)

// fieldMutation is one field-level mutation of one parity case's arguments.
type fieldMutation struct {
	// field is the JSON name of the argument field, or the Go field name for a
	// field carried outside the JSON payload (QueryArgs.Op is json:"-": it
	// travels as Request.Operation).
	field string
	// observable records whether mutating the field must flip the byte parity.
	observable bool
	// reason is REQUIRED when observable is false and explains why the
	// operation cannot reflect this field. It is not an excuse slot: the
	// unobservable claim is itself asserted.
	reason string
	// override replaces the parity case's base invocation for this field, for
	// fields another op of the SAME argument struct consumes. Both halves move
	// together: the arguments and the hand-written legacy call for them.
	override func(ids map[string]model.NodeId) (Arguments, legacyCall)
	// mutate changes exactly the named field to a different value.
	mutate func(a Arguments)
}

func observable(field string, mutate func(Arguments)) fieldMutation {
	return fieldMutation{field: field, observable: true, mutate: mutate}
}

// analyzeBases holds the base AnalyzeParams for each analyzer the analyze
// mutations select. One entry per analyzer keeps the hand-written legacy call
// and the executor arguments literally the same value.
func analyzeBases(ids map[string]model.NodeId) map[string]AnalyzeParams {
	return map[string]AnalyzeParams{
		// One reachable path (p.A → p.B), so re-pointing Target moves the answer.
		"call-chain": {Name: "call-chain", Symbol: string(ids["A"]), Target: string(ids["B"]), MaxPaths: 10},
		// TWO reachable paths (p.A → p.B → p.C → p.D and p.A → p.C → p.D), so a
		// MaxPaths cap of 1 observably truncates the enumeration.
		"call-chain/multipath": {Name: "call-chain", Symbol: string(ids["A"]), Target: string(ids["D"]), MaxPaths: 2},
		"concept":              {Name: "concept", Concept: "p.B"},
		analysis.PriskAnalyzerName: {
			Name:       analysis.PriskAnalyzerName,
			Diff:       "p/A.go",
			Provenance: "full",
		},
	}
}

// analyzeOverride builds a mutation of one AnalyzeParams field against the
// analyzer that actually consumes it.
func analyzeOverride(field, analyzer string, mutate func(*AnalyzeParams)) fieldMutation {
	return fieldMutation{
		field:      field,
		observable: true,
		override: func(ids map[string]model.NodeId) (Arguments, legacyCall) {
			params := analyzeBases(ids)[analyzer]
			return &AnalyzeArgs{params}, func(ctx context.Context, c Client) ([]byte, error) {
				return c.Analyze(ctx, params)
			}
		},
		mutate: func(a Arguments) { mutate(&a.(*AnalyzeArgs).AnalyzeParams) },
	}
}

func carriedOnly(field, reason string, mutate func(Arguments)) fieldMutation {
	return fieldMutation{field: field, observable: false, reason: reason, mutate: mutate}
}

const (
	reasonMemoryStoreOnly = "consumed only by op=store, whose canonical response is {id,count} and echoes nothing of what was stored; every op that could reflect it also mutates the journal, so a two-invocation parity comparison would differ for reasons of state rather than of argument"
	reasonMemoryUntagged  = "Direct.Memory hardcodes TagPrefix:\"\" for recall/list/export, so no read op filters on tags; the field is consumed by op=store only (see the store-only reason)"
	reasonOtherAnalyzer   = "consumed by a different analyzer than the one this case selects (name=impact); AnalyzeParams is one struct shared by every analyzer"
	reasonNoDepth         = "engine/query.Dispatch passes depth to the neighborhood traversal only; every other structural query takes (symbol) and ignores it"
)

// argumentMutations is the per-operation mutation table. Every JSON field of
// every parity case's argument struct must appear here — see
// TestExecutor_EveryArgumentFieldHasMutationCoverage.
func argumentMutations(ids map[string]model.NodeId) map[string][]fieldMutation {
	other := string(ids["A"])
	table := map[string][]fieldMutation{
		"dead_code": {
			observable("max_items", func(a Arguments) { a.(*DeadCodeArgs).MaxItems = 1 }),
		},
		"architecture": {
			observable("max_items", func(a Arguments) { a.(*ArchitectureArgs).MaxItems = 1 }),
		},
		"architecture_violations": {
			observable("max_items", func(a Arguments) { a.(*ArchitectureViolationsArgs).MaxItems = 1 }),
		},
		"framework_map": {
			observable("max_items", func(a Arguments) { a.(*FrameworkMapArgs).MaxItems = 1 }),
		},
		"repo_overview": {
			observable("max_items", func(a Arguments) { a.(*RepoOverviewArgs).MaxItems = 1 }),
			observable("communities", func(a Arguments) { a.(*RepoOverviewArgs).Communities = false }),
		},
		"search_hybrid": {
			observable("query", func(a Arguments) { a.(*SearchHybridArgs).Query = "p.C" }),
			observable("max_items", func(a Arguments) { a.(*SearchHybridArgs).MaxItems = 1 }),
		},
		"test_impact": {
			observable("target", func(a Arguments) { a.(*TestImpactArgs).Target = other }),
			observable("diff", func(a Arguments) { a.(*TestImpactArgs).Diff = "p/A.go" }),
			observable("depth", func(a Arguments) { a.(*TestImpactArgs).Depth = 1 }),
			observable("max_items", func(a Arguments) { a.(*TestImpactArgs).MaxItems = 1 }),
		},
		"search": {
			observable("query", func(a Arguments) { a.(*SearchArgs).Query = "p.B" }),
			observable("limit", func(a Arguments) { a.(*SearchArgs).Limit = 1 }),
		},
		"search_semantic": {
			observable("query", func(a Arguments) { a.(*SemanticSearchArgs).Query = "p.B" }),
			observable("limit", func(a Arguments) { a.(*SemanticSearchArgs).Limit = 1 }),
		},
		"search_ast": {
			observable("pattern", func(a Arguments) { a.(*SearchASTArgs).Pattern = `{"kind":"package"}` }),
			observable("limit", func(a Arguments) { a.(*SearchASTArgs).Limit = 1 }),
		},
		"find_clones": {
			observable("config", func(a Arguments) { a.(*FindClonesArgs).Config = `{"clone_kinds":["method"]}` }),
		},
		"compound": {
			observable("query", func(a Arguments) { a.(*CompoundArgs).Query = "SEED p.D\nHOP out references\n" }),
		},
		"impact": {
			observable("symbol", func(a Arguments) { a.(*ImpactArgs).Symbol = other }),
			observable("direction", func(a Arguments) { a.(*ImpactArgs).Direction = "reverse" }),
			observable("max_nodes", func(a Arguments) { a.(*ImpactArgs).MaxNodes = 1 }),
		},
		"analyze": {
			observable("name", func(a Arguments) { a.(*AnalyzeArgs).Name = "call-chain" }),
			observable("symbol", func(a Arguments) { a.(*AnalyzeArgs).Symbol = other }),
			observable("direction", func(a Arguments) { a.(*AnalyzeArgs).Direction = "reverse" }),
			observable("max_nodes", func(a Arguments) { a.(*AnalyzeArgs).MaxNodes = 1 }),
			observable("kinds", func(a Arguments) { a.(*AnalyzeArgs).Kinds = []string{query.EdgeKindReferences} }),
			// AnalyzeParams is ONE struct shared by every analyzer, so the
			// fields the impact analyzer does not read are covered by
			// selecting the analyzer that does — a real analyzer, chosen from
			// the registry, not an invented name whose only effect is an error.
			analyzeOverride("target", "call-chain", func(p *AnalyzeParams) { p.Target = string(ids["C"]) }),
			analyzeOverride("max_paths", "call-chain/multipath", func(p *AnalyzeParams) { p.MaxPaths = 1 }),
			analyzeOverride("concept", "concept", func(p *AnalyzeParams) { p.Concept = "p.C" }),
			analyzeOverride("diff", analysis.PriskAnalyzerName, func(p *AnalyzeParams) { p.Diff = "p/B.go" }),
			analyzeOverride("provenance", analysis.PriskAnalyzerName, func(p *AnalyzeParams) { p.Provenance = "summary" }),
		},
		"agent_brief": {
			observable("topic", func(a Arguments) { a.(*BriefArgs).Topic = "p.C" }),
		},
		"explain_symbol": {
			observable("symbol", func(a Arguments) { a.(*ExplainSymbolArgs).Symbol = other }),
			observable("max_items", func(a Arguments) { a.(*ExplainSymbolArgs).MaxItems = 1 }),
		},
		"related_files": {
			observable("target", func(a Arguments) { a.(*RelatedFilesArgs).Target = other }),
			observable("direction", func(a Arguments) { a.(*RelatedFilesArgs).Direction = "reverse" }),
			observable("max_files", func(a Arguments) { a.(*RelatedFilesArgs).MaxFiles = 1 }),
		},
		"change_risk": {
			observable("target", func(a Arguments) { a.(*ChangeRiskArgs).Target = other }),
			observable("diff", func(a Arguments) { a.(*ChangeRiskArgs).Diff = "p/A.go" }),
			observable("max_items", func(a Arguments) { a.(*ChangeRiskArgs).MaxItems = 1 }),
		},
		// SavingsArgs is empty on purpose; the entry exists so "no fields" is a
		// recorded fact rather than a missing table row.
		"savings": {},
		"memory": {
			observable("op", func(a Arguments) { a.(*MemoryArgs).Op = "recall" }),
			observable("scope", func(a Arguments) { a.(*MemoryArgs).Scope = "session" }),
			observable("notebook", func(a Arguments) { a.(*MemoryArgs).Notebook = "other" }),
			observable("limit", func(a Arguments) { a.(*MemoryArgs).Limit = 1 }),
			{
				// op=forget is idempotent and, for an id that does not exist,
				// leaves the journal untouched — so it can be compared twice
				// while still echoing the id back in its canonical response.
				field:      "id",
				observable: true,
				override: func(map[string]model.NodeId) (Arguments, legacyCall) {
					req := MemoryRequest{Op: "forget", ID: "mem-absent-a"}
					return &MemoryArgs{req}, func(ctx context.Context, c Client) ([]byte, error) {
						return c.Memory(ctx, req)
					}
				},
				mutate: func(a Arguments) { a.(*MemoryArgs).ID = "mem-absent-b" },
			},
			{
				// SAFE-01 / SW-112: a non-empty destination path must be
				// REJECTED, not ignored. op=export is the only op that reads
				// it, and it is read-only, so the rejection is provable here.
				field:      "export_to_path",
				observable: true,
				override: func(map[string]model.NodeId) (Arguments, legacyCall) {
					req := MemoryRequest{Op: "export", Scope: "project", Notebook: "nb"}
					return &MemoryArgs{req}, func(ctx context.Context, c Client) ([]byte, error) {
						return c.Memory(ctx, req)
					}
				},
				mutate: func(a Arguments) { a.(*MemoryArgs).ExportToPath = "/tmp/graphi-sw239-must-be-rejected" },
			},
			carriedOnly("tags", reasonMemoryUntagged, func(a Arguments) { a.(*MemoryArgs).Tags = []string{"topic/other"} }),
			carriedOnly("payload", reasonMemoryStoreOnly, func(a Arguments) { a.(*MemoryArgs).Payload = "zeta" }),
			carriedOnly("kind", reasonMemoryStoreOnly, func(a Arguments) { a.(*MemoryArgs).Kind = "note" }),
			carriedOnly("source", reasonMemoryStoreOnly, func(a Arguments) { a.(*MemoryArgs).Source = "sw-239" }),
			carriedOnly("confidence", reasonMemoryStoreOnly, func(a Arguments) { a.(*MemoryArgs).Confidence = "high" }),
			carriedOnly("evidence", reasonMemoryStoreOnly, func(a Arguments) { a.(*MemoryArgs).Evidence = "p/A.go:1" }),
		},
	}

	// The ten structural query operations share QueryArgs, so each one gets the
	// same three field mutations — and Op, which travels as Request.Operation
	// rather than in the payload, is mutated to a DIFFERENT query operation so
	// the addressing itself is proven per operation.
	for _, op := range query.Operations {
		operation := op
		next := query.Operations[0]
		if next == operation {
			next = query.Operations[1]
		}
		muts := []fieldMutation{
			observable("Op", func(a Arguments) { a.(*QueryArgs).Op = next }),
			observable("symbol", func(a Arguments) { a.(*QueryArgs).Symbol = other }),
		}
		if operation == query.OpNeighborhood {
			muts = append(muts, observable("depth", func(a Arguments) { a.(*QueryArgs).Depth = 1 }))
		} else {
			muts = append(muts, carriedOnly("depth", reasonNoDepth, func(a Arguments) { a.(*QueryArgs).Depth = 3 }))
		}
		table[operation] = muts
	}
	return table
}

// TestExecutor_ArgumentMutationsMoveTheParity is AC-2: for every argument field
// an operation consumes, mutating it in the executor path turns the byte-parity
// comparison RED. The baseline is the hand-written legacy call with the
// UNMUTATED arguments, so nothing here compares the adapter with itself.
func TestExecutor_ArgumentMutationsMoveTheParity(t *testing.T) {
	direct, ids := executorParityFixture(t)
	executor, err := NewExecutor(direct)
	if err != nil {
		t.Fatalf("NewExecutor: %v", err)
	}
	ctx := context.Background()
	mutations := argumentMutations(ids)

	for _, tc := range executorParityCases(ids) {
		muts := mutations[tc.operation]
		t.Run(tc.operation, func(t *testing.T) {
			for _, m := range muts {
				t.Run(m.field, func(t *testing.T) {
					base, legacy := tc.args, tc.legacy
					if m.override != nil {
						base, legacy = m.override(ids)
					}
					wantBytes, wantErr := legacy(ctx, direct)
					if wantErr == nil && len(wantBytes) == 0 {
						t.Fatal("the baseline produced no bytes and no error — a mutation of it would prove nothing")
					}

					baseReq, err := executor.NewRequest(base)
					if err != nil {
						t.Fatalf("NewRequest(base): %v", err)
					}
					mutated := cloneArguments(t, base)
					m.mutate(mutated)
					mutatedReq, err := executor.NewRequest(mutated)
					if err != nil {
						t.Fatalf("NewRequest(mutated): %v", err)
					}

					// The mutation must be visible in what the executor
					// transports, and must survive the decode direction. This
					// holds for observable and non-observable fields alike: it
					// is what "the executor forwards this field" means.
					if mutatedReq.Operation == baseReq.Operation && bytes.Equal(mutatedReq.Arguments, baseReq.Arguments) {
						t.Fatalf("mutating %q changed neither the addressed operation nor the argument payload (%s) — the executor drops this field",
							m.field, baseReq.Arguments)
					}
					decoded, err := executor.DecodeArguments(mutatedReq)
					if err != nil {
						t.Fatalf("DecodeArguments(mutated): %v", err)
					}
					reEncoded, err := executor.NewRequest(decoded)
					if err != nil {
						t.Fatalf("NewRequest(decoded): %v", err)
					}
					if !bytes.Equal(reEncoded.Arguments, mutatedReq.Arguments) || reEncoded.Operation != mutatedReq.Operation {
						t.Fatalf("the mutated %q did not survive the round trip:\n  sent: %s %s\n  back: %s %s",
							m.field, mutatedReq.Operation, mutatedReq.Arguments, reEncoded.Operation, reEncoded.Arguments)
					}

					gotBytes, gotErr := executor.Execute(ctx, mutatedReq)
					agree := sameOutcome(wantBytes, wantErr, gotBytes, gotErr)
					switch {
					case m.observable && agree:
						t.Fatalf("mutating %q left the parity GREEN: the mutated call returned the same outcome as the unmutated legacy baseline, so the parity case proves nothing about this field\n  baseline: %s\n  mutated:  %s",
							m.field, truncateForDiff(wantBytes), truncateForDiff(gotBytes))
					case !m.observable && !agree:
						t.Fatalf("%q is recorded as not observable (%s) but mutating it FLIPPED the parity — the record is stale, reclassify it as observable\n  baseline: %v / %s\n  mutated:  %v / %s",
							m.field, m.reason, wantErr, truncateForDiff(wantBytes), gotErr, truncateForDiff(gotBytes))
					}
				})
			}
		})
	}
}

// TestExecutor_EveryArgumentFieldHasMutationCoverage is AC-3: the coverage above
// is enforced by reflection rather than by convention. A field added to any
// adapted argument struct without a mutation entry fails here, so it cannot ship
// silently unproven.
func TestExecutor_EveryArgumentFieldHasMutationCoverage(t *testing.T) {
	_, ids := executorParityFixture(t)
	mutations := argumentMutations(ids)

	for _, tc := range executorParityCases(ids) {
		t.Run(tc.operation, func(t *testing.T) {
			muts, ok := mutations[tc.operation]
			if !ok {
				t.Fatalf("operation %q has parity evidence but no argument-mutation entry", tc.operation)
			}
			fields := argumentFieldNames(reflect.TypeOf(tc.args).Elem())
			covered := map[string]bool{}
			for _, m := range muts {
				if covered[m.field] {
					t.Errorf("field %q has two mutation entries", m.field)
				}
				covered[m.field] = true
				if !m.observable && strings.TrimSpace(m.reason) == "" {
					t.Errorf("field %q is recorded as not observable without a reason", m.field)
				}
				if m.observable && strings.TrimSpace(m.reason) != "" {
					t.Errorf("field %q is observable, so it needs evidence and not a reason", m.field)
				}
				if !fields[m.field] {
					t.Errorf("mutation names field %q, which %T does not have (renamed or removed?)", m.field, tc.args)
				}
			}
			var missing []string
			for f := range fields {
				if !covered[f] {
					missing = append(missing, f)
				}
			}
			if len(missing) > 0 {
				sort.Strings(missing)
				t.Fatalf("%T carries fields with no argument-fidelity mutation: %v — add one, or record why the operation cannot observe it", tc.args, missing)
			}
		})
	}

	// And no dead rows: a table entry for an operation with no parity case
	// would be coverage that is never run.
	cases := map[string]bool{}
	for _, tc := range executorParityCases(ids) {
		cases[tc.operation] = true
	}
	for op := range mutations {
		if !cases[op] {
			t.Errorf("argument-mutation entry %q has no parity case to run against", op)
		}
	}
}

// argumentFieldNames returns the transported name of every field of an argument
// struct: the JSON name, or the Go field name for a field carried outside the
// payload (json:"-"). Embedded structs are flattened the way encoding/json
// flattens them, so MemoryArgs.MemoryRequest contributes its own fields and not
// itself.
func argumentFieldNames(t reflect.Type) map[string]bool {
	out := map[string]bool{}
	var walk func(reflect.Type)
	walk = func(rt reflect.Type) {
		for i := 0; i < rt.NumField(); i++ {
			f := rt.Field(i)
			if f.PkgPath != "" {
				continue // unexported: not transportable
			}
			tag, hasTag := f.Tag.Lookup("json")
			name := strings.Split(tag, ",")[0]
			if f.Anonymous && f.Type.Kind() == reflect.Struct && (!hasTag || name == "") {
				walk(f.Type)
				continue
			}
			if !hasTag || name == "" || name == "-" {
				name = f.Name
			}
			out[name] = true
		}
	}
	walk(t)
	return out
}

// cloneArguments copies an Arguments value so a mutation cannot leak into the
// shared parity case. The copy is made by reflection rather than by the
// executor's own encode/decode round trip: cloning through the adapter would
// let a lossy adapter produce a clone that agrees with it.
func cloneArguments(t *testing.T, a Arguments) Arguments {
	t.Helper()
	v := reflect.ValueOf(a)
	if v.Kind() != reflect.Ptr || v.IsNil() {
		t.Fatalf("arguments %T are not a non-nil pointer", a)
	}
	cp := reflect.New(v.Elem().Type())
	cp.Elem().Set(v.Elem())
	out, ok := cp.Interface().(Arguments)
	if !ok {
		t.Fatalf("clone of %T does not implement Arguments", a)
	}
	return out
}

// sameOutcome is assertSameOutcome's predicate half: it reports whether two
// invocations agree on bytes AND on error identity, without failing the test.
func sameOutcome(wantBytes []byte, wantErr error, gotBytes []byte, gotErr error) bool {
	switch {
	case (wantErr == nil) != (gotErr == nil):
		return false
	case wantErr != nil:
		return errors.Is(gotErr, wantErr) && gotErr.Error() == wantErr.Error()
	}
	return bytes.Equal(gotBytes, wantBytes)
}
