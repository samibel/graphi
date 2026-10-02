package mcpregistration

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/samibel/graphi/internal/mcpconfig"
)

// Reconciler applies the frozen auto-registration policy after a successful
// explicit CLI sync. It is deliberately not called by runtime ingest or MCP.
type Reconciler struct {
	StateDir string
	Binary   string
}

// ClientFailure is one independent client integration failure. A sibling
// client may still succeed, and the synchronized graph store remains valid.
type ClientFailure struct {
	ClientID   string
	ConfigPath string
	Err        error
}

// ReconcileResult separates config integration outcomes from the preceding
// sync result. Changes and failures may both be present after a partial run.
type ReconcileResult struct {
	Changes  []ServiceChange
	Failures []ClientFailure
}

type reconcileError struct{ failures []ClientFailure }

func (e reconcileError) Error() string {
	parts := make([]string, 0, len(e.failures))
	for _, failure := range e.failures {
		parts = append(parts, fmt.Sprintf("%s (%s): %v", failure.ClientID, failure.ConfigPath, failure.Err))
	}
	return "mcp registration: auto-registration failed for " + strings.Join(parts, "; ")
}

func (e reconcileError) Unwrap() []error {
	errs := make([]error, 0, len(e.failures))
	for _, failure := range e.failures {
		errs = append(errs, failure.Err)
	}
	return errs
}

// Reconcile updates only enabled, concrete policy targets. The manifest lock
// is acquired before any config lock and held for the full run, giving setup
// and concurrent syncs one consistent lock order and preventing lost entries.
func (r Reconciler) Reconcile(root string) (ReconcileResult, error) {
	if !filepath.IsAbs(r.StateDir) {
		return ReconcileResult{}, errors.New("mcp registration: state directory must be absolute")
	}
	if strings.TrimSpace(r.Binary) == "" {
		return ReconcileResult{}, errors.New("mcp registration: graphi binary is required")
	}
	// Fast no-policy path: ordinary syncs must not create a manifest lock or
	// any client/config artifact when auto-registration was never enabled.
	manifestPath := ManifestPath(r.StateDir)
	manifest, err := LoadManifest(manifestPath)
	if err != nil {
		return ReconcileResult{}, err
	}
	policies, err := enabledPolicies(manifest)
	if err != nil {
		return ReconcileResult{}, err
	}
	if len(policies) == 0 {
		return ReconcileResult{}, nil
	}
	if err := os.MkdirAll(r.StateDir, 0o700); err != nil {
		return ReconcileResult{}, fmt.Errorf("mcp registration: create state directory: %w", err)
	}
	if err := os.Chmod(r.StateDir, 0o700); err != nil {
		return ReconcileResult{}, fmt.Errorf("mcp registration: protect state directory: %w", err)
	}
	unlock, err := lockManifest(manifestPath)
	if err != nil {
		return ReconcileResult{}, err
	}
	defer unlock()

	manifest, err = LoadManifest(manifestPath)
	if err != nil {
		return ReconcileResult{}, err
	}
	policies, err = enabledPolicies(manifest)
	if err != nil {
		return ReconcileResult{}, err
	}
	if len(policies) == 0 {
		return ReconcileResult{}, nil
	}

	service := Service{StateDir: r.StateDir, Binary: r.Binary}
	result := ReconcileResult{}
	for _, policy := range policies {
		// The preceding client may have confirmed receipts or added a
		// registration. Reload under the still-held manifest lock so pending
		// recovery for this client can never save an older snapshot over it.
		manifest, err = LoadManifest(manifestPath)
		if err != nil {
			return result, err
		}
		changed, recoveryErr := recoverPolicyPending(&manifest, policy)
		if changed {
			if err := SaveManifest(manifestPath, manifest, false); err != nil {
				return result, err
			}
		}
		if recoveryErr != nil {
			result.Failures = append(result.Failures, ClientFailure{
				ClientID: policy.ClientID, ConfigPath: policy.ConfigPath, Err: recoveryErr,
			})
			continue
		}
		client, ok := mcpconfig.ClientByID(policy.ClientID)
		if !ok {
			result.Failures = append(result.Failures, ClientFailure{
				ClientID: policy.ClientID, ConfigPath: policy.ConfigPath,
				Err: fmt.Errorf("unsupported consented client %q", policy.ClientID),
			})
			continue
		}
		client = client.WithConfigPath(policy.ConfigPath)
		serviceResult, serviceErr := service.executeUnlocked(ServiceRequest{
			Roots: []string{root}, Clients: []mcpconfig.Client{client},
		})
		result.Changes = append(result.Changes, serviceResult.Changes...)
		if serviceErr != nil {
			result.Failures = append(result.Failures, ClientFailure{
				ClientID: policy.ClientID, ConfigPath: policy.ConfigPath, Err: serviceErr,
			})
		}
	}
	if len(result.Failures) != 0 {
		return result, reconcileError{failures: result.Failures}
	}
	return result, nil
}

// recoverPolicyPending completes or safely retries an interrupted write for
// this exact consented target. A third digest is a conflict and remains pending;
// no whole-config restore is attempted.
func recoverPolicyPending(manifest *Manifest, policy AutoRegisterPolicy) (bool, error) {
	var ids []string
	for _, pending := range manifest.Pending {
		if pending.Receipt.ClientID == policy.ClientID && filepath.Clean(pending.Receipt.ConfigPath) == policy.ConfigPath {
			ids = append(ids, pending.ID)
		}
	}
	changed := false
	for _, id := range ids {
		status, err := RecoverPending(manifest, id, digestConfigPath(policy.ConfigPath))
		if err != nil {
			return changed, fmt.Errorf("recover pending change %s: %w", id, err)
		}
		if status == RecoveryCompleted || status == RecoveryRetry {
			changed = true
		}
	}
	return changed, nil
}
