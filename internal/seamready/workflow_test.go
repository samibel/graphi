package seamready_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/samibel/graphi/internal/seamready"
)

// TestAX14_AssessmentRequiresDeclaredReleaseTags enforces the input contract
// at the package that consumes it. Any CI job that reaches internal/seamready
// directly, through go test ./..., or through a harness therefore proves that
// the checkout exposes every declared release tag. The supported CI setup is
// full history; fetch-tags at the default depth does not expose historical tags.
func TestAX14_AssessmentRequiresDeclaredReleaseTags(t *testing.T) {
	root := repositoryRoot(t)
	raw, err := os.ReadFile(filepath.Join(root, seamready.DeclarationPath))
	if err != nil {
		t.Fatalf("read declaration: %v", err)
	}
	declaration, err := seamready.ParseDeclaration(raw)
	if err != nil {
		t.Fatalf("parse declaration: %v", err)
	}

	git := seamready.RepoGit(root)
	if git == nil {
		t.Fatal("cannot confirm declared tags: repository is not a git checkout")
	}
	hasTags, err := git.HasAnyTag()
	if err != nil {
		t.Fatalf("list checkout tags: %v", err)
	}
	if !hasTags {
		t.Fatal("cannot confirm declared tags: this checkout has none; actions/checkout must set fetch-depth: 0")
	}
	declared := map[string]bool{}
	for _, operation := range declaration.Operations {
		for _, tag := range operation.Criteria["c1"].ReleaseTags {
			declared[tag] = true
		}
	}
	var missing []string
	for tag := range declared {
		exists, err := git.TagExists(tag)
		if err != nil {
			t.Fatalf("confirm declared tag %q: %v", tag, err)
		}
		if !exists {
			missing = append(missing, tag)
		}
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		t.Fatalf("cannot confirm declared tags: checkout is missing %v; fetch full history", missing)
	}
}

func repositoryRoot(t *testing.T) string {
	t.Helper()
	out, err := exec.Command("go", "env", "GOMOD").Output()
	if err != nil {
		t.Fatalf("go env GOMOD: %v", err)
	}
	return filepath.Dir(strings.TrimSpace(string(out)))
}
