// Package extpack is graphi's declarative rule-pack mechanism — ADR 0013's
// trust tier A, and the first user-facing extension product (SW-229 / AX-09).
//
// # What a pack is, and what it can never be
//
// A pack is TWO files: a versioned manifest and one data artifact the manifest
// pins by SHA-256. Both are data. Nothing in this package compiles, links,
// evaluates or executes anything a pack ships, and nothing in this package opens
// a socket. ADR 0013's tier table states the trust assumption in one line: "the
// schema validator is trusted; the pack is not". Everything below is that
// sentence turned into code.
//
// Concretely, and each of these is pinned by an attack test:
//
//   - A pack cannot name a file to read. `artifact.path` is validated as a bare
//     filename and is used ONLY to locate the artifact next to the manifest at
//     INSTALL time, through internal/rootfile, which cannot escape the directory
//     the user pointed at. Once installed, the artifact is stored under a fixed
//     name (artifactFileName) and the pack-supplied string is never used as a
//     path again. There is no URL field, and none can be added without a schema
//     version bump: unknown manifest fields are rejected.
//   - A pack cannot exceed the size it declared. `limits.max_output_bytes` binds
//     the pack that wrote it: an artifact larger than its own declared limit is
//     refused, and the declared limit itself is capped by MaxArtifactBytes.
//   - A pack cannot be a different pack than the one the user approved. The
//     manifest hash is supplied out of band (`--sha256`) and verified BEFORE any
//     byte is written; the artifact hash is verified against the manifest, and
//     both are re-verified on every load.
//   - A pack cannot shadow a built-in or another pack. See "Collision policy".
//   - A pack cannot ask for a permission tier A does not have. `permissions`
//     accepts exactly one value, PermissionGraphRead, so a manifest declaring
//     network or exec access is a validation failure rather than a request that
//     something downstream has to remember to refuse.
//
// # Collision policy
//
// CollisionPolicy is registry.PolicyFirstWins — the SW-222 vocabulary, reused
// rather than re-invented. A pack declares every capability key it provides in
// `capabilities.provides`, and that declaration is checked against the artifact:
// a manifest that under- or over-declares what its artifact defines is refused,
// so the field is load-bearing rather than decorative.
//
// Because the policy is first-wins with NO sanctioned Replace path,
// registry.GuardReplace answers any attempt to take a key somebody already owns
// with registry.ErrUnsupportedOverride. SW-222 shipped that sentinel with no
// producer, reserved for exactly this: a pack overriding a built-in is the
// silent-shadowing threat ADR 0013 records as T5, and the tier-A answer is that
// it is not expressible. A pack ADDS capability keys; it never takes one.
//
// # Determinism
//
// Merge order is a function of the lockfile CONTENT, not of the order packs were
// installed: entries are sorted by pack id, and every rule list is emitted in
// (pack id, rule id) order. Two users who install the same packs in opposite
// orders get byte-identical results, and a test permutes install order to prove
// it. The lockfile itself serializes canonically for the same reason — it is a
// reviewable, diffable artifact that belongs in git.
//
// # Provenance
//
// Every rule carries a Ref (pack id, version, manifest SHA-256), and every
// consumer renders it into the finding it influenced. ADR 0013 D5.2 requires an
// extension-produced result to be distinguishable from a first-party one at the
// point of consumption, not only in a log. Ref fields are length-bounded on the
// way out (MaxFieldLength, the engine/trust MaxPathLength convention) because
// pack-controlled text must never reach an artifact verbatim.
//
// # Rollback
//
// An entry with `enabled: false` is not loaded at all, so a disabled pack
// restores the pre-pack behaviour exactly — ADR 0013 §4.1 records that as a
// testable contract this story owes, and set_test.go pays it by byte comparison.
// Removing a pack deletes its directory and its lockfile entry. Neither path
// touches a schema or the graph.
//
// # Layering
//
// This package sits at ENGINE rank and depends on core/registry, internal
// tooling and the standard library only. It deliberately does NOT import the
// packages that consume packs (engine/analysis/taint, engine/agenttools/
// archintel): consumers import this package, so the pack model can never acquire
// a dependency on the analyzers it parameterises.
package extpack
