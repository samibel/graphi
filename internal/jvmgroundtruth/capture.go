package jvmgroundtruth

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"
)

// # The incomplete-capture forge, and the gate that closes it (SW-173)
//
// ParseJavap resolves every invoke's callee owner against the classes present
// in the SAME capture. Its doc says the caller "must pass javap output covering
// every class whose calls should be resolvable (external owners resolve to
// CalleeFile "")" — and that parenthesis reads as though an omission merely
// costs recall. It does not. resolveOwner's `if !known { return owner, "" }`
// branch (groundtruth.go) cannot tell an EXTERNAL owner from an intra-repo
// owner that is simply MISSING FROM THIS CAPTURE. So an omitted class makes its
// truth fact lose its source path, score as external, and drop out of the truth
// set entirely — and graphi's CORRECT confirmed call into it is then reported as
// a soundness violation, at every precision, with no abstention, no named reason
// and no counter. A forged stop-ship, indistinguishable from a real one.
//
// SW-172's reviewer demonstrated it on a three-class fixture: the whole capture
// scores sound=true matched=1, and the same fixture with one class omitted
// scores sound=false violations=1 at all three precisions. capture_test.go
// reproduces that demonstration rather than describing it.
//
// It could not fire in SW-172 because disassembleWithDeclared enumerated every
// `.class` and passed them all in ONE javap exec. That is exactly the property
// SW-173 cannot preserve: guava at its pin compiles to 1 966 classes, whose
// dotted names alone are ~100 KB of command line, and a corpus that grows past
// ARG_MAX forces a sharded or per-package javap invocation. The moment the
// capture is assembled from more than one exec, "did every shard land?" becomes
// a real question, and answering it wrong forges stop-ships silently.
//
// The gate below is the answer, and it is deliberately the ONLY door: a Capture
// cannot be constructed without stating which classes it was REQUIRED to cover,
// and NewCapture refuses — with the missing names — rather than returning a
// capture that would forge. Recall loss is a measurement; a forged stop-ship is
// a lie about the product, so this fails closed.
//
// The required set is enumerated from the compiler's OWN output directory (every
// `.class` file on disk), never from the capture itself. A set derived from the
// capture would be trivially self-satisfying: an omitted class is absent from
// both sides and the check would pass exactly when it must fail.

// IncompleteCaptureError reports a javap capture that omits classes it was
// required to cover. It carries the missing names because "the capture is
// short" is not actionable and "a.Iface is missing" is.
type IncompleteCaptureError struct {
	// Missing is the sorted set of required internal class names the capture
	// does not contain.
	Missing []string
	// Required and Present are the two set sizes, so a report can state the
	// shortfall as a fraction rather than only naming the first few.
	Required int
	Present  int
}

func (e *IncompleteCaptureError) Error() string {
	shown := e.Missing
	const max = 8
	suffix := ""
	if len(shown) > max {
		shown, suffix = shown[:max], fmt.Sprintf(" (+%d more)", len(e.Missing)-max)
	}
	return fmt.Sprintf(
		"jvmgroundtruth: INCOMPLETE javap capture — %d of %d required classes are missing: %s%s; "+
			"scoring this capture would forge soundness violations against correct code "+
			"(an omitted intra-repo owner is indistinguishable from an external one), so it is refused",
		len(e.Missing), e.Required, strings.Join(shown, ", "), suffix)
}

// Capture is a javap disassembly bound to the PROOF that it covers every class
// it was required to cover. It is the only supported way to feed a multi-exec
// (sharded) disassembly into the oracle.
//
// Construct it with NewCapture. There is deliberately no way to build one from
// bytes alone: the required set is the whole point, and a constructor that made
// it optional would be a constructor that makes forging the default.
type Capture struct {
	merged  []byte
	present []string // sorted internal names
}

// NewCapture merges shard outputs and refuses the result unless every class in
// required appears in it.
//
// shards are concatenated in the order given; ShardClasses produces that order
// deterministically from a sorted class list, so identical inputs yield
// byte-identical merged bytes and therefore an identical Digest. That is what
// makes the AC-5 two-run reproducibility check meaningful: it compares the exact
// bytes the oracle consumes, not a proxy for them.
//
// required may be given in dotted (a.App) or internal (a/App) form; both are
// normalized, so a caller may pass the class names exactly as it enumerated
// them from the output directory.
func NewCapture(shards [][]byte, required []string) (*Capture, error) {
	merged := mergeShards(shards)

	classes, err := parseClasses(merged)
	if err != nil {
		return nil, err
	}
	present := make([]string, 0, len(classes))
	for internal := range classes {
		present = append(present, internal)
	}
	sort.Strings(present)

	have := make(map[string]struct{}, len(present))
	for _, c := range present {
		have[c] = struct{}{}
	}
	var missing []string
	seen := make(map[string]struct{}, len(required))
	for _, r := range required {
		internal := internalClassName(r)
		if internal == "" {
			continue
		}
		if _, dup := seen[internal]; dup {
			continue
		}
		seen[internal] = struct{}{}
		if _, ok := have[internal]; !ok {
			missing = append(missing, internal)
		}
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		return nil, &IncompleteCaptureError{
			Missing:  missing,
			Required: len(seen),
			Present:  len(present),
		}
	}
	return &Capture{merged: merged, present: present}, nil
}

// Bytes returns the merged capture. The slice is the Capture's own; callers
// must not mutate it.
func (c *Capture) Bytes() []byte { return c.merged }

// Classes returns the sorted internal names the capture contains.
func (c *Capture) Classes() []string { return append([]string(nil), c.present...) }

// Digest is the sha256 of the merged capture bytes — the identity of the exact
// input the oracle consumes. Two runs of a reproducible compile strategy must
// produce the same Digest; a difference is an environment finding to explain,
// never a flake to retry away.
func (c *Capture) Digest() string {
	sum := sha256.Sum256(c.merged)
	return hex.EncodeToString(sum[:])
}

// Calls parses the capture into bytecode call facts (see ParseJavap).
func (c *Capture) Calls() ([]Call, error) { return ParseJavap(c.merged) }

// Declared parses javac's own declared-method index out of the capture (see
// ParseDeclaredMethods).
func (c *Capture) Declared() (DeclaredMethods, error) { return ParseDeclaredMethods(c.merged) }

// mergeShards concatenates shard outputs, guaranteeing a newline boundary
// between them so a shard that does not end in one cannot glue its last line to
// the next shard's first line and corrupt both.
func mergeShards(shards [][]byte) []byte {
	var out []byte
	for _, s := range shards {
		if len(s) == 0 {
			continue
		}
		out = append(out, s...)
		if s[len(s)-1] != '\n' {
			out = append(out, '\n')
		}
	}
	return out
}

// internalClassName normalizes a dotted or internal class name to internal form.
func internalClassName(s string) string {
	return strings.ReplaceAll(strings.TrimSpace(s), ".", "/")
}

// ShardClasses splits class names into deterministic batches whose joined
// command-line length stays under maxBytes.
//
// Deterministic in both senses that matter: the input is SORTED first, so the
// batching does not depend on the order a directory walk happened to yield, and
// the split points are a pure function of the sorted names — so two runs shard
// identically and their merged captures are byte-identical.
//
// A single name longer than maxBytes still gets its own batch rather than being
// dropped: losing a class silently is the failure this whole file exists to
// prevent, and an over-long exec fails loudly at the OS.
func ShardClasses(classes []string, maxBytes int) [][]string {
	if maxBytes <= 0 {
		panic("jvmgroundtruth: ShardClasses maxBytes must be positive")
	}
	sorted := append([]string(nil), classes...)
	sort.Strings(sorted)

	var batches [][]string
	var cur []string
	size := 0
	for _, c := range sorted {
		n := len(c) + 1 // +1 for the argument separator
		if len(cur) > 0 && size+n > maxBytes {
			batches = append(batches, cur)
			cur, size = nil, 0
		}
		cur = append(cur, c)
		size += n
	}
	if len(cur) > 0 {
		batches = append(batches, cur)
	}
	return batches
}
