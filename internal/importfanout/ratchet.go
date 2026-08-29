package importfanout

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
)

// This file is the AX-16a ratchet (SW-253). The AX-00 metric above stays what
// it was — a non-blocking report against the historical baseline. The ratchet
// is a second, separate contract: a checked-in ceiling file that names every
// allowed edge with a reason and a category, and a check that fails the build
// when the measured set differs from the declared one in ANY direction.
//
// Three rules, all deliberate:
//
//   - Over the ceiling, or an edge that is not declared, fails. An exchanged
//     edge (one out, one in, count unchanged) is a new dependency, not a free
//     move, so the SET is what is checked, not the number.
//   - UNDER the ceiling fails too, with the instruction to lower the ceiling to
//     the measured value in the same change. A gain that is not locked in is
//     slack the next story spends without noticing.
//   - Raising the ceiling is a reviewed edit that names its story (`raised_by`).
//     Regeneration never raises it silently: when the measured count is above
//     the ceiling it writes an empty `raised_by` placeholder that fails
//     validation until a human fills it in.
//
// The TRANSITIVE closure is measured and reported beside the direct number but
// never gated. It is the anti-gaming instrument: moving an import one hop away
// into a re-export or collector package lowers the direct count and leaves the
// transitive count where it was, and both numbers sit on the same log line.

// Category is the declared kind of one allowed edge. The vocabulary is closed
// so a reason cannot be smuggled in under an unreviewed heading.
type Category string

const (
	// CategoryTransport — the edge exists so the client can reach a wire.
	CategoryTransport Category = "transport"
	// CategoryCompatAdapter — the edge belongs to the executor-seam legacy
	// adapters / canary machinery and is expected to go when AX-17 fires.
	CategoryCompatAdapter Category = "compat-adapter"
	// CategoryEngineHandler — the client calls an engine service directly
	// because no engine-side handler exists yet (the AX-15 / AX-16b target).
	CategoryEngineHandler Category = "engine-handler"
	// CategoryPort — the edge is a type-only dependency on a port or model.
	CategoryPort Category = "port"
	// CategoryTooling — unranked internal helpers (state, freshness, release).
	CategoryTooling Category = "tooling"
)

// Categories is the closed vocabulary, in declaration order.
var Categories = []Category{CategoryTransport, CategoryCompatAdapter, CategoryEngineHandler, CategoryPort, CategoryTooling}

func validCategory(c Category) bool {
	for _, known := range Categories {
		if c == known {
			return true
		}
	}
	return false
}

// AllowedImport is one declared edge.
type AllowedImport struct {
	Path     string   `json:"path"`
	Category Category `json:"category"`
	Reason   string   `json:"reason"`
}

// Targets records where the number is meant to go. Intent, not a gate: the
// ratchet only stops the number rising, and the targets are beside it so the
// intent is not lost in a backlog.
type Targets struct {
	Intermediate int `json:"intermediate"`
	Final        int `json:"final"`
}

// Raise names the story that raised the ceiling and why. A non-nil Raise with
// an empty Story or Reason is invalid — that is the placeholder regeneration
// leaves behind, and it is meant to fail until a human fills it in.
type Raise struct {
	Story  string `json:"story"`
	Reason string `json:"reason"`
}

// Ceiling is the checked-in ratchet declaration (docs/rc/ax16-import-fanout-ceiling.json).
type Ceiling struct {
	Package        string          `json:"package"`
	Ceiling        int             `json:"ceiling"`
	AllowedImports []AllowedImport `json:"allowed_imports"`
	Targets        Targets         `json:"targets"`
	RaisedBy       *Raise          `json:"raised_by"`
}

// LoadCeiling reads and decodes a ceiling file. Unknown fields are an error:
// a misspelt key would otherwise silently declare nothing.
func LoadCeiling(path string) (Ceiling, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return Ceiling{}, fmt.Errorf("importfanout: ceiling file %s is missing or unreadable: %w", path, err)
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	var c Ceiling
	if err := dec.Decode(&c); err != nil {
		return Ceiling{}, fmt.Errorf("importfanout: ceiling file %s is corrupt: %w", path, err)
	}
	return c, nil
}

// Marshal renders the declaration in the one canonical byte form the file is
// committed in (2-space indent, no HTML escaping, trailing newline).
func (c Ceiling) Marshal() ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(c); err != nil {
		return nil, fmt.Errorf("importfanout: encode ceiling: %w", err)
	}
	return buf.Bytes(), nil
}

// Validate is the schema half of the ratchet (SW-253 AC-1 and AC-3). It says
// nothing about the measured tree; Check does that.
func (c Ceiling) Validate() error {
	var problems []string
	if c.Package == "" {
		problems = append(problems, "`package` is empty")
	}
	if c.Ceiling <= 0 {
		problems = append(problems, fmt.Sprintf("`ceiling` is %d; it must be positive", c.Ceiling))
	}
	if len(c.AllowedImports) == 0 {
		problems = append(problems, "`allowed_imports` is empty")
	}
	seen := map[string]bool{}
	for i, edge := range c.AllowedImports {
		switch {
		case edge.Path == "":
			problems = append(problems, fmt.Sprintf("allowed_imports[%d]: `path` is empty", i))
		case strings.HasPrefix(edge.Path, ModulePath):
			problems = append(problems, fmt.Sprintf("allowed_imports[%d]: `path` %q must be module-relative (drop the %s/ prefix)", i, edge.Path, ModulePath))
		case seen[edge.Path]:
			problems = append(problems, fmt.Sprintf("allowed_imports[%d]: `path` %q is declared twice", i, edge.Path))
		}
		seen[edge.Path] = true
		if i > 0 && c.AllowedImports[i-1].Path > edge.Path {
			problems = append(problems, fmt.Sprintf("allowed_imports[%d]: %q sorts before %q — keep the list sorted so the diff reads", i, edge.Path, c.AllowedImports[i-1].Path))
		}
		if !validCategory(edge.Category) {
			problems = append(problems, fmt.Sprintf("allowed_imports[%d] (%s): `category` %q is not one of %s", i, edge.Path, edge.Category, joinCategories()))
		}
		if strings.TrimSpace(edge.Reason) == "" {
			problems = append(problems, fmt.Sprintf("allowed_imports[%d] (%s): `reason` is empty — every edge is a declared, reasoned fact", i, edge.Path))
		}
	}
	// AC-3: the ceiling can never exceed what is declared. A ceiling with room
	// in it is an inline bump nobody reviewed.
	if c.Ceiling > len(c.AllowedImports) {
		problems = append(problems, fmt.Sprintf("`ceiling` %d is greater than the %d declared `allowed_imports` — raising the ceiling means declaring the new edges with a reason AND naming the story in `raised_by`, never bumping the number", c.Ceiling, len(c.AllowedImports)))
	}
	if c.RaisedBy != nil && (strings.TrimSpace(c.RaisedBy.Story) == "" || strings.TrimSpace(c.RaisedBy.Reason) == "") {
		problems = append(problems, "`raised_by` is set but does not carry both a `story` and a non-empty `reason` — a raise is a reviewed edit that names its story")
	}
	// Targets are intent, not a bound on the ceiling: once the ceiling drops
	// below a target the target is met, and the file must still validate.
	if c.Targets.Final <= 0 || c.Targets.Intermediate < c.Targets.Final {
		problems = append(problems, fmt.Sprintf("`targets` {intermediate %d, final %d} must satisfy 0 < final <= intermediate", c.Targets.Intermediate, c.Targets.Final))
	}
	if len(problems) == 0 {
		return nil
	}
	return fmt.Errorf("importfanout: ceiling declaration for %q is invalid:\n  - %s", c.Package, strings.Join(problems, "\n  - "))
}

func joinCategories() string {
	names := make([]string, len(Categories))
	for i, c := range Categories {
		names[i] = string(c)
	}
	return strings.Join(names, ", ")
}

// Regenerate returns the declaration re-pinned to a measurement: the allowed
// set becomes exactly the measured set (known edges keep their category and
// reason, new edges get an empty reason that Validate rejects until a human
// writes one) and the ceiling becomes the measured count. When the count went
// UP, `raised_by` is set to an empty placeholder so the raise cannot pass review
// unnamed; when it went down or stayed, `raised_by` is left as it was.
//
// This is the GRAPHI_UPDATE_GOLDEN=1 path. It is an explicit act, never a
// side effect of a failing check.
func (c Ceiling) Regenerate(measured Result) Ceiling {
	known := map[string]AllowedImport{}
	for _, edge := range c.AllowedImports {
		known[edge.Path] = edge
	}
	next := Ceiling{
		Package:  measured.Package,
		Ceiling:  measured.Fanout,
		Targets:  c.Targets,
		RaisedBy: c.RaisedBy,
	}
	paths := append([]string(nil), measured.Imports...)
	sort.Strings(paths)
	for _, path := range paths {
		if edge, ok := known[path]; ok {
			next.AllowedImports = append(next.AllowedImports, edge)
			continue
		}
		next.AllowedImports = append(next.AllowedImports, AllowedImport{Path: path})
	}
	if measured.Fanout > c.Ceiling && c.Ceiling > 0 {
		next.RaisedBy = &Raise{}
	}
	return next
}

// Transitive is the internal closure of one package: every distinct package in
// this module reachable from it through non-test imports, itself excluded.
type Transitive struct {
	Package  string   `json:"package"`
	Count    int      `json:"count"`
	Packages []string `json:"packages"`
}

// MeasureTransitive walks imports with go/parser from root/pkgPath outward and
// returns the closure. It reuses Measure per package, so the same rules apply
// at every hop: non-test files only, every build configuration counted.
//
// go/ast rather than `go list -deps` for the same reason as Measure: this runs
// inside an ordinary test and must stay hermetic and instant. The two can
// differ on build-tagged files; for a coupling number the go/ast answer is the
// more honest one, and the choice is recorded here (SW-253 AC-4).
func MeasureTransitive(root, pkgPath string) (Transitive, error) {
	visited := map[string]bool{pkgPath: true}
	queue := []string{pkgPath}
	for len(queue) > 0 {
		pkg := queue[0]
		queue = queue[1:]
		res, err := Measure(pkgDir(root, pkg), pkg)
		if err != nil {
			return Transitive{}, fmt.Errorf("transitive closure of %s: at %s: %w", pkgPath, pkg, err)
		}
		for _, dep := range res.Imports {
			if !visited[dep] {
				visited[dep] = true
				queue = append(queue, dep)
			}
		}
	}
	delete(visited, pkgPath)
	packages := make([]string, 0, len(visited))
	for pkg := range visited {
		packages = append(packages, pkg)
	}
	sort.Strings(packages)
	return Transitive{Package: pkgPath, Count: len(packages), Packages: packages}, nil
}

func pkgDir(root, pkg string) string {
	return root + string(os.PathSeparator) + strings.ReplaceAll(pkg, "/", string(os.PathSeparator))
}

// Report is the ratchet's verdict over one measurement.
type Report struct {
	Package    string
	Direct     int
	Ceiling    int
	Transitive int
	// Undeclared are measured edges that are not in allowed_imports — new
	// dependencies, whether or not the count moved.
	Undeclared []string
	// Stale are declared edges the package no longer imports. They are slack:
	// a later change could re-add them for free, so they fail too.
	Stale []string
}

// Check compares a measurement against the declaration. It assumes
// c.Validate() passed; a malformed declaration is a different failure.
func Check(c Ceiling, direct Result, transitive Transitive) Report {
	rep := Report{Package: c.Package, Direct: direct.Fanout, Ceiling: c.Ceiling, Transitive: transitive.Count}
	allowed := map[string]bool{}
	for _, edge := range c.AllowedImports {
		allowed[edge.Path] = true
	}
	measured := map[string]bool{}
	for _, path := range direct.Imports {
		measured[path] = true
		if !allowed[path] {
			rep.Undeclared = append(rep.Undeclared, path)
		}
	}
	for _, edge := range c.AllowedImports {
		if !measured[edge.Path] {
			rep.Stale = append(rep.Stale, edge.Path)
		}
	}
	sort.Strings(rep.Undeclared)
	sort.Strings(rep.Stale)
	return rep
}

// Line is the one-line report SW-253 AC-4 asks for, in exactly this shape:
//
//	direct 44 (ceiling 44) · transitive 137
func (r Report) Line() string {
	return fmt.Sprintf("direct %d (ceiling %d) · transitive %d", r.Direct, r.Ceiling, r.Transitive)
}

// Failures lists every rule the measurement breaks, in the order a reader
// should fix them. Empty means the ratchet holds.
func (r Report) Failures() []string {
	var out []string
	if r.Direct > r.Ceiling {
		out = append(out, fmt.Sprintf("direct fan-out %d exceeds the ceiling %d (+%d); added edges: %s", r.Direct, r.Ceiling, r.Direct-r.Ceiling, orNone(r.Undeclared)))
	}
	if len(r.Undeclared) > 0 {
		out = append(out, fmt.Sprintf("undeclared import(s) — every edge must appear in allowed_imports with a category and a reason (an exchanged edge is a new dependency, not a free move): %s", strings.Join(r.Undeclared, ", ")))
	}
	if len(r.Stale) > 0 {
		out = append(out, fmt.Sprintf("declared but no longer imported — remove from allowed_imports so the gain is locked in: %s", strings.Join(r.Stale, ", ")))
	}
	if r.Direct < r.Ceiling {
		out = append(out, fmt.Sprintf("direct fan-out %d is BELOW the ceiling %d — lower `ceiling` to %d in the same change; gains are locked in, never left as slack", r.Direct, r.Ceiling, r.Direct))
	}
	return out
}

func orNone(paths []string) string {
	if len(paths) == 0 {
		return "(none — the count rose without a new edge; re-run the measurement)"
	}
	return strings.Join(paths, ", ")
}

// Pass is true when the ratchet holds.
func (r Report) Pass() bool { return len(r.Failures()) == 0 }

// Format renders the verdict. ceilingFile is named so the failing render tells
// the reader which file to edit.
func (r Report) Format(ceilingFile string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "import fan-out ratchet (%s): %s", r.Package, r.Line())
	failures := r.Failures()
	if len(failures) == 0 {
		b.WriteString(" — holds; every edge declared.")
		return b.String()
	}
	fmt.Fprintf(&b, "\nimport fan-out ratchet FAILED for %s", r.Package)
	for _, f := range failures {
		fmt.Fprintf(&b, "\n  - %s", f)
	}
	if ceilingFile != "" {
		fmt.Fprintf(&b, "\n  Declaration: %s. Re-pin deliberately with GRAPHI_UPDATE_GOLDEN=1 (new edges get an empty reason and a `raised_by` placeholder you must fill in), then review the diff.", ceilingFile)
	}
	return b.String()
}
