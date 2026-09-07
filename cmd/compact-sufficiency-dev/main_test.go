package main

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/samibel/graphi/internal/eval/retrieval"
)

func TestFlagsRefuseUnknownPhaseAndMeasurementOverrides(t *testing.T) {
	for _, args := range [][]string{
		{"unknown"}, {"prepare", "-dataset", "anything.json"}, {"prepare", "-budget", "1200"}, {"prepare", "-k", "1"},
		{"response", "-seal"}, {"decide", "-run-dir", "../outside"}, {"decide", "-run-dir", "engine"},
	} {
		if err := run(args, new(bytes.Buffer), "unused"); err == nil {
			t.Fatalf("accepted unsafe flags %v", args)
		}
	}
	var out bytes.Buffer
	if err := run([]string{"-h"}, &out, "unused"); err != nil {
		t.Fatal(err)
	}
	for _, phase := range []string{"prepare", "response", "grade", "decide"} {
		if !strings.Contains(out.String(), phase) {
			t.Fatal("help omits phase", phase)
		}
	}
}

type cliFixture struct {
	root, runDir, manifest, participants, sha string
	prepareArgs                               []string
}

func fixture(t *testing.T) cliFixture {
	t.Helper()
	sourceRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	for _, relative := range []string{devDatasetPath, devCapturePath} {
		raw, err := os.ReadFile(filepath.Join(sourceRoot, relative))
		if err != nil {
			t.Fatal(err)
		}
		writeTestFile(t, filepath.Join(root, relative), raw)
	}
	writeTestFile(t, filepath.Join(root, "candidate.go"), []byte("package fixture\n"))
	git := func(args ...string) string {
		t.Helper()
		command := exec.Command("git", args...)
		command.Dir = root
		raw, err := command.CombinedOutput()
		if err != nil {
			t.Fatalf("git fixture failed: %v %s", err, raw)
		}
		return strings.TrimSpace(string(raw))
	}
	git("init", "--quiet")
	git("add", "--", ".")
	git("-c", "user.name=Fixture", "-c", "user.email=fixture@example.invalid", "-c", "commit.gpgSign=false", "-c", "core.hooksPath=/dev/null", "commit", "--quiet", "-m", "Fixture")
	sha := git("rev-parse", "HEAD")
	runDir := "docs/eval/retrieval/runs/compact-cli-fixture"
	manifest := filepath.Join(root, runDir, "candidate-files.json")
	files := []retrieval.CompactDevCandidateFile{{Path: "candidate.go", SHA256: retrieval.SHA256Hex([]byte("package fixture\n"))}}
	raw, err := json.Marshal(files)
	if err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, manifest, raw)
	participants := filepath.Join(root, runDir, "participants.json")
	writeTestFile(t, participants, []byte(`{"primary":[{"id":"p0","provider":"fixture","model":"model"},{"id":"p1","provider":"fixture","model":"model"}],"grader":{"id":"g","provider":"fixture","model":"grader"},"adjudicator":{"id":"a","provider":"fixture","model":"model"}}`))
	return cliFixture{root: root, runDir: runDir, manifest: manifest, participants: participants, sha: sha, prepareArgs: []string{"prepare", "-run-dir", runDir, "-candidate-sha", sha, "-candidate-files", manifest, "-participants", participants}}
}

func writeTestFile(t *testing.T, path string, raw []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, raw, 0644); err != nil {
		t.Fatal(err)
	}
}

func TestPrepareResponseGradeAndDecideRemainDevOnly(t *testing.T) {
	f := fixture(t)
	var out bytes.Buffer
	if err := run(f.prepareArgs, &out, f.root); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out.String(), "QUESTION:") || !strings.Contains(out.String(), `"k":36`) {
		t.Fatal("prepare output leaks packets or has wrong threshold")
	}
	first := append([]byte(nil), out.Bytes()...)
	out.Reset()
	if err := run(f.prepareArgs, &out, f.root); err != nil {
		t.Fatal("identical prepare seal refused", err)
	}
	if !bytes.Equal(first, out.Bytes()) {
		t.Fatal("repeated prepare changed identity")
	}
	participantRaw, err := os.ReadFile(f.participants)
	if err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, f.participants, bytes.Replace(participantRaw, []byte(`"p0"`), []byte(`"replacement-session"`), 1))
	if err := run(f.prepareArgs, new(bytes.Buffer), f.root); err == nil {
		t.Fatal("changed frozen participant configuration replaced registration")
	}
	writeTestFile(t, f.participants, participantRaw)
	regRaw, err := os.ReadFile(filepath.Join(f.root, f.runDir, "pre-registration.json"))
	if err != nil {
		t.Fatal(err)
	}
	var reg retrieval.CompactDevSufficiencyRegistration
	if err := strictJSON(regRaw, &reg); err != nil {
		t.Fatal(err)
	}
	q0, q1 := reg.Queries[0].QueryID, reg.Queries[1].QueryID
	answer := filepath.Join(f.root, f.runDir, "raw", "answer.txt")
	writeTestFile(t, answer, []byte("INSUFFICIENT SECRET_RESPONSE_MARKER missing context"))
	responseArgs := []string{"response", "-run-dir", f.runDir, "-query", q0, "-slot", "0", "-status", "answered", "-response-file", answer}
	out.Reset()
	if err := run(responseArgs, &out, f.root); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out.String(), "SECRET_RESPONSE_MARKER") || !strings.Contains(out.String(), `"status":"insufficient"`) {
		t.Fatal("response leaked text or did not derive insufficiency")
	}
	if err := run(responseArgs, new(bytes.Buffer), f.root); err == nil {
		t.Fatal("response retry accepted")
	}
	writeTestFile(t, answer, []byte("Improved replacement answer"))
	if err := run(responseArgs, new(bytes.Buffer), f.root); err == nil {
		t.Fatal("response replacement accepted")
	}
	reason := filepath.Join(f.root, f.runDir, "raw", "reason.txt")
	writeTestFile(t, reason, []byte("The cited source supports the answer."))
	if err := run([]string{"grade", "-run-dir", f.runDir, "-query", q0, "-slot", "0", "-outcome", "fail", "-rationale-file", reason}, new(bytes.Buffer), f.root); err == nil {
		t.Fatal("mechanical insufficiency was gradable")
	}
	writeTestFile(t, answer, nil)
	if err := run([]string{"response", "-run-dir", f.runDir, "-query", q0, "-slot", "1", "-status", "missing", "-response-file", answer}, new(bytes.Buffer), f.root); err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, answer, []byte("A concrete answer supported by the fixture source."))
	for _, slot := range []string{"0", "1"} {
		if err := run([]string{"response", "-run-dir", f.runDir, "-query", q1, "-slot", slot, "-status", "answered", "-response-file", answer}, new(bytes.Buffer), f.root); err != nil {
			t.Fatal(err)
		}
		args := []string{"grade", "-run-dir", f.runDir, "-query", q1, "-slot", slot, "-outcome", "pass", "-rationale-file", reason}
		if err := run(args, new(bytes.Buffer), f.root); err != nil {
			t.Fatal(err)
		}
		if err := run(args, new(bytes.Buffer), f.root); err == nil {
			t.Fatal("grade retry accepted")
		}
	}
	out.Reset()
	if err := run([]string{"decide", "-run-dir", f.runDir, "-seal"}, &out, f.root); err != nil {
		t.Fatal(err)
	}
	var status map[string]any
	if err := json.Unmarshal(out.Bytes(), &status); err != nil {
		t.Fatal(err)
	}
	if status["scope"] != retrieval.CompactDevSufficiencyScope || status["n"] != float64(40) || status["passed"] != float64(1) || status["complete"] != false || status["diagnostic_pass"] != false {
		t.Fatalf("invalid outcome status %v", status)
	}
	if err := run([]string{"response", "-run-dir", f.runDir, "-query", reg.Queries[2].QueryID, "-slot", "0", "-status", "answered", "-response-file", answer}, new(bytes.Buffer), f.root); err == nil {
		t.Fatal("closed run accepted response")
	}
}

func TestCandidateBindingRejectsUnmanifestedDirtySource(t *testing.T) {
	f := fixture(t)
	if err := run(f.prepareArgs, new(bytes.Buffer), f.root); err != nil {
		t.Fatal(err)
	}
	// New raw artifacts and records are permitted only inside the chosen run.
	writeTestFile(t, filepath.Join(f.root, f.runDir, "raw", "progress.txt"), []byte("status"))
	if err := run([]string{"decide", "-run-dir", f.runDir}, new(bytes.Buffer), f.root); err != nil {
		t.Fatal("run directory growth refused", err)
	}
	writeTestFile(t, filepath.Join(f.root, "unmanifested.go"), []byte("package fixture\n"))
	if err := run([]string{"decide", "-run-dir", f.runDir}, new(bytes.Buffer), f.root); err == nil || !strings.Contains(err.Error(), "dirty outside") {
		t.Fatalf("unmanifested code drift accepted: %v", err)
	}
	if err := run(f.prepareArgs, new(bytes.Buffer), f.root); err == nil {
		t.Fatal("dirty candidate prepared")
	}
}

func TestCapturePreparationRejectsIncompleteUnknownAndSymlinkInputs(t *testing.T) {
	f := fixture(t)
	datasetRaw, err := os.ReadFile(filepath.Join(f.root, devDatasetPath))
	if err != nil {
		t.Fatal(err)
	}
	artifactRaw, err := os.ReadFile(filepath.Join(f.root, devCapturePath))
	if err != nil {
		t.Fatal(err)
	}
	real, err := retrieval.LoadPinnedRealPayloadCounter()
	if err != nil {
		t.Fatal(err)
	}
	var artifact map[string]json.RawMessage
	if err := json.Unmarshal(artifactRaw, &artifact); err != nil {
		t.Fatal(err)
	}
	artifact["independent_builds"] = json.RawMessage(`[]`)
	bad, err := json.Marshal(artifact)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := buildCaptures(datasetRaw, bad, real); err == nil {
		t.Fatal("incomplete captures accepted")
	}
	artifact["unknown"] = json.RawMessage(`true`)
	bad, err = json.Marshal(artifact)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := buildCaptures(datasetRaw, bad, real); err == nil {
		t.Fatal("unknown capture field accepted")
	}
	// Even a clean commit containing a self-consistent, edited artifact cannot
	// change the frozen input bytes for this version of the CLI.
	writeTestFile(t, filepath.Join(f.root, devCapturePath), append(artifactRaw, '\n'))
	for _, args := range [][]string{
		{"add", "--", devCapturePath},
		{"-c", "user.name=Fixture", "-c", "user.email=fixture@example.invalid", "-c", "commit.gpgSign=false", "-c", "core.hooksPath=/dev/null", "commit", "--quiet", "-m", "Edited capture fixture"},
	} {
		command := exec.Command("git", args...)
		command.Dir = f.root
		if raw, err := command.CombinedOutput(); err != nil {
			t.Fatalf("commit fixture drift: %v %s", err, raw)
		}
	}
	headCommand := exec.Command("git", "rev-parse", "HEAD")
	headCommand.Dir = f.root
	head, err := headCommand.Output()
	if err != nil {
		t.Fatal(err)
	}
	f.prepareArgs[4] = strings.TrimSpace(string(head))
	if err := run(f.prepareArgs, new(bytes.Buffer), f.root); err == nil || !strings.Contains(err.Error(), "capture artifact digest changed") {
		t.Fatalf("committed capture drift accepted: %v", err)
	}
	link := filepath.Join(f.root, f.runDir, "linked")
	if err := os.Symlink(t.TempDir(), link); err != nil {
		t.Fatal(err)
	}
	if _, err := insidePath(f.root, f.runDir+"/linked/response.txt"); err == nil {
		t.Fatal("symlink ancestor accepted")
	}
}
