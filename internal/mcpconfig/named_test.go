package mcpconfig

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func TestNamedEntryCreateAndNoOp(t *testing.T) {
	path := filepath.Join(t.TempDir(), "client.json")
	client := fakeClient("test", "mcpServers", path)
	entry := GraphiEntry("/opt/graphi", []string{"mcp", "-db", "/state/a/db.sqlite", "-meta", "/state/a/meta"})

	created, err := client.ApplyEntry("graphi-service", entry, false)
	if err != nil || created.Action != ActionCreated {
		t.Fatalf("create = (%s, %v), want created", created.Action, err)
	}
	before := readBytes(t, path)
	unchanged, err := client.ApplyEntry("graphi-service", entry, false)
	if err != nil || unchanged.Action != ActionUnchanged {
		t.Fatalf("second apply = (%s, %v), want unchanged", unchanged.Action, err)
	}
	if got := readBytes(t, path); string(got) != string(before) {
		t.Fatal("unchanged named apply rewrote the config")
	}
}

func TestAuthorizedUpsertRejectsEditBeforeWriterLock(t *testing.T) {
	path := filepath.Join(t.TempDir(), "client.json")
	client := fakeClient("test", "mcpServers", path)
	entry := GraphiEntry("/opt/graphi", []string{"mcp", "-db", "/state/a/db.sqlite", "-meta", "/state/a/meta"})
	plan, err := client.PlanEntryState("graphi-service", entry)
	if err != nil {
		t.Fatal(err)
	}
	external := []byte(`{"mcpServers":{"foreign":{"command":"manual"}}}`)
	if err := os.WriteFile(path, external, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := client.ApplyEntryObserved("graphi-service", entry, plan.BeforeConfigDigest, false); err == nil {
		t.Fatal("observed upsert overwrote a config edited after authorization")
	}
	if got := readBytes(t, path); string(got) != string(external) {
		t.Fatalf("external edit changed after refused upsert: %s", got)
	}
}

func TestAuthorizedRemovalRejectsEditBeforeWriterLock(t *testing.T) {
	path := filepath.Join(t.TempDir(), "client.json")
	client := fakeClient("test", "mcpServers", path)
	entry := GraphiEntry("/opt/graphi", []string{"mcp", "-db", "/state/a/db.sqlite", "-meta", "/state/a/meta"})
	if _, err := client.ApplyEntry("graphi-service", entry, false); err != nil {
		t.Fatal(err)
	}
	plan, err := client.PlanRemoveEntryState("graphi-service")
	if err != nil {
		t.Fatal(err)
	}
	external := []byte(`{"mcpServers":{"graphi-service":{"command":"/manual/edit","args":["mcp"]}}}`)
	if err := os.WriteFile(path, external, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := client.RemoveEntryObserved("graphi-service", plan.Current, plan.BeforeConfigDigest, false); err == nil {
		t.Fatal("observed removal deleted an entry edited after authorization")
	}
	if got := readBytes(t, path); string(got) != string(external) {
		t.Fatalf("external edit changed after refused removal: %s", got)
	}
}

func TestPreserveForeignServersAndFields(t *testing.T) {
	path := filepath.Join(t.TempDir(), "client.json")
	original := `{
  "mcpServers": {
    "foreign": {"command":"other","custom":{"limit":3}},
    "graphi-service": {
      "type":"stdio",
      "command":"/old/graphi",
      "args":["mcp","-db","/old/db"],
      "env":{"FOREIGN_TOKEN":"keep-secret","GRAPHI_PROFILE":"old"},
      "permissions":{"allow":["query"]},
      "customFlag":true
    }
  },
  "theme":"dark"
}`
	if err := os.WriteFile(path, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}
	entry := GraphiEntry("/new/graphi", []string{"mcp", "-db", "/state/service/db.sqlite"})
	entry.Env = map[string]string{"GRAPHI_PROFILE": "fast"}
	if _, err := Apply(path, "graphi-service", entry, false); err != nil {
		t.Fatalf("apply: %v", err)
	}
	doc, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if doc["theme"] != "dark" {
		t.Fatal("top-level foreign field was lost")
	}
	servers := doc["mcpServers"].(map[string]any)
	if _, ok := servers["foreign"]; !ok {
		t.Fatal("foreign server was lost")
	}
	managed := servers["graphi-service"].(map[string]any)
	if managed["customFlag"] != true || managed["permissions"] == nil {
		t.Fatalf("unknown managed-entry fields were lost: %#v", managed)
	}
	env := managed["env"].(map[string]any)
	if env["FOREIGN_TOKEN"] != "keep-secret" || env["GRAPHI_PROFILE"] != "fast" {
		t.Fatalf("environment merge = %#v", env)
	}
}

func TestPreserveDisabledState(t *testing.T) {
	path := filepath.Join(t.TempDir(), "client.json")
	writeJSON(t, path, map[string]any{"mcpServers": map[string]any{
		"graphi-service": map[string]any{"command": "/old/graphi", "args": []string{"mcp"}, "disabled": true},
	}})
	if _, err := Apply(path, "graphi-service", GraphiEntry("/new/graphi", nil), false); err != nil {
		t.Fatal(err)
	}
	doc, _ := Load(path)
	entry := doc["mcpServers"].(map[string]any)["graphi-service"].(map[string]any)
	if entry["disabled"] != true {
		t.Fatalf("disabled state changed: %#v", entry)
	}
}

func TestUnknownJSONNumbersRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "client.json")
	const number = "900719925474099312345678901234567890"
	original := `{"telemetry":{"sequence":` + number + `},"mcpServers":{}}`
	if err := os.WriteFile(path, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Apply(path, "graphi-service", GraphiEntry("/bin/graphi", nil), false); err != nil {
		t.Fatal(err)
	}
	if got := string(readBytes(t, path)); !strings.Contains(got, number) {
		t.Fatalf("unknown integer changed during round-trip:\n%s", got)
	}
}

func TestInvalidConfigUnchanged(t *testing.T) {
	path := filepath.Join(t.TempDir(), "client.json")
	original := []byte(`{"mcpServers": ["ambiguous"]}`)
	if err := os.WriteFile(path, original, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Apply(path, "graphi-service", GraphiEntry("/bin/graphi", nil), false); err == nil {
		t.Fatal("ambiguous server container was accepted")
	}
	if got := readBytes(t, path); string(got) != string(original) {
		t.Fatal("invalid config changed")
	}
}

func TestDryRunWritesNothing(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "missing")
	path := filepath.Join(dir, "client.json")
	if _, err := Apply(path, "graphi-service", GraphiEntry("/bin/graphi", nil), true); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("dry-run created filesystem state: %v", err)
	}
}

func TestBackupFailureAborts(t *testing.T) {
	path := filepath.Join(t.TempDir(), "client.json")
	original := []byte(`{"mcpServers":{}}`)
	if err := os.WriteFile(path, original, 0o600); err != nil {
		t.Fatal(err)
	}
	wantErr := errors.New("synthetic backup failure")
	_, err := applyKeyWithHooks(path, "mcpServers", "graphi-service", GraphiEntry("/bin/graphi", nil), false, writerHooks{
		backup: func(string, []byte) (string, error) { return "", wantErr },
	})
	if !errors.Is(err, wantErr) {
		t.Fatalf("apply error = %v, want synthetic backup failure", err)
	}
	if got := readBytes(t, path); string(got) != string(original) {
		t.Fatal("backup failure modified live config")
	}
}

func TestConcurrentGraphiWriters(t *testing.T) {
	path := filepath.Join(t.TempDir(), "client.json")
	start := make(chan struct{})
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for _, name := range []string{"graphi-alpha", "graphi-beta"} {
		name := name
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_, err := Apply(path, name, GraphiEntry("/bin/graphi", []string{"mcp", "-db", "/state/" + name}), false)
			errs <- err
		}()
	}
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("concurrent apply: %v", err)
		}
	}
	doc, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	servers := doc["mcpServers"].(map[string]any)
	if servers["graphi-alpha"] == nil || servers["graphi-beta"] == nil {
		t.Fatalf("concurrent update was lost: %#v", servers)
	}
}

func TestObservedExternalEditAborts(t *testing.T) {
	path := filepath.Join(t.TempDir(), "client.json")
	original := []byte(`{"mcpServers":{},"theme":"dark"}`)
	external := []byte(`{"mcpServers":{},"theme":"light","external":true}`)
	if err := os.WriteFile(path, original, 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := applyKeyWithHooks(path, "mcpServers", "graphi-service", GraphiEntry("/bin/graphi", nil), false, writerHooks{
		beforeCommit: func(string) error { return os.WriteFile(path, external, 0o600) },
	})
	if err == nil {
		t.Fatal("observed external edit did not abort")
	}
	if got := readBytes(t, path); string(got) != string(external) {
		t.Fatalf("external edit was overwritten:\n%s", got)
	}
}

func TestConfigDiffRedactsSecrets(t *testing.T) {
	path := filepath.Join(t.TempDir(), "client.json")
	entry := GraphiEntry("/private/bin/graphi", []string{"mcp", "--token", "super-secret-token"})
	entry.Env = map[string]string{"API_TOKEN": "another-secret"}
	result, err := Apply(path, "graphi-service", entry, true)
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"super-secret-token", "another-secret", "/private/bin/graphi"} {
		if strings.Contains(result.Diff, secret) {
			t.Fatalf("diff leaked %q: %s", secret, result.Diff)
		}
	}
	if !strings.Contains(result.Diff, "graphi-service") || !strings.Contains(result.Diff, "created") {
		t.Fatalf("redacted diff lacks useful plan metadata: %s", result.Diff)
	}
}
