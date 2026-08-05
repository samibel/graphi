package archmatrix

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os/exec"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"sync"

	"github.com/samibel/graphi/surfaces/client"
)

// ClientPackageRelPath is where the legacy contract and its sentinels live.
const ClientPackageRelPath = "surfaces/client"

// ModuleRoot resolves the module root once via `go env GOMOD` and caches it.
var ModuleRoot = sync.OnceValues(func() (string, error) {
	out, err := exec.Command("go", "env", "GOMOD").Output()
	if err != nil {
		return "", err
	}
	gomod := strings.TrimSpace(string(out))
	if gomod == "" || gomod == "/dev/null" {
		return "", fmt.Errorf("archmatrix: no go.mod found (GOMOD=%q)", gomod)
	}
	return filepath.Dir(gomod), nil
})

// LiveMethods returns the method set of the live surfaces/client.Client contract.
// Reflection over the interface type — rather than a hand-kept list — is what
// makes the matrix unable to rot: adding a method to the legacy client without
// deciding its owning context fails the guard.
func LiveMethods() []string {
	t := reflect.TypeOf((*client.Client)(nil)).Elem()
	out := make([]string, 0, t.NumMethod())
	for i := 0; i < t.NumMethod(); i++ {
		out = append(out, t.Method(i).Name)
	}
	sort.Strings(out)
	return out
}

// LiveSentinels returns the exported error sentinels declared in the client
// package, read from source. It scans package-level `var Err… ` declarations in
// the non-test files of surfaces/client.
func LiveSentinels(moduleRoot string) ([]string, error) {
	dir := filepath.Join(moduleRoot, filepath.FromSlash(ClientPackageRelPath))
	fset := token.NewFileSet()
	pkgs, err := parser.ParseDir(fset, dir, func(fi fs.FileInfo) bool {
		return !strings.HasSuffix(fi.Name(), "_test.go")
	}, 0)
	if err != nil {
		return nil, fmt.Errorf("archmatrix: parse %s: %w", ClientPackageRelPath, err)
	}
	var out []string
	for _, pkg := range pkgs {
		for _, file := range pkg.Files {
			for _, decl := range file.Decls {
				gen, ok := decl.(*ast.GenDecl)
				if !ok || gen.Tok != token.VAR {
					continue
				}
				for _, spec := range gen.Specs {
					value, ok := spec.(*ast.ValueSpec)
					if !ok {
						continue
					}
					for _, name := range value.Names {
						if strings.HasPrefix(name.Name, "Err") && name.IsExported() {
							out = append(out, name.Name)
						}
					}
				}
			}
		}
	}
	sort.Strings(out)
	return out, nil
}

// Report is the outcome of one drift check.
type Report struct {
	// MissingMethods are live client methods with no matrix row: an undecided
	// migration target, which is exactly what this guard exists to prevent.
	MissingMethods []string
	// PhantomMethods are matrix rows for methods that no longer exist.
	PhantomMethods []string
	// MissingSentinels are live sentinels absent from the inventory.
	MissingSentinels []string
	// PhantomSentinels are inventory rows for sentinels that no longer exist.
	PhantomSentinels []string
}

// Pass reports whether the matrix and the code agree.
func (r Report) Pass() bool {
	return len(r.MissingMethods) == 0 && len(r.PhantomMethods) == 0 &&
		len(r.MissingSentinels) == 0 && len(r.PhantomSentinels) == 0
}

// Check compares the matrix against the live contract in both directions.
func Check(m Matrix, liveMethods, liveSentinels []string) Report {
	return Report{
		MissingMethods:   diff(liveMethods, m.MethodNames()),
		PhantomMethods:   diff(m.MethodNames(), liveMethods),
		MissingSentinels: diff(liveSentinels, m.SentinelNames()),
		PhantomSentinels: diff(m.SentinelNames(), liveSentinels),
	}
}

// diff returns the elements of want that are absent from have.
func diff(want, have []string) []string {
	present := make(map[string]bool, len(have))
	for _, v := range have {
		present[v] = true
	}
	var out []string
	for _, v := range want {
		if !present[v] {
			out = append(out, v)
		}
	}
	sort.Strings(out)
	return out
}

// Format renders the report for CI output.
func (r Report) Format() string {
	var b strings.Builder
	if r.Pass() {
		b.WriteString("migration-matrix check PASS — every live client method and error sentinel is inventoried.\n")
		return b.String()
	}
	b.WriteString("migration-matrix check FAIL\n")
	section := func(title string, items []string, remedy string) {
		if len(items) == 0 {
			return
		}
		fmt.Fprintf(&b, "  %s (%d):\n", title, len(items))
		for _, item := range items {
			fmt.Fprintf(&b, "    - %s\n", item)
		}
		fmt.Fprintf(&b, "    → %s\n", remedy)
	}
	section("client methods with no matrix row", r.MissingMethods,
		"add a row to "+MatrixYAMLPath+" naming the bounded context that will own it. "+
			"A method with no recorded owner is a method that silently stays in the legacy client.")
	section("matrix rows for methods that no longer exist", r.PhantomMethods,
		"remove the stale row from "+MatrixYAMLPath+" (or restore the method).")
	section("error sentinels missing from the inventory", r.MissingSentinels,
		"add them to the sentinels: block in "+MatrixYAMLPath+"; every fail-closed path must stay accounted for.")
	section("inventory rows for sentinels that no longer exist", r.PhantomSentinels,
		"remove the stale row from "+MatrixYAMLPath+" (or restore the sentinel).")
	return b.String()
}
