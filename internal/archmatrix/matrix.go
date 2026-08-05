// Package archmatrix owns the ARCH-P0 migration matrix: the inventory that maps
// every method of the broad surfaces/client.Client contract onto the bounded
// context that will own it, the application service it moves to, and the
// architecture phase that moves it.
//
// The P2 modularization removes a 29-method central interface by relocating its
// responsibilities. That only works if the target of every single method is
// decided up front and stays decided — a method with no recorded owner is a
// method that silently stays behind in the legacy client. So this matrix is not
// documentation about the code; it is checked against the code. The drift guard
// in check.go derives the live method set by reflection and the live error
// sentinels from the source, and fails when the matrix and the code disagree in
// either direction.
//
// It follows the internal/coverage house pattern: a checked-in YAML source of
// truth, a generated Markdown rendering, and one `-check` command that fails CI
// on drift or staleness. Like internal/coverage, internal/evidence, and
// internal/layerguard, this is UNRANKED CI tooling — it is not part of any
// shipped runtime import graph.
package archmatrix

import (
	"fmt"
	"os"
	"sort"
	"strings"
)

// Repo-relative paths to the checked-in matrix source and rendered table.
const (
	MatrixYAMLPath = "docs/migration-matrix.yaml"
	MatrixMDPath   = "docs/migration-matrix.md"
)

// Bounded contexts, fixed by the P2 Phase 0 PRD. Every client method belongs to
// exactly one.
const (
	ContextGraphRead  = "graphread"
	ContextCodeChange = "codechange"
	ContextReview     = "review"
	ContextKnowledge  = "knowledge"
	ContextOperations = "operations"
)

// contextPlan records, per bounded context, the application package that will own
// its use cases and the PRD phase that migrates them. Deriving both from the
// context (instead of repeating them on every row) means a row cannot claim a
// target service that contradicts its own context, and the phase plan stays
// stated in exactly one place.
type contextPlan struct {
	Service string
	Phase   int
	Title   string
}

var contextPlans = map[string]contextPlan{
	ContextGraphRead:  {Service: "app/graphread", Phase: 4, Title: "Graph Read"},
	ContextCodeChange: {Service: "app/codechange", Phase: 5, Title: "Code Change"},
	ContextReview:     {Service: "app/review", Phase: 6, Title: "Review & Forge"},
	ContextKnowledge:  {Service: "app/knowledge", Phase: 7, Title: "Knowledge"},
	ContextOperations: {Service: "app/operations", Phase: 7, Title: "Operations & Capability"},
}

// contextOrder is the rendering order: the phase order the migration runs in.
var contextOrder = []string{
	ContextGraphRead, ContextCodeChange, ContextReview, ContextKnowledge, ContextOperations,
}

// Implementation-status values for a client implementation on a given method.
const (
	// ImplFull means the implementation executes the real operation.
	ImplFull = "full"
	// ImplUnavailable means it refuses with a typed sentinel and performs no work
	// — the compatibility stubs the PRD counts and forbids growing.
	ImplUnavailable = "unavailable"
	// ImplTypedSkip means it returns a typed graceful-skip payload with NO error,
	// which is a different contract from an unavailable sentinel and must not be
	// collapsed into one during the refactor.
	ImplTypedSkip = "typed-skip"
)

var validImpl = map[string]bool{ImplFull: true, ImplUnavailable: true, ImplTypedSkip: true}

// Sentinel kinds classify what an exported error in surfaces/client guards.
const (
	// SentinelCapability guards an optional service that is not wired.
	SentinelCapability = "capability"
	// SentinelTransport is a transport-level failure of a remote client.
	SentinelTransport = "transport"
	// SentinelSafety is a deliberate refusal (a safety contract, not a gap).
	SentinelSafety = "safety"
	// SentinelValidation rejects malformed caller input.
	SentinelValidation = "validation"
)

var validSentinelKind = map[string]bool{
	SentinelCapability: true, SentinelTransport: true,
	SentinelSafety: true, SentinelValidation: true,
}

// Method is one row of the migration matrix.
type Method struct {
	// Name is the exact method name on surfaces/client.Client.
	Name string
	// Context is the bounded context that will own the use case.
	Context string
	// Direct, HTTP, Daemon record today's implementation status per client. They
	// are cross-checked against the source by CheckStubs, so a new compatibility
	// stub cannot appear without being recorded here.
	Direct string
	HTTP   string
	Daemon string
	// Pilot marks a use case the PRD migrates first, as the Phase 3 differential
	// pilot, ahead of its context's bulk migration.
	Pilot bool
	// Decision records an open question a maintainer must sign off on. A non-empty
	// value is surfaced in the rendered matrix rather than buried.
	Decision string
	// Note is a short human remark.
	Note string
}

// Service returns the application package that will own this method.
func (m Method) Service() string { return contextPlans[m.Context].Service }

// Phase returns the PRD phase that migrates this method.
func (m Method) Phase() int { return contextPlans[m.Context].Phase }

// Sentinel is one row of the error-sentinel inventory.
type Sentinel struct {
	// Name is the exported variable name in surfaces/client.
	Name string
	// Kind classifies what the sentinel guards.
	Kind string
	// Note is a short human remark.
	Note string
}

// Matrix is the whole checked-in inventory.
type Matrix struct {
	Methods   []Method
	Sentinels []Sentinel
}

// MethodNames returns the recorded method names, sorted.
func (m Matrix) MethodNames() []string {
	out := make([]string, 0, len(m.Methods))
	for _, row := range m.Methods {
		out = append(out, row.Name)
	}
	sort.Strings(out)
	return out
}

// SentinelNames returns the recorded sentinel names, sorted.
func (m Matrix) SentinelNames() []string {
	out := make([]string, 0, len(m.Sentinels))
	for _, row := range m.Sentinels {
		out = append(out, row.Name)
	}
	sort.Strings(out)
	return out
}

// ByContext returns the methods of one bounded context, sorted by name.
func (m Matrix) ByContext(context string) []Method {
	var out []Method
	for _, row := range m.Methods {
		if row.Context == context {
			out = append(out, row)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// Load reads and parses the checked-in migration matrix YAML at path.
func Load(path string) (Matrix, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return Matrix{}, fmt.Errorf("archmatrix: read matrix %q: %w", path, err)
	}
	m, err := parseYAML(string(raw))
	if err != nil {
		return Matrix{}, fmt.Errorf("archmatrix: parse matrix %q: %w", path, err)
	}
	if err := validate(m); err != nil {
		return Matrix{}, fmt.Errorf("archmatrix: invalid matrix %q: %w", path, err)
	}
	sort.Slice(m.Methods, func(i, j int) bool { return m.Methods[i].Name < m.Methods[j].Name })
	sort.Slice(m.Sentinels, func(i, j int) bool { return m.Sentinels[i].Name < m.Sentinels[j].Name })
	return m, nil
}

// validate enforces the per-row invariants the matrix promises.
func validate(m Matrix) error {
	seen := map[string]bool{}
	for _, row := range m.Methods {
		if row.Name == "" {
			return fmt.Errorf("method row without a name")
		}
		if seen[row.Name] {
			return fmt.Errorf("method %q listed twice — every method must have exactly one owning context", row.Name)
		}
		seen[row.Name] = true
		if _, ok := contextPlans[row.Context]; !ok {
			return fmt.Errorf("method %q has unknown context %q (want one of %s)", row.Name, row.Context, strings.Join(contextOrder, ", "))
		}
		for label, impl := range map[string]string{"direct": row.Direct, "http": row.HTTP, "daemon": row.Daemon} {
			if !validImpl[impl] {
				return fmt.Errorf("method %q has invalid %s status %q (want full|unavailable|typed-skip)", row.Name, label, impl)
			}
		}
	}

	seenSentinel := map[string]bool{}
	for _, row := range m.Sentinels {
		if row.Name == "" {
			return fmt.Errorf("sentinel row without a name")
		}
		if seenSentinel[row.Name] {
			return fmt.Errorf("sentinel %q listed twice", row.Name)
		}
		seenSentinel[row.Name] = true
		if !validSentinelKind[row.Kind] {
			return fmt.Errorf("sentinel %q has invalid kind %q (want capability|transport|safety|validation)", row.Name, row.Kind)
		}
	}
	return nil
}

// parseYAML parses the strict block-list subset this repo emits, mirroring the
// approach in internal/coverage: graphi deliberately carries no YAML dependency,
// so the matrix format stays inside what a small, explicit parser can validate.
//
// Grammar (whitespace-significant):
//
//	# comments and blank lines are ignored anywhere
//	methods:
//	  - method: <scalar>
//	    context: <scalar>
//	    ...
//	sentinels:
//	  - name: <scalar>
//	    kind: <scalar>
func parseYAML(text string) (Matrix, error) {
	var (
		m           Matrix
		section     string
		curMethod   *Method
		curSentinel *Sentinel
	)

	flush := func() {
		if curMethod != nil {
			m.Methods = append(m.Methods, *curMethod)
			curMethod = nil
		}
		if curSentinel != nil {
			m.Sentinels = append(m.Sentinels, *curSentinel)
			curSentinel = nil
		}
	}

	for lineNo, rawLine := range strings.Split(text, "\n") {
		line := stripComment(rawLine)
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}

		if trimmed == "methods:" || trimmed == "sentinels:" {
			flush()
			section = strings.TrimSuffix(trimmed, ":")
			continue
		}
		if section == "" {
			return Matrix{}, fmt.Errorf("line %d: content before any 'methods:' or 'sentinels:' key: %q", lineNo+1, trimmed)
		}

		if strings.HasPrefix(trimmed, "- ") {
			flush()
			switch section {
			case "methods":
				curMethod = &Method{}
			case "sentinels":
				curSentinel = &Sentinel{}
			}
			trimmed = strings.TrimSpace(strings.TrimPrefix(trimmed, "-"))
		}

		key, val, err := splitField(trimmed)
		if err != nil {
			return Matrix{}, fmt.Errorf("line %d: %w", lineNo+1, err)
		}
		switch {
		case curMethod != nil:
			if err := assignMethodField(curMethod, key, val); err != nil {
				return Matrix{}, fmt.Errorf("line %d: %w", lineNo+1, err)
			}
		case curSentinel != nil:
			if err := assignSentinelField(curSentinel, key, val); err != nil {
				return Matrix{}, fmt.Errorf("line %d: %w", lineNo+1, err)
			}
		default:
			return Matrix{}, fmt.Errorf("line %d: field outside any list item: %q", lineNo+1, trimmed)
		}
	}
	flush()

	if len(m.Methods) == 0 {
		return Matrix{}, fmt.Errorf("no 'methods:' rows found")
	}
	if len(m.Sentinels) == 0 {
		return Matrix{}, fmt.Errorf("no 'sentinels:' rows found")
	}
	return m, nil
}

func assignMethodField(m *Method, key, val string) error {
	switch key {
	case "method":
		m.Name = val
	case "context":
		m.Context = val
	case "direct":
		m.Direct = val
	case "http":
		m.HTTP = val
	case "daemon":
		m.Daemon = val
	case "pilot":
		parsed, err := parseBool(val)
		if err != nil {
			return fmt.Errorf("method %q: %w", m.Name, err)
		}
		m.Pilot = parsed
	case "decision":
		m.Decision = val
	case "note":
		m.Note = val
	default:
		return fmt.Errorf("unknown method field %q", key)
	}
	return nil
}

func assignSentinelField(s *Sentinel, key, val string) error {
	switch key {
	case "name":
		s.Name = val
	case "kind":
		s.Kind = val
	case "note":
		s.Note = val
	default:
		return fmt.Errorf("unknown sentinel field %q", key)
	}
	return nil
}

func parseBool(val string) (bool, error) {
	switch val {
	case "true", "yes":
		return true, nil
	case "false", "no":
		return false, nil
	}
	return false, fmt.Errorf("invalid boolean %q (want true|false)", val)
}

// splitField splits "key: value" and unquotes a double-quoted value.
func splitField(s string) (key, val string, err error) {
	i := strings.IndexByte(s, ':')
	if i < 0 {
		return "", "", fmt.Errorf("not a key: value field: %q", s)
	}
	key = strings.TrimSpace(s[:i])
	val = strings.TrimSpace(s[i+1:])
	if len(val) >= 2 && val[0] == '"' && val[len(val)-1] == '"' {
		val = val[1 : len(val)-1]
	}
	return key, val, nil
}

// stripComment removes a "# ..." comment that is not inside a quoted scalar.
func stripComment(line string) string {
	inQuote := false
	for i := 0; i < len(line); i++ {
		switch line[i] {
		case '"':
			inQuote = !inQuote
		case '#':
			if !inQuote {
				return line[:i]
			}
		}
	}
	return line
}
