package main

import (
	"bytes"
	"reflect"
	"strings"
	"testing"
)

func TestFilterFirstPartyTargetsExcludesNodeModulesWithoutLosingAutoDiscovery(t *testing.T) {
	got, err := filterFirstPartyTargets([]string{
		"github.com/samibel/graphi/cmd/graphi",
		"github.com/samibel/graphi/extensions/vscode/node_modules/flatted/golang/pkg/flatted",
		"github.com/samibel/graphi/newmodule/pkg",
		"github.com/samibel/graphi/web/node_modules/flatted/golang/pkg/flatted",
	})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"github.com/samibel/graphi/cmd/graphi",
		"github.com/samibel/graphi/newmodule/pkg",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("targets = %v, want %v", got, want)
	}
}

func TestFilterFirstPartyTargetsFailsWhenOnlyDependencyTreesRemain(t *testing.T) {
	if _, err := filterFirstPartyTargets([]string{"example/node_modules/foreign"}); err == nil {
		t.Fatal("dependency-only discovery must fail closed")
	}
}

func TestRun_NonexistentTargetFailsClosed(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run(
		[]string{"-target", "./definitely-not-a-package", "-timeout", "30s"},
		strings.NewReader(""),
		&stdout,
		&stderr,
	)
	if code == 0 {
		t.Fatalf("nonexistent package returned GREEN\nstdout:\n%s\nstderr:\n%s", stdout.String(), stderr.String())
	}
	if !strings.Contains(stdout.String(), "NOT GREEN") {
		t.Fatalf("nonexistent package verdict did not name a failed gate\nstdout:\n%s\nstderr:\n%s", stdout.String(), stderr.String())
	}
}

func TestRun_StdinRequiresProducerExitCode(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run(
		[]string{"-stdin"},
		strings.NewReader(""),
		&stdout,
		&stderr,
	)
	if code == 0 || !strings.Contains(stderr.String(), "requires -producer-exit-code") {
		t.Fatalf("unverified stdin producer must fail closed: code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
}

func TestRun_StdinValidatesProducerExitCode(t *testing.T) {
	var stdout, stderr bytes.Buffer
	stream := strings.Join([]string{
		`{"Action":"start","Package":"github.com/samibel/graphi/example"}`,
		`{"Action":"pass","Package":"github.com/samibel/graphi/example"}`,
	}, "\n")
	code := run(
		[]string{"-stdin", "-producer-exit-code", "1"},
		strings.NewReader(stream),
		&stdout,
		&stderr,
	)
	if code == 0 || !strings.Contains(stdout.String(), "go test exited 1") {
		t.Fatalf("producer exit mismatch must fail closed: code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
}
