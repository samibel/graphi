package seamready_test

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

type workflowDefinition struct {
	Jobs map[string]workflowJob `yaml:"jobs"`
}

type workflowJob struct {
	Steps []workflowStep `yaml:"steps"`
}

type workflowStep struct {
	Uses string         `yaml:"uses"`
	With map[string]any `yaml:"with"`
	Run  string         `yaml:"run"`
}

// TestAX14_AssessmentWorkflowsFetchReleaseTags protects the live git input
// behind c5. A default actions/checkout clone has no tags, so TagExists cannot
// distinguish a missing declaration tag from an incomplete checkout and the
// assessment reports UNKNOWN. Every CI job that executes seamready (directly,
// through the full suite, or through release-gate's testgate) must therefore
// fetch tags or full history.
func TestAX14_AssessmentWorkflowsFetchReleaseTags(t *testing.T) {
	root := repositoryRoot(t)
	tests := []struct {
		workflow string
		job      string
		command  string
	}{
		{workflow: "testgate.yml", job: "test-gate", command: "./cmd/testgate"},
		{workflow: "release.yml", job: "workspace-build-test", command: "go test ./..."},
		{workflow: "release-gate.yml", job: "release-gate", command: "./cmd/release-gate"},
		{workflow: "lint.yml", job: "race", command: "go test -race"},
		{workflow: "release-dag.yml", job: "gate", command: "./cmd/release-gate"},
	}

	for _, tt := range tests {
		t.Run(tt.workflow+"/"+tt.job, func(t *testing.T) {
			path := filepath.Join(root, ".github", "workflows", tt.workflow)
			raw, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read workflow: %v", err)
			}
			var definition workflowDefinition
			if err := yaml.Unmarshal(raw, &definition); err != nil {
				t.Fatalf("parse workflow: %v", err)
			}
			job, ok := definition.Jobs[tt.job]
			if !ok {
				t.Fatalf("job %q is missing", tt.job)
			}

			var checkout *workflowStep
			assessmentRuns := false
			for i := range job.Steps {
				step := &job.Steps[i]
				if strings.HasPrefix(step.Uses, "actions/checkout@") {
					checkout = step
				}
				if strings.Contains(step.Run, tt.command) {
					assessmentRuns = true
				}
			}
			if !assessmentRuns {
				t.Fatalf("job no longer runs %q; review whether it still executes seamready", tt.command)
			}
			if checkout == nil {
				t.Fatal("job has no actions/checkout step")
			}
			if !checkoutHasTags(checkout.With) {
				t.Fatalf("actions/checkout does not fetch tags: with=%v", checkout.With)
			}
		})
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

func checkoutHasTags(with map[string]any) bool {
	if fetchTags, ok := with["fetch-tags"].(bool); ok && fetchTags {
		return true
	}
	return fmt.Sprint(with["fetch-depth"]) == "0"
}
