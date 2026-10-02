package mcpregistration

import (
	"fmt"
	"path/filepath"
	"sort"
)

// enabledPolicies returns the exact client/config pairs the user previously
// consented to. It never discovers newly installed clients or substitutes a
// client's newly resolved default path.
func enabledPolicies(manifest Manifest) ([]AutoRegisterPolicy, error) {
	seen := make(map[string]struct{})
	policies := make([]AutoRegisterPolicy, 0, len(manifest.Policies))
	for _, policy := range manifest.Policies {
		if !policy.Enabled {
			continue
		}
		if policy.ClientID == "" {
			return nil, fmt.Errorf("mcp registration: auto-register policy has no client id")
		}
		if !filepath.IsAbs(policy.ConfigPath) {
			return nil, fmt.Errorf("mcp registration: auto-register target for %s must be absolute", policy.ClientID)
		}
		key := policy.ClientID + "\x00" + filepath.Clean(policy.ConfigPath)
		if _, duplicate := seen[key]; duplicate {
			return nil, fmt.Errorf("mcp registration: duplicate auto-register target for %s", policy.ClientID)
		}
		seen[key] = struct{}{}
		policy.ConfigPath = filepath.Clean(policy.ConfigPath)
		policies = append(policies, policy)
	}
	sort.Slice(policies, func(i, j int) bool {
		if policies[i].ConfigPath != policies[j].ConfigPath {
			return policies[i].ConfigPath < policies[j].ConfigPath
		}
		return policies[i].ClientID < policies[j].ClientID
	})
	return policies, nil
}
