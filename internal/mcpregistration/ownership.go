package mcpregistration

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sort"

	"github.com/samibel/graphi/internal/mcpconfig"
)

// ManagedSnapshot stores the non-secret fields Graphi last wrote. Environment
// values are represented only by digests so the registration manifest never
// becomes a token store.
type ManagedSnapshot struct {
	Command    string            `json:"command"`
	Args       []string          `json:"args,omitempty"`
	EnvDigests map[string]string `json:"env_digests,omitempty"`
}

// ClientReceipt proves which named entry Graphi manages for one checkout and
// client target. EntryDigest covers the complete post-write entry for guarded
// removal, while ManagedDigest permits unknown/manual fields to coexist.
type ClientReceipt struct {
	CheckoutID    string          `json:"checkout_id"`
	ClientID      string          `json:"client_id"`
	ConfigPath    string          `json:"config_path"`
	ServerName    string          `json:"server_name"`
	Managed       ManagedSnapshot `json:"managed"`
	ManagedDigest string          `json:"managed_digest"`
	EntryDigest   string          `json:"entry_digest"`
}

// PendingChange records the observed pre-write digest and intended target
// digest before a config update. It contains only hashes and the receipt that
// may be confirmed after recovery.
type PendingChange struct {
	ID           string        `json:"id"`
	BeforeDigest string        `json:"before_digest"`
	TargetDigest string        `json:"target_digest"`
	Receipt      ClientReceipt `json:"receipt"`
}

// OwnershipRequest describes an authorization check immediately before a
// named config entry is changed or explicitly adopted.
type OwnershipRequest struct {
	CheckoutID string
	ClientID   string
	ConfigPath string
	ServerName string
	Current    any
	Desired    mcpconfig.ServerEntry
	Adopt      bool
}

// AuthorizeEntry refuses to manage a pre-existing manual entry unless the user
// explicitly adopts it and its managed fields already bind to the selected
// store. A prior receipt authorizes only while those managed fields still match.
func AuthorizeEntry(manifest Manifest, request OwnershipRequest) error {
	receipt, owned := findReceipt(manifest, request)
	if owned {
		if request.Current == nil {
			return errors.New("mcp registration: owned entry is missing")
		}
		managed, err := snapshotCurrent(request.Current, receipt.Managed.EnvDigests)
		if err != nil {
			return fmt.Errorf("mcp registration: inspect owned entry: %w", err)
		}
		digest, err := digestJSON(managed)
		if err != nil {
			return err
		}
		if digest != receipt.ManagedDigest {
			return errors.New("mcp registration: managed entry was edited; refusing to overwrite")
		}
		return nil
	}
	if request.Current == nil {
		return nil
	}
	if !request.Adopt {
		return errors.New("mcp registration: existing manual entry requires explicit adoption")
	}
	if err := managedFieldsMatch(request.Current, request.Desired); err != nil {
		return fmt.Errorf("mcp registration: cannot adopt entry: %w", err)
	}
	return nil
}

func findReceipt(manifest Manifest, request OwnershipRequest) (ClientReceipt, bool) {
	for _, receipt := range manifest.Receipts {
		if receipt.CheckoutID == request.CheckoutID && receipt.ClientID == request.ClientID &&
			receipt.ConfigPath == request.ConfigPath && receipt.ServerName == request.ServerName {
			return receipt, true
		}
	}
	return ClientReceipt{}, false
}

// NewClientReceipt creates a receipt from the selected managed fields and the
// complete final entry. The latter is stored only as a digest.
func NewClientReceipt(checkoutID, clientID, configPath, serverName string, desired mcpconfig.ServerEntry, finalEntry any) (ClientReceipt, error) {
	managed := snapshotDesired(desired)
	managedDigest, err := digestJSON(managed)
	if err != nil {
		return ClientReceipt{}, err
	}
	entryDigest, err := EntryDigest(finalEntry)
	if err != nil {
		return ClientReceipt{}, err
	}
	return ClientReceipt{
		CheckoutID: checkoutID, ClientID: clientID, ConfigPath: configPath, ServerName: serverName,
		Managed: managed, ManagedDigest: managedDigest, EntryDigest: entryDigest,
	}, nil
}

func snapshotDesired(entry mcpconfig.ServerEntry) ManagedSnapshot {
	envDigests := make(map[string]string, len(entry.Env))
	for key, value := range entry.Env {
		envDigests[key] = DigestBytes([]byte(value))
	}
	if len(envDigests) == 0 {
		envDigests = nil
	}
	return ManagedSnapshot{Command: entry.Command, Args: append([]string(nil), entry.Args...), EnvDigests: envDigests}
}

func snapshotCurrent(raw any, expectedEnv map[string]string) (ManagedSnapshot, error) {
	entry, err := genericEntry(raw)
	if err != nil {
		return ManagedSnapshot{}, err
	}
	envDigests := make(map[string]string, len(expectedEnv))
	for key := range expectedEnv {
		value, ok := entry.Env[key]
		if !ok {
			return ManagedSnapshot{}, fmt.Errorf("managed environment key %q is missing", key)
		}
		envDigests[key] = DigestBytes([]byte(value))
	}
	if len(envDigests) == 0 {
		envDigests = nil
	}
	return ManagedSnapshot{Command: entry.Command, Args: entry.Args, EnvDigests: envDigests}, nil
}

func managedFieldsMatch(raw any, desired mcpconfig.ServerEntry) error {
	current, err := genericEntry(raw)
	if err != nil {
		return err
	}
	if current.Command != desired.Command {
		return errors.New("command does not match the selected Graphi binary")
	}
	if !reflect.DeepEqual(current.Args, desired.Args) {
		return errors.New("arguments do not bind the selected Graphi store")
	}
	for key, value := range desired.Env {
		if current.Env[key] != value {
			return fmt.Errorf("managed environment key %q does not match", key)
		}
	}
	return nil
}

type decodedEntry struct {
	Command string
	Args    []string
	Env     map[string]string
}

func genericEntry(raw any) (decodedEntry, error) {
	object, ok := raw.(map[string]any)
	if !ok {
		return decodedEntry{}, errors.New("entry is not an object")
	}
	command, ok := object["command"].(string)
	if !ok || command == "" {
		return decodedEntry{}, errors.New("entry has no command")
	}
	var args []string
	if rawArgs, exists := object["args"]; exists {
		switch values := rawArgs.(type) {
		case []any:
			args = make([]string, len(values))
			for index, value := range values {
				arg, ok := value.(string)
				if !ok {
					return decodedEntry{}, errors.New("entry args contain a non-string value")
				}
				args[index] = arg
			}
		case []string:
			args = append([]string(nil), values...)
		default:
			return decodedEntry{}, errors.New("entry args are not an array")
		}
	}
	env := map[string]string{}
	if rawEnv, exists := object["env"]; exists {
		values, ok := rawEnv.(map[string]any)
		if !ok {
			if typed, typedOK := rawEnv.(map[string]string); typedOK {
				for key, value := range typed {
					env[key] = value
				}
			} else {
				return decodedEntry{}, errors.New("entry env is not an object")
			}
		} else {
			for key, value := range values {
				text, ok := value.(string)
				if !ok {
					return decodedEntry{}, fmt.Errorf("entry env key %q is not a string", key)
				}
				env[key] = text
			}
		}
	}
	return decodedEntry{Command: command, Args: args, Env: env}, nil
}

// EntryDigest returns a stable digest of a complete JSON-compatible entry.
func EntryDigest(entry any) (string, error) { return digestJSON(entry) }

func digestJSON(value any) (string, error) {
	b, err := json.Marshal(value)
	if err != nil {
		return "", fmt.Errorf("mcp registration: digest JSON: %w", err)
	}
	return DigestBytes(b), nil
}

// DigestBytes returns a SHA-256 digest suitable for pending-state comparison.
func DigestBytes(value []byte) string {
	sum := sha256.Sum256(value)
	return hex.EncodeToString(sum[:])
}

// RecoveryStatus classifies a pending write observed on restart.
type RecoveryStatus string

const (
	RecoveryCompleted RecoveryStatus = "completed"
	RecoveryRetry     RecoveryStatus = "retry"
	RecoveryConflict  RecoveryStatus = "conflict"
)

// RecoverPending reconciles one pending change against the current config
// digest. It never restores whole configs: target confirms ownership, before
// permits a safe re-plan, and every third state remains pending as a conflict.
func RecoverPending(manifest *Manifest, id, observedDigest string) (RecoveryStatus, error) {
	if manifest == nil {
		return RecoveryConflict, errors.New("mcp registration: nil manifest")
	}
	for index, pending := range manifest.Pending {
		if pending.ID != id {
			continue
		}
		switch observedDigest {
		case pending.TargetDigest:
			upsertReceipt(manifest, pending.Receipt)
			manifest.Pending = append(manifest.Pending[:index], manifest.Pending[index+1:]...)
			return RecoveryCompleted, nil
		case pending.BeforeDigest:
			manifest.Pending = append(manifest.Pending[:index], manifest.Pending[index+1:]...)
			return RecoveryRetry, nil
		default:
			return RecoveryConflict, errors.New("mcp registration: pending config changed externally")
		}
	}
	return RecoveryConflict, fmt.Errorf("mcp registration: pending change %q not found", id)
}

func upsertReceipt(manifest *Manifest, receipt ClientReceipt) {
	for index, current := range manifest.Receipts {
		if current.CheckoutID == receipt.CheckoutID && current.ClientID == receipt.ClientID &&
			current.ConfigPath == receipt.ConfigPath && current.ServerName == receipt.ServerName {
			manifest.Receipts[index] = receipt
			return
		}
	}
	manifest.Receipts = append(manifest.Receipts, receipt)
	sort.Slice(manifest.Receipts, func(i, j int) bool {
		a, b := manifest.Receipts[i], manifest.Receipts[j]
		if a.CheckoutID != b.CheckoutID {
			return a.CheckoutID < b.CheckoutID
		}
		if a.ClientID != b.ClientID {
			return a.ClientID < b.ClientID
		}
		if a.ConfigPath != b.ConfigPath {
			return a.ConfigPath < b.ConfigPath
		}
		return a.ServerName < b.ServerName
	})
}
