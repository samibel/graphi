package opcatalog

import (
	"sort"

	"github.com/samibel/graphi/core/registry"
)

// registryName is the short name the typed lifecycle errors carry.
const registryName = "opcatalog"

// Catalog is the set of operation specs. It is built once and frozen: after
// Build, every mutation entry point returns a registry.ErrFrozen-typed error
// (AC-1), and every accessor hands out copies, so a consumer cannot reach in
// and edit a spec that another consumer is holding.
//
// Iteration order is canonical — sorted by ID — everywhere. Nothing in graphi
// may depend on Go map order, and a catalog that projected descriptors in map
// order would produce a different tools/list on every run.
//
// # Why this reuses core/registry but not its rollback switch
//
// The typed errors and the freeze vocabulary come from core/registry (SW-222):
// there is one lifecycle language in the tree and this is it. What the catalog
// deliberately does NOT inherit is registry.Lifecycle.CheckMutable's
// GRAPHI_REGISTRY_FREEZE escape hatch. That switch exists so AX-02 could
// restore the exact pre-freeze behaviour of registries that used to be mutable
// at runtime. The catalog has no such history: it has never been mutable after
// construction, so there is no prior behaviour to roll back TO — disarming
// enforcement here would only weaken a new invariant. Frozen() is consulted
// directly.
//
// # Validate
//
// SW-222 deliberately shipped no Validate step because no registry had an
// obligation that spanned its registrants. The catalog is the first that does:
// ids must be unique across the whole set, and each spec's declared permissions
// must agree with the derivation from its ports. Build runs both.
type Catalog struct {
	lifecycle registry.Lifecycle
	byID      map[string]OperationSpec
	order     []string
}

// New returns an empty, unfrozen catalog.
func New() *Catalog {
	return &Catalog{byID: make(map[string]OperationSpec)}
}

// Frozen reports whether the catalog has been built.
func (c *Catalog) Frozen() bool { return c.lifecycle.Frozen() }

// Len reports how many specs the catalog holds.
func (c *Catalog) Len() int { return len(c.byID) }

// Policy declares the catalog's collision rule. An operation id is a frozen
// wire identifier, so a second registration is a programming fault and never an
// override: PolicyFirstWins, with no Replace entry point at all.
func (c *Catalog) Policy() registry.Policy { return registry.PolicyFirstWins }

// Add registers one spec. It fails with a registry.ErrFrozen-typed error after
// Build, with registry.ErrDuplicate when the id is already registered, and with
// a plain validation error when the spec itself is malformed.
func (c *Catalog) Add(spec OperationSpec) error {
	if c.Frozen() {
		return registry.Errorf(registry.ErrFrozen, registryName, "Add", spec.ID,
			"%s: Add %q after freeze: catalog is frozen", registryName, spec.ID)
	}
	if err := spec.Validate(); err != nil {
		return err
	}
	_, exists := c.byID[spec.ID]
	if err := registry.GuardDuplicate(c.Policy(), registryName, "operation", spec.ID, exists); err != nil {
		return err
	}
	c.byID[spec.ID] = spec
	c.order = append(c.order, spec.ID)
	sort.Strings(c.order)
	return nil
}

// Validate runs the cross-spec obligations. Add already enforces per-spec
// validity and id uniqueness, so this is the belt to that braces: it re-checks
// the whole set, which is what makes a catalog assembled by some other route
// (a future rule pack, a merged module set) safe to freeze.
func (c *Catalog) Validate() error {
	seen := make(map[string]bool, len(c.byID))
	for _, id := range c.order {
		spec, ok := c.byID[id]
		if !ok {
			return registry.Errorf(registry.ErrMissingDependency, registryName, "Validate", id,
				"%s: order lists %q but no spec is registered", registryName, id)
		}
		if seen[id] {
			return registry.Errorf(registry.ErrDuplicate, registryName, "Validate", id,
				"%s: operation %q registered twice", registryName, id)
		}
		seen[id] = true
		if err := spec.Validate(); err != nil {
			return err
		}
	}
	if len(seen) != len(c.byID) {
		return registry.Errorf(registry.ErrMissingDependency, registryName, "Validate", "",
			"%s: %d specs registered but %d in canonical order", registryName, len(c.byID), len(seen))
	}
	return nil
}

// Build validates the whole set and freezes it. It is idempotent: building an
// already-frozen catalog re-validates and succeeds, so a composition root may
// call it defensively.
func (c *Catalog) Build() (*Catalog, error) {
	if err := c.Validate(); err != nil {
		return nil, err
	}
	c.lifecycle.Freeze()
	return c, nil
}

// All returns every spec in canonical (id-sorted) order. The slice and the
// specs in it are copies; mutating them cannot reach the catalog.
func (c *Catalog) All() []OperationSpec {
	out := make([]OperationSpec, 0, len(c.order))
	for _, id := range c.order {
		out = append(out, copySpec(c.byID[id]))
	}
	return out
}

// IDs returns every operation id in canonical order.
func (c *Catalog) IDs() []string {
	out := make([]string, len(c.order))
	copy(out, c.order)
	return out
}

// Lookup returns the spec for id and whether it exists.
func (c *Catalog) Lookup(id string) (OperationSpec, bool) {
	spec, ok := c.byID[id]
	if !ok {
		return OperationSpec{}, false
	}
	return copySpec(spec), true
}

// TierOf returns the tier declared for id and whether the id is known.
func (c *Catalog) TierOf(id string) (Tier, bool) {
	spec, ok := c.byID[id]
	if !ok {
		return "", false
	}
	return spec.Tier, true
}

// VersionOf returns the contract version declared for id and whether the id is
// known.
func (c *Catalog) VersionOf(id string) (string, bool) {
	spec, ok := c.byID[id]
	if !ok {
		return "", false
	}
	return spec.Version, true
}

// Supports reports whether the catalog declares operation id AT version. It is
// the version-aware lookup an executor gates work on, and it is deliberately
// exact rather than tolerant: an empty version is "no version declared", not a
// wildcard, so a caller that omits one is rejected instead of being resolved to
// whatever the catalog happens to hold today. Accepting the omission would make
// the eventual arrival of a version "2" a silent behaviour change for every
// caller that never declared one.
func (c *Catalog) Supports(id, version string) bool {
	declared, ok := c.VersionOf(id)
	return ok && version != "" && declared == version
}

// IDsWithTier returns the canonical-order ids carrying the given tier.
func (c *Catalog) IDsWithTier(tier Tier) []string {
	out := make([]string, 0, len(c.order))
	for _, id := range c.order {
		if c.byID[id].Tier == tier {
			out = append(out, id)
		}
	}
	return out
}

// copySpec deep-copies the slice fields and the optional Stable-profile
// advertisement. The decoded JSON values (InputSchema, Annotations) are shared
// by reference — they are treated as immutable by every consumer, and cloning
// arbitrary decoded JSON on every read would cost more than it protects. The
// parity tests compare them through CanonicalJSON, which reads and never
// writes.
func copySpec(spec OperationSpec) OperationSpec {
	out := spec
	if spec.Ports != nil {
		out.Ports = append([]Port(nil), spec.Ports...)
	}
	if spec.Permissions != nil {
		out.Permissions = append([]Permission(nil), spec.Permissions...)
	}
	if spec.StableProfileAdvertisement != nil {
		advertisement := *spec.StableProfileAdvertisement
		out.StableProfileAdvertisement = &advertisement
	}
	return out
}
