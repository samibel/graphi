package mcpregistration

import (
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
)

const maxDefaultNameBytes = 48

// NameCandidate describes an unregistered checkout that needs a server name.
type NameCandidate struct {
	CheckoutID   string
	Root         string
	ExplicitName string
}

// NameInventory supplies persistent registrations and names owned by foreign
// config entries. HomeDir prevents the user-home basename becoming a suffix.
type NameInventory struct {
	Existing []Registration
	Reserved []string
	HomeDir  string
}

// AllocateNames deterministically allocates names in CheckoutID order. Existing
// registrations always retain their names; newly encountered collisions add
// readable parent components and finally a checkout fingerprint suffix.
func AllocateNames(candidates []NameCandidate, inventory NameInventory) (map[string]string, error) {
	result := make(map[string]string, len(candidates))
	used := make(map[string]string, len(inventory.Existing)+len(inventory.Reserved))
	existingByID := make(map[string]Registration, len(inventory.Existing))
	for _, registration := range inventory.Existing {
		if registration.CheckoutID == "" || registration.Name == "" {
			return nil, errors.New("mcp registration: existing registration has an empty identity or name")
		}
		if owner, exists := used[registration.Name]; exists && owner != registration.CheckoutID {
			return nil, fmt.Errorf("mcp registration: existing name %q has multiple owners", registration.Name)
		}
		used[registration.Name] = registration.CheckoutID
		existingByID[registration.CheckoutID] = registration
	}
	for _, name := range inventory.Reserved {
		if name == "" {
			continue
		}
		if _, exists := used[name]; !exists {
			used[name] = "<foreign>"
		}
	}

	ordered := append([]NameCandidate(nil), candidates...)
	sort.SliceStable(ordered, func(i, j int) bool { return ordered[i].CheckoutID < ordered[j].CheckoutID })
	seen := make(map[string]struct{}, len(ordered))
	for _, candidate := range ordered {
		if candidate.CheckoutID == "" {
			return nil, errors.New("mcp registration: empty checkout identity")
		}
		if _, duplicate := seen[candidate.CheckoutID]; duplicate {
			return nil, fmt.Errorf("mcp registration: duplicate checkout identity %q", candidate.CheckoutID)
		}
		seen[candidate.CheckoutID] = struct{}{}

		if existing, ok := existingByID[candidate.CheckoutID]; ok {
			if candidate.ExplicitName != "" && candidate.ExplicitName != existing.Name {
				return nil, fmt.Errorf("mcp registration: checkout %s is already named %q", candidate.CheckoutID, existing.Name)
			}
			result[candidate.CheckoutID] = existing.Name
			continue
		}

		if candidate.ExplicitName != "" {
			if err := validateExplicitName(candidate.ExplicitName); err != nil {
				return nil, err
			}
			if owner, exists := used[candidate.ExplicitName]; exists {
				return nil, fmt.Errorf("mcp registration: explicit name %q conflicts with %s", candidate.ExplicitName, owner)
			}
			used[candidate.ExplicitName] = candidate.CheckoutID
			result[candidate.CheckoutID] = candidate.ExplicitName
			continue
		}

		name, err := allocateDefaultName(candidate, used, inventory.HomeDir)
		if err != nil {
			return nil, err
		}
		used[name] = candidate.CheckoutID
		result[candidate.CheckoutID] = name
	}
	return result, nil
}

func validateExplicitName(name string) error {
	if !strings.HasPrefix(name, "graphi-") || len(name) == len("graphi-") {
		return errors.New("mcp registration: explicit name must begin with graphi- and include a suffix")
	}
	for _, c := range name[len("graphi-"):] {
		if (c < 'a' || c > 'z') && (c < '0' || c > '9') && c != '-' {
			return fmt.Errorf("mcp registration: explicit name %q must use lowercase ASCII letters, digits, and hyphens", name)
		}
	}
	return nil
}

func allocateDefaultName(candidate NameCandidate, used map[string]string, home string) (string, error) {
	root := filepath.Clean(candidate.Root)
	base := slug(filepath.Base(root))
	if name := composeDefaultName(base, nil); nameAvailable(name, used) {
		return name, nil
	}

	home = filepath.Clean(home)
	homeBase := filepath.Base(home)
	var qualifiers []string
	for parent := filepath.Dir(root); parent != filepath.Dir(parent); parent = filepath.Dir(parent) {
		if home != "." && parent == home {
			break
		}
		raw := filepath.Base(parent)
		if homeBase != "." && raw == homeBase {
			continue
		}
		qualifiers = append(qualifiers, slug(raw))
		if name := composeDefaultName(base, qualifiers); nameAvailable(name, used) {
			return name, nil
		}
	}

	idSlug := slug(candidate.CheckoutID)
	if idSlug == "repo" {
		return "", fmt.Errorf("mcp registration: checkout identity %q cannot form a unique suffix", candidate.CheckoutID)
	}
	for length := 6; length <= len(idSlug); length += 2 {
		if length > len(idSlug) {
			length = len(idSlug)
		}
		name := composeDefaultName(base, []string{idSlug[:length]})
		if nameAvailable(name, used) {
			return name, nil
		}
	}
	if len(idSlug)%2 != 0 {
		name := composeDefaultName(base, []string{idSlug})
		if nameAvailable(name, used) {
			return name, nil
		}
	}
	return "", fmt.Errorf("mcp registration: cannot allocate a unique name for checkout %s", candidate.CheckoutID)
}

func nameAvailable(name string, used map[string]string) bool {
	_, exists := used[name]
	return !exists
}

func composeDefaultName(base string, qualifiers []string) string {
	const prefix = "graphi-"
	qualifier := ""
	if len(qualifiers) > 0 {
		qualifier = "-" + strings.Join(qualifiers, "-")
	}
	room := maxDefaultNameBytes - len(prefix) - len(qualifier)
	if room < 1 {
		// Parent context can itself be arbitrarily long. Preserve its tail and
		// leave one byte for the checkout slug.
		maxQualifier := maxDefaultNameBytes - len(prefix) - 2
		if maxQualifier < 1 {
			maxQualifier = 1
		}
		q := strings.Trim(strings.Join(qualifiers, "-"), "-")
		if len(q) > maxQualifier {
			q = q[len(q)-maxQualifier:]
			q = strings.Trim(q, "-")
		}
		qualifier = "-" + q
		room = maxDefaultNameBytes - len(prefix) - len(qualifier)
	}
	if len(base) > room {
		base = strings.Trim(base[:room], "-")
	}
	if base == "" {
		base = "r"
	}
	return prefix + base + qualifier
}

func slug(value string) string {
	value = strings.ToLower(value)
	var b strings.Builder
	separator := false
	for _, c := range value {
		if (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') {
			if separator && b.Len() > 0 {
				b.WriteByte('-')
			}
			b.WriteRune(c)
			separator = false
		} else {
			separator = true
		}
	}
	result := strings.Trim(b.String(), "-")
	if result == "" {
		return "repo"
	}
	return result
}
