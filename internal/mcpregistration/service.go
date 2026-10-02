package mcpregistration

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/samibel/graphi/internal/mcpconfig"
)

// AutoRegisterPolicy is the concrete, consented client target used by future
// explicit CLI syncs. It is local policy, not repository metadata.
type AutoRegisterPolicy struct {
	ClientID   string `json:"client_id"`
	ConfigPath string `json:"config_path"`
	Enabled    bool   `json:"enabled"`
}

// Service coordinates validated stores, stable names, ownership, client
// writers, and the local manifest. All paths and clients are injected.
type Service struct {
	StateDir string
	Binary   string
}

// ServiceRequest describes one per-repository setup operation.
type ServiceRequest struct {
	Roots               []string
	AllRepos            bool
	Clients             []mcpconfig.Client
	ExplicitName        string
	Adopt               bool
	Unregister          bool
	DryRun              bool
	EnableAutoRegister  bool
	DisableAutoRegister bool
}

// ServiceChange is one client/checkout result suitable for filtered CLI output.
type ServiceChange struct {
	CheckoutID string
	Root       string
	ClientID   string
	ConfigPath string
	ServerName string
	Result     mcpconfig.Result
}

// ServiceResult contains deterministic changes plus non-fatal invalid
// descriptor reports from --all-repos.
type ServiceResult struct {
	Changes []ServiceChange
	Issues  []error
}

// Execute performs one request. Dry-run never writes configs, locks, backups,
// the manifest, or policy.
func (s Service) Execute(request ServiceRequest) (ServiceResult, error) {
	if !filepath.IsAbs(s.StateDir) {
		return ServiceResult{}, errors.New("mcp registration: state directory must be absolute")
	}
	if len(request.Clients) == 0 {
		return ServiceResult{}, errors.New("mcp registration: at least one client is required")
	}
	clients, err := sortedClients(request.Clients)
	if err != nil {
		return ServiceResult{}, err
	}
	manifestPath := ManifestPath(s.StateDir)
	manifest, err := LoadManifest(manifestPath)
	if err != nil {
		return ServiceResult{}, err
	}
	if request.DisableAutoRegister {
		if request.DryRun {
			return ServiceResult{}, nil
		}
		for _, client := range clients {
			path, _ := client.ConfigPath()
			upsertPolicy(&manifest, AutoRegisterPolicy{ClientID: client.ID, ConfigPath: path, Enabled: false})
		}
		return ServiceResult{}, SaveManifest(manifestPath, manifest, false)
	}

	stores, issues, err := s.resolveStores(request)
	if err != nil {
		return ServiceResult{}, err
	}
	if len(stores) == 0 {
		return ServiceResult{Issues: issues}, errors.New("mcp registration: no valid existing stores selected")
	}
	for _, store := range stores {
		if err := ensureOutsideCheckout(store.CanonicalRoot, s.StateDir); err != nil {
			return ServiceResult{}, fmt.Errorf("mcp registration: state target: %w", err)
		}
		for _, client := range clients {
			path, _ := client.ConfigPath()
			if err := ensureOutsideCheckout(store.CanonicalRoot, path); err != nil {
				return ServiceResult{}, fmt.Errorf("mcp registration: config target for %s: %w", client.ID, err)
			}
		}
	}

	reserved, err := reservedNames(clients)
	if err != nil {
		return ServiceResult{}, err
	}
	if request.Adopt && request.ExplicitName != "" {
		filtered := reserved[:0]
		for _, reservedName := range reserved {
			if reservedName != request.ExplicitName {
				filtered = append(filtered, reservedName)
			}
		}
		reserved = filtered
	}
	candidates := make([]NameCandidate, 0, len(stores))
	for _, store := range stores {
		candidates = append(candidates, NameCandidate{CheckoutID: store.CheckoutID, Root: store.Root, ExplicitName: request.ExplicitName})
	}
	home, _ := os.UserHomeDir()
	names, err := AllocateNames(candidates, NameInventory{Existing: manifest.Registrations, Reserved: reserved, HomeDir: home})
	if err != nil {
		return ServiceResult{}, err
	}

	result := ServiceResult{Issues: issues}
	for _, store := range stores {
		name := names[store.CheckoutID]
		if request.Unregister {
			changes, unregisterErr := s.unregister(&manifest, manifestPath, store, name, clients, request.DryRun)
			result.Changes = append(result.Changes, changes...)
			if unregisterErr != nil {
				return result, unregisterErr
			}
			continue
		}
		entry := mcpconfig.GraphiEntry(s.Binary, []string{"mcp", "-db", store.DB, "-meta", store.Meta})
		if err := preflightOwnership(manifest, store, name, clients, entry, request.Adopt); err != nil {
			return result, err
		}
		if !request.DryRun {
			upsertRegistration(&manifest, Registration{CheckoutID: store.CheckoutID, Name: name, RepoFile: store.RepoFile})
			if err := SaveManifest(manifestPath, manifest, false); err != nil {
				return result, err
			}
		}
		for _, client := range clients {
			change, applyErr := s.applyOne(&manifest, manifestPath, store, name, client, entry, request)
			if change.ClientID != "" {
				result.Changes = append(result.Changes, change)
			}
			if applyErr != nil {
				return result, applyErr
			}
		}
	}
	if request.EnableAutoRegister && !request.DryRun {
		for _, client := range clients {
			path, _ := client.ConfigPath()
			upsertPolicy(&manifest, AutoRegisterPolicy{ClientID: client.ID, ConfigPath: path, Enabled: true})
		}
		if err := SaveManifest(manifestPath, manifest, false); err != nil {
			return result, err
		}
	}
	return result, nil
}

func (s Service) resolveStores(request ServiceRequest) ([]Store, []error, error) {
	resolver := NewResolver(s.StateDir)
	if request.AllRepos {
		return resolver.List()
	}
	stores := make([]Store, 0, len(request.Roots))
	for _, root := range request.Roots {
		store, err := resolver.Resolve(root)
		if err != nil {
			return nil, nil, err
		}
		stores = append(stores, store)
	}
	sort.Slice(stores, func(i, j int) bool { return stores[i].CheckoutID < stores[j].CheckoutID })
	return stores, nil, nil
}

func sortedClients(clients []mcpconfig.Client) ([]mcpconfig.Client, error) {
	type target struct {
		client mcpconfig.Client
		path   string
	}
	targets := make([]target, 0, len(clients))
	for _, client := range clients {
		path, err := client.ConfigPath()
		if err != nil {
			return nil, err
		}
		targets = append(targets, target{client: client, path: path})
	}
	sort.Slice(targets, func(i, j int) bool {
		if targets[i].path != targets[j].path {
			return targets[i].path < targets[j].path
		}
		return targets[i].client.ID < targets[j].client.ID
	})
	result := make([]mcpconfig.Client, len(targets))
	for index := range targets {
		result[index] = targets[index].client
	}
	return result, nil
}

func preflightOwnership(manifest Manifest, store Store, name string, clients []mcpconfig.Client, entry mcpconfig.ServerEntry, adopt bool) error {
	for _, client := range clients {
		path, err := client.ConfigPath()
		if err != nil {
			return err
		}
		plan, err := client.PlanEntryState(name, entry)
		if err != nil {
			return err
		}
		if err := AuthorizeEntry(manifest, OwnershipRequest{
			CheckoutID: store.CheckoutID, ClientID: client.ID, ConfigPath: path, ServerName: name,
			Current: plan.Current, Desired: entry, Adopt: adopt,
		}); err != nil {
			return err
		}
	}
	return nil
}

func reservedNames(clients []mcpconfig.Client) ([]string, error) {
	set := map[string]struct{}{}
	for _, client := range clients {
		entries, err := client.Entries()
		if err != nil {
			return nil, fmt.Errorf("mcp registration: read %s entries: %w", client.ID, err)
		}
		for name := range entries {
			set[name] = struct{}{}
		}
	}
	names := make([]string, 0, len(set))
	for name := range set {
		names = append(names, name)
	}
	sort.Strings(names)
	return names, nil
}

func (s Service) applyOne(manifest *Manifest, manifestPath string, store Store, name string, client mcpconfig.Client, entry mcpconfig.ServerEntry, request ServiceRequest) (ServiceChange, error) {
	path, err := client.ConfigPath()
	if err != nil {
		return ServiceChange{}, err
	}
	plan, err := client.PlanEntryState(name, entry)
	if err != nil {
		return ServiceChange{}, err
	}
	if err := AuthorizeEntry(*manifest, OwnershipRequest{
		CheckoutID: store.CheckoutID, ClientID: client.ID, ConfigPath: path, ServerName: name,
		Current: plan.Current, Desired: entry, Adopt: request.Adopt,
	}); err != nil {
		return ServiceChange{}, err
	}
	change := ServiceChange{CheckoutID: store.CheckoutID, Root: store.Root, ClientID: client.ID, ConfigPath: path, ServerName: name}
	if request.DryRun {
		change.Result, err = client.ApplyEntry(name, entry, true)
		return change, err
	}
	receipt, err := NewClientReceipt(store.CheckoutID, client.ID, path, name, entry, plan.Target)
	if err != nil {
		return ServiceChange{}, err
	}
	pending := PendingChange{
		ID:           store.CheckoutID + ":" + client.ID + ":" + name,
		BeforeDigest: plan.BeforeConfigDigest, TargetDigest: plan.TargetConfigDigest, Receipt: receipt,
	}
	upsertPending(manifest, pending)
	if err := SaveManifest(manifestPath, *manifest, false); err != nil {
		return ServiceChange{}, err
	}
	change.Result, err = client.ApplyEntry(name, entry, false)
	if err != nil {
		observedDigest := digestConfigPath(path)
		_, _ = RecoverPending(manifest, pending.ID, observedDigest)
		_ = SaveManifest(manifestPath, *manifest, false)
		return change, err
	}
	entries, err := client.Entries()
	if err != nil {
		return change, err
	}
	_, exists := entries[name]
	if !exists {
		return change, errors.New("mcp registration: client write did not produce the named entry")
	}
	observedDigest := digestConfigPath(path)
	status, err := RecoverPending(manifest, pending.ID, observedDigest)
	if err != nil {
		return change, fmt.Errorf("mcp registration: confirm client receipt: %w", err)
	}
	if status != RecoveryCompleted {
		return change, fmt.Errorf("mcp registration: confirm client receipt: unexpected recovery state %s", status)
	}
	if err := SaveManifest(manifestPath, *manifest, false); err != nil {
		return change, err
	}
	return change, nil
}

func digestConfigPath(path string) string {
	raw, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return DigestBytes(nil)
	}
	if err != nil {
		return ""
	}
	return DigestBytes(raw)
}

func (s Service) unregister(manifest *Manifest, manifestPath string, store Store, name string, clients []mcpconfig.Client, dryRun bool) ([]ServiceChange, error) {
	var changes []ServiceChange
	for _, client := range clients {
		path, _ := client.ConfigPath()
		receiptIndex := receiptIndex(*manifest, store.CheckoutID, client.ID, path, name)
		if receiptIndex < 0 {
			return changes, fmt.Errorf("mcp registration: no ownership receipt for %s/%s", client.ID, name)
		}
		entries, err := client.Entries()
		if err != nil {
			return changes, err
		}
		current, exists := entries[name]
		if !exists {
			return changes, fmt.Errorf("mcp registration: owned entry %s is missing from %s", name, client.ID)
		}
		digest, err := EntryDigest(current)
		if err != nil {
			return changes, err
		}
		if digest != manifest.Receipts[receiptIndex].EntryDigest {
			return changes, fmt.Errorf("mcp registration: %s entry %s was edited; refusing removal", client.ID, name)
		}
		result, err := client.RemoveEntry(name, dryRun)
		if err != nil {
			return changes, err
		}
		changes = append(changes, ServiceChange{CheckoutID: store.CheckoutID, Root: store.Root, ClientID: client.ID, ConfigPath: path, ServerName: name, Result: result})
		if !dryRun {
			manifest.Receipts = append(manifest.Receipts[:receiptIndex], manifest.Receipts[receiptIndex+1:]...)
			if err := SaveManifest(manifestPath, *manifest, false); err != nil {
				return changes, err
			}
		}
	}
	if !dryRun && !hasReceiptForCheckout(*manifest, store.CheckoutID) {
		removeRegistration(manifest, store.CheckoutID)
		if err := SaveManifest(manifestPath, *manifest, false); err != nil {
			return changes, err
		}
	}
	return changes, nil
}

func ensureOutsideCheckout(canonicalRoot, target string) error {
	canonicalTarget, err := canonicalPotentialPath(target)
	if err != nil {
		return err
	}
	relative, err := filepath.Rel(canonicalRoot, canonicalTarget)
	if err != nil {
		return err
	}
	outsidePrefix := ".." + string(os.PathSeparator)
	if relative == "." || (relative != ".." && !strings.HasPrefix(relative, outsidePrefix)) {
		return fmt.Errorf("%s is inside consumer repository %s", target, canonicalRoot)
	}
	return nil
}

func canonicalPotentialPath(path string) (string, error) {
	abs, err := filepath.Abs(filepath.Clean(path))
	if err != nil {
		return "", err
	}
	current := abs
	var suffix []string
	for {
		resolved, evalErr := filepath.EvalSymlinks(current)
		if evalErr == nil {
			parts := append([]string{resolved}, reverseStrings(suffix)...)
			return filepath.Clean(filepath.Join(parts...)), nil
		}
		if !os.IsNotExist(evalErr) {
			return "", evalErr
		}
		parent := filepath.Dir(current)
		if parent == current {
			return "", evalErr
		}
		suffix = append(suffix, filepath.Base(current))
		current = parent
	}
}

func reverseStrings(values []string) []string {
	result := make([]string, len(values))
	for index := range values {
		result[len(values)-1-index] = values[index]
	}
	return result
}

func upsertRegistration(manifest *Manifest, registration Registration) {
	for index, current := range manifest.Registrations {
		if current.CheckoutID == registration.CheckoutID {
			manifest.Registrations[index] = registration
			return
		}
	}
	manifest.Registrations = append(manifest.Registrations, registration)
	sort.Slice(manifest.Registrations, func(i, j int) bool {
		return manifest.Registrations[i].CheckoutID < manifest.Registrations[j].CheckoutID
	})
}

func removeRegistration(manifest *Manifest, checkoutID string) {
	for index, registration := range manifest.Registrations {
		if registration.CheckoutID == checkoutID {
			manifest.Registrations = append(manifest.Registrations[:index], manifest.Registrations[index+1:]...)
			return
		}
	}
}

func upsertPending(manifest *Manifest, pending PendingChange) {
	for index, current := range manifest.Pending {
		if current.ID == pending.ID {
			manifest.Pending[index] = pending
			return
		}
	}
	manifest.Pending = append(manifest.Pending, pending)
}

func upsertPolicy(manifest *Manifest, policy AutoRegisterPolicy) {
	for index, current := range manifest.Policies {
		if current.ClientID == policy.ClientID && current.ConfigPath == policy.ConfigPath {
			manifest.Policies[index] = policy
			return
		}
	}
	manifest.Policies = append(manifest.Policies, policy)
	sort.Slice(manifest.Policies, func(i, j int) bool {
		if manifest.Policies[i].ClientID != manifest.Policies[j].ClientID {
			return manifest.Policies[i].ClientID < manifest.Policies[j].ClientID
		}
		return manifest.Policies[i].ConfigPath < manifest.Policies[j].ConfigPath
	})
}

func receiptIndex(manifest Manifest, checkoutID, clientID, configPath, name string) int {
	for index, receipt := range manifest.Receipts {
		if receipt.CheckoutID == checkoutID && receipt.ClientID == clientID && receipt.ConfigPath == configPath && receipt.ServerName == name {
			return index
		}
	}
	return -1
}

func hasReceiptForCheckout(manifest Manifest, checkoutID string) bool {
	for _, receipt := range manifest.Receipts {
		if receipt.CheckoutID == checkoutID {
			return true
		}
	}
	return false
}
