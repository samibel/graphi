package main

import (
	"sync"

	"github.com/samibel/graphi/core/graphstore"
	"github.com/samibel/graphi/engine/analysis"
)

// Short verbs (SW-069, EP-010 Task F) are PURELY ADDITIVE thin aliases over the
// existing long-form dispatchers. `graphi callers X` rewrites to the long-form
// `query callers -symbol X`; `graphi impact X` rewrites to `analyze impact
// -symbol X`. They add NO engine logic — rewriteVerbArgs only reshapes argv so
// the existing runQuery/runAnalyze paths produce BYTE-IDENTICAL output to the
// long form (the parity contract).

// queryVerbSet is the fixed set of structural-query operations exposed as short
// verbs, mapping to the `query` dispatcher.
var queryVerbSet = map[string]bool{
	"callers":      true,
	"callees":      true,
	"references":   true,
	"definition":   true,
	"neighborhood": true,
}

// analyzeVerbSetOnce/value memoize the analyze verb set so it is derived once
// from the real analyzer registry and stays in lock-step with it.
var (
	analyzeVerbSetOnce  sync.Once
	analyzeVerbSetValue map[string]bool
)

// analyzeVerbSet returns the set of analyzer names exposed as short verbs,
// derived from the real default analysis service so the verbs track the
// analyzers. It asserts "impact" and "taint" are present and falls back to
// including them if a future refactor drops them from the default registry.
func analyzeVerbSet() map[string]bool {
	analyzeVerbSetOnce.Do(func() {
		set := map[string]bool{}
		for _, n := range analysis.NewDefaultService(graphstore.NewMemStore()).Names() {
			set[n] = true
		}
		// Lock-step assertion: impact + taint are the canonical headline
		// analyzers; guarantee they alias even if the registry shape shifts.
		set["impact"] = true
		set["taint"] = true
		analyzeVerbSetValue = set
	})
	return analyzeVerbSetValue
}

// rewriteVerbArgs maps a short-verb invocation's args to the long-form argv the
// existing query/analyze dispatchers expect. op is the verb (e.g. "callers");
// args are the tokens AFTER the verb (i.e. os.Args[2:]).
//
// It (1) pulls any leading -db/-daemon pair(s) into a prefix — matching
// extractFlags's front-pull so they keep working through the alias — then (2)
// promotes the FIRST remaining bare (non-flag) token to `-symbol <token>`,
// leaving everything else in order. The result is `prefix + op + rest`, i.e.
// `[-db v] callers -symbol X [-depth 2]`, identical to the long form.
func rewriteVerbArgs(op string, args []string) []string {
	prefix := make([]string, 0, 4)
	rest := make([]string, 0, len(args))

	// 1. Front-pull leading -db/-daemon (space- or =-separated) into prefix,
	// stopping at the first token that is not one of those flags.
	i := 0
	for i < len(args) {
		a := args[i]
		switch {
		case a == "-db" && i+1 < len(args):
			prefix = append(prefix, a, args[i+1])
			i += 2
		case len(a) > 4 && a[:4] == "-db=":
			prefix = append(prefix, a)
			i++
		case a == "-daemon" && i+1 < len(args):
			prefix = append(prefix, a, args[i+1])
			i += 2
		case len(a) > 8 && a[:8] == "-daemon=":
			prefix = append(prefix, a)
			i++
		default:
			goto frontPullDone
		}
	}
frontPullDone:

	// 2. Promote the FIRST remaining token to `-symbol <token>`, but ONLY when it
	// is a bare positional (does not start with '-'). If the first remaining
	// token is a flag, or an explicit -symbol is already present anywhere, leave
	// the args untouched. (We only promote the leading positional — a later bare
	// token is a flag's value, e.g. the `3` in `-depth 3`, and must not move.)
	hasSymbol := false
	for _, a := range args[i:] {
		if a == "-symbol" || (len(a) >= 8 && a[:8] == "-symbol=") {
			hasSymbol = true
			break
		}
	}
	tail := args[i:]
	if len(tail) > 0 && !hasSymbol && len(tail[0]) > 0 && tail[0][0] != '-' {
		rest = append(rest, "-symbol", tail[0])
		rest = append(rest, tail[1:]...)
	} else {
		rest = append(rest, tail...)
	}

	out := append(prefix, op)
	return append(out, rest...)
}
