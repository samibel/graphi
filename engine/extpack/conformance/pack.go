package conformance

import (
	"bytes"
	"fmt"
	"os"
	"strings"

	"github.com/samibel/graphi/engine/extpack"
)

// VerifyPack runs the tier-A contract checks over a declarative rule pack.
//
// path is a manifest file or the directory holding one, which is what
// `graphi extension init` produces and what a pack author has open.
//
// The checks are the pack-shaped form of the same four properties a compiled
// contribution is held to — schema validity, host API compatibility,
// determinism, and honest provenance — and they are deliberately run against the
// REAL install-and-merge path rather than against a model of it. A determinism
// check that compared two in-memory decodes would be proving that YAML parsing
// is a function; what has to be proven is that the merge a user's repository
// performs is one.
func VerifyPack(path string) Report {
	rec := &recorder{subject: "pack " + path}

	diagnostics := extpack.Lint(path)
	manifestPath, resolveErr := extpack.ResolveManifestPath(path)
	if resolveErr != nil {
		rec.fail(CheckManifest, "%v", resolveErr)
		return rec.report()
	}
	rec.subject = "pack " + manifestPath

	manifestDiags, artifactDiags := splitDiagnostics(manifestPath, diagnostics)
	rec.record(CheckManifest, "manifest validates against "+extpack.SchemaVersion, diagnosticError(manifestDiags))
	rec.record(CheckArtifactSchema, "artifact validates against its kind's schema", diagnosticError(artifactDiags))
	if len(diagnostics) > 0 {
		// Everything below installs the pack. Installing one that does not
		// validate would report the refusal three more times, in checks whose
		// names would then be misleading.
		rec.fail(CheckAPIVersion, "not run: the pack did not validate")
		rec.fail(CheckMergeDeterminism, "not run: the pack did not validate")
		rec.fail(CheckProvenance, "not run: the pack did not validate")
		return rec.report()
	}

	candidate, err := extpack.ValidateFile(manifestPath, "")
	if err != nil {
		rec.fail(CheckAPIVersion, "%v", err)
		rec.fail(CheckMergeDeterminism, "not run: the pack did not validate")
		rec.fail(CheckProvenance, "not run: the pack did not validate")
		return rec.report()
	}
	m := candidate.Manifest
	rec.subject = fmt.Sprintf("pack %s@%s (%s)", m.ID, m.Version, m.Kind)
	rec.record(CheckAPIVersion,
		fmt.Sprintf("declares api %s..%s; this host speaks %s", m.API.Min, m.API.Max, HostAPIVersion),
		m.API.Validate())

	firstSet, firstBytes, err := mergeOnce(manifestPath, candidate.ManifestSHA256)
	if err != nil {
		rec.fail(CheckMergeDeterminism, "%v", err)
		rec.fail(CheckProvenance, "not run: the pack could not be merged")
		return rec.report()
	}
	_, secondBytes, err := mergeOnce(manifestPath, candidate.ManifestSHA256)
	if err != nil {
		rec.fail(CheckMergeDeterminism, "%v", err)
		rec.fail(CheckProvenance, "not run: the pack could not be merged")
		return rec.report()
	}
	if !bytes.Equal(firstBytes, secondBytes) {
		rec.fail(CheckMergeDeterminism,
			"%s: two independent installs of %q merged to different bytes (%d vs %d)",
			registryName, m.ID, len(firstBytes), len(secondBytes))
	} else {
		rec.pass(CheckMergeDeterminism,
			fmt.Sprintf("two independent installs merged to identical bytes (%d)", len(firstBytes)))
	}
	rec.record(CheckProvenance,
		fmt.Sprintf("every merged item carries %s@%s sha256:%s", m.ID, m.Version, candidate.ManifestSHA256),
		checkProvenance(firstSet, m.ID))
	return rec.report()
}

// splitDiagnostics separates manifest findings from artifact findings so the two
// schemas get their own check row. A single "it did not validate" row would make
// the linter's whole point — that a pack is two documents with two vocabularies —
// invisible in the report.
func splitDiagnostics(manifestPath string, diagnostics []extpack.Diagnostic) (manifest, artifact []extpack.Diagnostic) {
	for _, d := range diagnostics {
		if d.File == manifestPath {
			manifest = append(manifest, d)
			continue
		}
		artifact = append(artifact, d)
	}
	return manifest, artifact
}

func diagnosticError(diagnostics []extpack.Diagnostic) error {
	if len(diagnostics) == 0 {
		return nil
	}
	lines := make([]string, 0, len(diagnostics))
	for _, d := range diagnostics {
		lines = append(lines, d.String())
	}
	return fmt.Errorf("%d diagnostic(s):\n    %s", len(diagnostics), strings.Join(lines, "\n    "))
}

// mergeOnce installs the pack into a throwaway repository root and returns the
// merged set plus its canonical bytes.
//
// A fresh root per run is the point: two merges that shared a store would share
// whatever the first one cached, and the check would pass for a reason that has
// nothing to do with determinism.
func mergeOnce(manifestPath, manifestSHA256 string) (*extpack.Set, []byte, error) {
	root, err := os.MkdirTemp("", "graphi-conformance-")
	if err != nil {
		return nil, nil, fmt.Errorf("%s: create a temporary repository root: %w", registryName, err)
	}
	defer os.RemoveAll(root)

	if _, err := extpack.Install(root, manifestPath, manifestSHA256); err != nil {
		return nil, nil, fmt.Errorf("%s: install into a temporary root: %w", registryName, err)
	}
	set, err := extpack.Load(root)
	if err != nil {
		return nil, nil, fmt.Errorf("%s: load the installed pack: %w", registryName, err)
	}
	canonical, err := set.Canonical()
	if err != nil {
		return nil, nil, err
	}
	return set, canonical, nil
}

// checkProvenance proves every merged item is attributable.
//
// ADR 0013 D5.2 requires an extension-produced result to be distinguishable from
// a first-party one AT THE POINT OF CONSUMPTION. The loader stamps the Ref; this
// check is what stops a future merge path from forgetting to, which would be
// invisible until somebody read a finding and could not tell where it came from.
func checkProvenance(set *extpack.Set, id string) error {
	items := 0
	for _, r := range set.ArchRules() {
		items++
		if err := refOf("architecture rule "+r.ID, r.Pack, id); err != nil {
			return err
		}
	}
	for _, d := range set.TaintSources() {
		items++
		if err := refOf("taint source "+d.ID, d.Pack, id); err != nil {
			return err
		}
	}
	for _, d := range set.TaintSinks() {
		items++
		if err := refOf("taint sink "+d.ID, d.Pack, id); err != nil {
			return err
		}
	}
	for _, d := range set.TaintSanitizers() {
		items++
		if err := refOf("taint sanitizer "+d.ID, d.Pack, id); err != nil {
			return err
		}
	}
	if items == 0 {
		return fmt.Errorf("%s: the pack merged to nothing; a pack that contributes no item cannot "+
			"have its provenance checked", registryName)
	}
	return nil
}

func refOf(what string, ref extpack.Ref, id string) error {
	switch {
	case ref.ID != id:
		return fmt.Errorf("%s: %s carries pack id %q, want %q", registryName, what, extpack.Bound(ref.ID), id)
	case ref.Version == "":
		return fmt.Errorf("%s: %s carries no pack version", registryName, what)
	case ref.SHA256 == "":
		return fmt.Errorf("%s: %s carries no pack hash", registryName, what)
	}
	return nil
}
