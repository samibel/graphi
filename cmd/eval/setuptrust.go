package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/samibel/graphi/core/graphstore"
	"github.com/samibel/graphi/core/model"
	"github.com/samibel/graphi/internal/doctor"
	"github.com/samibel/graphi/internal/evalreport"
)

// measureSetupTrust exercises the doctor over controlled fixtures — valid /
// stale / missing MCP registration, readable / empty / missing databases —
// and asserts the PRD contract: correct statuses, an actionable next step on
// every non-pass result, byte-stable JSON, and strict read-only behavior.
// Score = passed assertions / total * 100, with each assertion recorded.
func measureSetupTrust() (float64, *evalreport.SetupTrustMetrics, error) {
	ctx := context.Background()
	m := &evalreport.SetupTrustMetrics{}
	assert := func(name string, pass bool, detail string) {
		m.Assertions = append(m.Assertions, evalreport.SetupAssertion{Name: name, Pass: pass, Detail: detail})
	}

	tmp, err := os.MkdirTemp("", "graphi-eval-doctor-*")
	if err != nil {
		return 0, nil, err
	}
	defer os.RemoveAll(tmp)
	cfgDir := filepath.Join(tmp, "client-config")
	if err := os.MkdirAll(cfgDir, 0o755); err != nil {
		return 0, nil, err
	}

	var results []doctor.CheckResult
	runCheck := func(c doctor.Check, env doctor.Env) doctor.CheckResult {
		res := c.Run(ctx, env)
		results = append(results, res)
		return res
	}

	// --- MCP registration states -----------------------------------------
	mcpStates := []struct {
		name       string
		plan       doctor.MCPPlanAction
		wantStatus doctor.Status
	}{
		{"mcp_valid_registration_passes", doctor.MCPPlanNoOp, doctor.StatusPass},
		{"mcp_stale_command_path_warns", doctor.MCPPlanUpdate, doctor.StatusWarn},
		{"mcp_missing_registration_fails", doctor.MCPPlanCreate, doctor.StatusFail},
	}
	for _, st := range mcpStates {
		env := &fixtureEnv{mcp: &fixtureMCP{configPath: filepath.Join(cfgDir, "config.json"), plan: st.plan}}
		res := runCheck(doctor.MCPCheck("graphi"), env)
		ok := res.Status == st.wantStatus
		if res.Status != doctor.StatusPass && res.Action == "" {
			ok = false
		}
		assert(st.name, ok, fmt.Sprintf("status=%s action=%q", res.Status, res.Action))
	}

	// --- Database states ---------------------------------------------------
	readableDB := filepath.Join(tmp, "readable.db")
	if err := buildReadableDB(ctx, readableDB); err != nil {
		return 0, nil, err
	}
	res := runCheck(doctor.DBCheck(), &fixtureEnv{dbPath: readableDB})
	assert("db_readable_passes", res.Status == doctor.StatusPass, fmt.Sprintf("status=%s msg=%q", res.Status, res.Message))

	emptyDB := filepath.Join(tmp, "empty.db")
	store, err := graphstore.OpenSQLite(emptyDB)
	if err != nil {
		return 0, nil, err
	}
	_ = store.Close()
	res = runCheck(doctor.DBCheck(), &fixtureEnv{dbPath: emptyDB})
	assert("db_empty_warns_with_action", res.Status == doctor.StatusWarn && res.Action != "", fmt.Sprintf("status=%s action=%q", res.Status, res.Action))

	missingDB := filepath.Join(tmp, "missing.db")
	res = runCheck(doctor.DBCheck(), &fixtureEnv{dbPath: missingDB})
	_, statErr := os.Stat(missingDB)
	assert("db_missing_fails_and_stays_absent", res.Status == doctor.StatusFail && os.IsNotExist(statErr),
		fmt.Sprintf("status=%s created=%v", res.Status, statErr == nil))

	// --- PATH / privacy / local-first contract ----------------------------
	env := &fixtureEnv{}
	for _, c := range []doctor.Check{doctor.PATHCheck(), doctor.PrivacyCheck(), doctor.LocalFirstCheck()} {
		res := runCheck(c, env)
		ok := res.Status.Valid() && res.Message != ""
		if res.Status != doctor.StatusPass && res.Status != doctor.StatusInfo && res.Action == "" {
			ok = false
		}
		assert(res.ID+"_contract", ok, fmt.Sprintf("status=%s msg=%q action=%q", res.Status, res.Message, res.Action))
	}

	// --- Every non-pass result carries an actionable next step ------------
	actionable := true
	for _, r := range results {
		if r.Status != doctor.StatusPass && r.Status != doctor.StatusInfo && r.Action == "" {
			actionable = false
		}
	}
	assert("non_pass_results_carry_actions", actionable, "")

	// --- JSON output is byte-stable ----------------------------------------
	report := doctor.Report{Results: results}
	var a, b bytes.Buffer
	if err := doctor.RenderJSON(&a, report); err != nil {
		return 0, nil, err
	}
	if err := doctor.RenderJSON(&b, report); err != nil {
		return 0, nil, err
	}
	stable := bytes.Equal(a.Bytes(), b.Bytes())
	var parsed struct {
		Outcome struct {
			Status string `json:"status"`
		} `json:"outcome"`
		Checks []struct {
			ID     string `json:"id"`
			Status string `json:"status"`
		} `json:"checks"`
	}
	if err := json.Unmarshal(a.Bytes(), &parsed); err != nil || parsed.Outcome.Status == "" || len(parsed.Checks) != len(results) {
		stable = false
	}
	assert("doctor_json_stable_and_contract_shaped", stable, "")

	// --- Fixture dir untouched (read-only proof beyond the missing DB) ----
	entries, err := os.ReadDir(cfgDir)
	assert("doctor_writes_nothing", err == nil && len(entries) == 0, "")

	passed := 0
	for _, a := range m.Assertions {
		if a.Pass {
			passed++
		}
	}
	m.Score = float64(passed) / float64(len(m.Assertions)) * 100
	return m.Score, m, nil
}

// buildReadableDB creates a real store with one node so DBCheck sees a
// populated, FTS-backed database.
func buildReadableDB(ctx context.Context, path string) error {
	store, err := graphstore.OpenSQLite(path)
	if err != nil {
		return err
	}
	defer store.Close()
	n, err := model.NewNode("function", "fix.Node", "fix/node.go", 1, 1)
	if err != nil {
		return err
	}
	if err := store.PutNode(ctx, n); err != nil {
		return err
	}
	return store.WALCheckpoint(ctx, "TRUNCATE")
}

// fixtureEnv is a minimal read-only doctor.Env over controlled fixtures.
type fixtureEnv struct {
	dbPath string
	mcp    doctor.MCPConfigReader
}

func (e *fixtureEnv) RepoRoot() string                  { return "" }
func (e *fixtureEnv) DBPath() string                    { return e.dbPath }
func (e *fixtureEnv) MCPConfig() doctor.MCPConfigReader { return e.mcp }
func (e *fixtureEnv) Release() doctor.ReleaseInfo       { return nil }
func (e *fixtureEnv) State() doctor.StateReader         { return nil }

// fixtureMCP is a single-client MCP config reader with a scripted plan action.
type fixtureMCP struct {
	configPath string
	plan       doctor.MCPPlanAction
}

func (f *fixtureMCP) Clients() []doctor.MCPClient {
	return []doctor.MCPClient{{ID: "fixture", Display: "Fixture Client", ConfigPath: f.configPath}}
}

func (f *fixtureMCP) Plan(doctor.MCPClient, string) (doctor.MCPPlanAction, error) {
	return f.plan, nil
}
