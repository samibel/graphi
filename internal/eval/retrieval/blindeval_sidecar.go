package retrieval

// Sidecar binding for the qrel-blind smoke evaluation.
//
// The responses, grades and adjudications are named by the pre-registration and
// sealed write-once, so removing one is caught: the pre-registration says how
// many queries there are and who answered them, and an absent attempt has to be
// recorded as `missing` rather than omitted.
//
// The SIDECARS had no such anchor. `capture-provenance.json` and
// `disclosed-grading-concerns.json` are both read with "if the file is there,
// use it", and both feed the decision:
//
//   - the provenance decides whether the capture is bound, and an unbound
//     capture is a release refusal;
//   - the concern record subtracts from the corrected count, and the release is
//     taken on the smaller of the reviewed and corrected counts.
//
// So DELETING a concern record raised the corrected count — at the threshold,
// 56 reviewed / 55 corrected (`NO`) becomes 56/56 (`YES`) — and the decision
// procedure accepted it and said nothing, because it had no record that the
// file had ever existed. That is the whole class: an artifact the decision
// depends on that is optional, mutable, and unbound.
//
// The manifest is that record. It content-addresses every sidecar, it is
// itself MANDATORY — a run without one cannot be decided at all, so deleting
// the manifest is a refusal rather than a way back to the old behaviour — and
// it admits exactly one transition, the one whose direction can only ever
// worsen the outcome:
//
//	absent  -> present   allowed: a concern raised later only subtracts, and a
//	                     provenance recorded later is still assessed on its own
//	                     content before it can bind anything.
//	present -> absent    refused.
//	present -> different refused.
//
// What this does NOT defend against is the same thing the append-only seal does
// not defend against: an author with write access to the whole run directory
// can delete the manifest and the sidecar together and re-seal both. The
// defence there is the run directory's git history, where such a removal is a
// visible deletion of committed files. That limitation is stated in the run's
// METHOD.md rather than left for a reader to discover.

import (
	"fmt"
	"os"
	"path/filepath"
)

// BlindEvalSidecarManifestFile is the run-directory file that binds the
// sidecars. It is mandatory: LoadEvaluationArtifacts refuses a run without one.
const BlindEvalSidecarManifestFile = "sidecar-manifest.json"

// BlindEvalSidecarFiles is the closed set of decision-bearing sidecar files, in
// the order the manifest records them. It is a function rather than a variable
// so no caller can append to it.
func BlindEvalSidecarFiles() []string {
	return []string{BlindEvalProvenanceFile, BlindEvalConcernsFile}
}

// SidecarSeal is one sidecar's recorded state. Present is recorded explicitly,
// so "this run had no concern record" and "this run's concern record was
// deleted" are different facts rather than the same silence.
type SidecarSeal struct {
	File    string `json:"file"`
	Present bool   `json:"present"`
	SHA256  string `json:"sha256,omitempty"`
}

// SidecarManifest binds the sidecars to one run.
type SidecarManifest struct {
	ContractVersion       string        `json:"contract_version"`
	Evaluation            string        `json:"evaluation"`
	PreRegistrationSHA256 string        `json:"pre_registration_sha256"`
	Sidecars              []SidecarSeal `json:"sidecars"`
}

// ObserveSidecars content-addresses every sidecar exactly as it is on disk.
//
// A read failure that is not "no such file" is an error, not an absence: a
// permission or I/O failure that read as "no concern record" would raise the
// corrected count, which is the direction nothing here is allowed to move on
// its own.
func ObserveSidecars(dir string) ([]SidecarSeal, error) {
	names := BlindEvalSidecarFiles()
	seals := make([]SidecarSeal, 0, len(names))
	for _, name := range names {
		raw, err := os.ReadFile(filepath.Join(dir, name))
		switch {
		case err == nil:
			seals = append(seals, SidecarSeal{File: name, Present: true, SHA256: SHA256Hex(raw)})
		case os.IsNotExist(err):
			seals = append(seals, SidecarSeal{File: name, Present: false})
		default:
			return nil, fmt.Errorf("retrieval %s: read the sidecar %s: %w", QrelBlindSmokeEvaluationName, filepath.Join(dir, name), err)
		}
	}
	return seals, nil
}

// LoadSidecarManifest reads the manifest. The second result is false only when
// no manifest exists; an unreadable or malformed one is an error.
func LoadSidecarManifest(dir string) (SidecarManifest, bool, error) {
	path := filepath.Join(dir, BlindEvalSidecarManifestFile)
	if _, err := os.Stat(path); err != nil {
		if os.IsNotExist(err) {
			return SidecarManifest{}, false, nil
		}
		return SidecarManifest{}, false, fmt.Errorf("retrieval %s: stat %s: %w", QrelBlindSmokeEvaluationName, path, err)
	}
	var manifest SidecarManifest
	if err := readBlindEvalJSON(path, &manifest); err != nil {
		return SidecarManifest{}, false, err
	}
	return manifest, true, nil
}

// SealSidecarManifest records the sidecars as they are now, refusing every
// transition but "a sidecar that was recorded absent has appeared".
//
// It is deterministic and carries no clock, so sealing the same state again
// writes the same bytes.
func SealSidecarManifest(dir string, pre PreRegistration) (SidecarManifest, error) {
	observed, err := ObserveSidecars(dir)
	if err != nil {
		return SidecarManifest{}, err
	}
	manifest := SidecarManifest{
		ContractVersion:       QrelBlindSmokeContractVersion,
		Evaluation:            QrelBlindSmokeEvaluationName,
		PreRegistrationSHA256: pre.SHA256,
		Sidecars:              observed,
	}
	prior, had, err := LoadSidecarManifest(dir)
	if err != nil {
		return SidecarManifest{}, err
	}
	if had {
		if err := checkSidecarManifestBinding(prior, pre); err != nil {
			return SidecarManifest{}, err
		}
		recorded := map[string]SidecarSeal{}
		for _, seal := range prior.Sidecars {
			recorded[seal.File] = seal
		}
		for _, now := range observed {
			was, known := recorded[now.File]
			if !known || !was.Present {
				// absent -> present, or a sidecar this build knows and the
				// earlier manifest did not. Both only add material, and the
				// material is assessed on its own content before it binds
				// anything.
				continue
			}
			if !now.Present {
				return SidecarManifest{}, fmt.Errorf("retrieval %s: the sidecar %s is recorded in %s as present with content %s and is now absent; a sidecar the decision depends on is never deleted, and an absent %s would raise the corrected count",
					QrelBlindSmokeEvaluationName, now.File, BlindEvalSidecarManifestFile, was.SHA256, now.File)
			}
			if now.SHA256 != was.SHA256 {
				return SidecarManifest{}, fmt.Errorf("retrieval %s: the sidecar %s is recorded in %s with content %s and now holds %s; a recorded sidecar is written once and is never replaced",
					QrelBlindSmokeEvaluationName, now.File, BlindEvalSidecarManifestFile, was.SHA256, now.SHA256)
			}
		}
	}
	if err := WriteBlindEvalJSON(filepath.Join(dir, BlindEvalSidecarManifestFile), manifest); err != nil {
		return SidecarManifest{}, err
	}
	return manifest, nil
}

// CheckSidecarBinding is the decision-time half: the manifest must exist, must
// bind to this pre-registration, and must describe the files that are actually
// on disk.
func CheckSidecarBinding(dir string, pre PreRegistration) error {
	manifest, had, err := LoadSidecarManifest(dir)
	if err != nil {
		return err
	}
	if !had {
		return fmt.Errorf("retrieval %s: this run records no %s; without it the decision cannot tell a run that never had a %s from one whose %s was deleted, and deleting a disclosed concern raises the corrected count — seal the manifest before deciding",
			QrelBlindSmokeEvaluationName, BlindEvalSidecarManifestFile, BlindEvalConcernsFile, BlindEvalConcernsFile)
	}
	if err := checkSidecarManifestBinding(manifest, pre); err != nil {
		return err
	}
	recorded := map[string]SidecarSeal{}
	for _, seal := range manifest.Sidecars {
		if _, duplicate := recorded[seal.File]; duplicate {
			return fmt.Errorf("retrieval %s: %s records the sidecar %s twice", QrelBlindSmokeEvaluationName, BlindEvalSidecarManifestFile, seal.File)
		}
		recorded[seal.File] = seal
	}
	names := BlindEvalSidecarFiles()
	if len(recorded) != len(names) {
		return fmt.Errorf("retrieval %s: %s records %d sidecars; this evaluation's decision depends on exactly %d (%v)",
			QrelBlindSmokeEvaluationName, BlindEvalSidecarManifestFile, len(recorded), len(names), names)
	}
	observed, err := ObserveSidecars(dir)
	if err != nil {
		return err
	}
	for _, now := range observed {
		was, known := recorded[now.File]
		if !known {
			return fmt.Errorf("retrieval %s: %s does not record the sidecar %s, which the decision reads", QrelBlindSmokeEvaluationName, BlindEvalSidecarManifestFile, now.File)
		}
		switch {
		case was.Present && !now.Present:
			return fmt.Errorf("retrieval %s: %s records the sidecar %s with content %s, and no such file is present; a recorded artifact that is now absent is a refusal, never a better number",
				QrelBlindSmokeEvaluationName, BlindEvalSidecarManifestFile, now.File, was.SHA256)
		case was.Present && now.SHA256 != was.SHA256:
			return fmt.Errorf("retrieval %s: %s records the sidecar %s with content %s, and the file on disk holds %s; a recorded sidecar is never replaced",
				QrelBlindSmokeEvaluationName, BlindEvalSidecarManifestFile, now.File, was.SHA256, now.SHA256)
		case !was.Present && now.Present:
			return fmt.Errorf("retrieval %s: %s records that this run had no %s, and one is now present holding %s; seal the manifest again so the record names it before it is read",
				QrelBlindSmokeEvaluationName, BlindEvalSidecarManifestFile, now.File, now.SHA256)
		}
		if was.Present && was.SHA256 == "" {
			return fmt.Errorf("retrieval %s: %s records the sidecar %s as present with no content address", QrelBlindSmokeEvaluationName, BlindEvalSidecarManifestFile, now.File)
		}
	}
	return nil
}

// checkSidecarManifestBinding refuses a manifest that belongs to another run or
// another contract. The pre-registration's content address is the run's
// identity: every response names it, so a manifest copied from a different run
// cannot claim this one.
func checkSidecarManifestBinding(m SidecarManifest, pre PreRegistration) error {
	if m.ContractVersion != QrelBlindSmokeContractVersion {
		return fmt.Errorf("retrieval %s: %s has contract_version=%q, want %q", QrelBlindSmokeEvaluationName, BlindEvalSidecarManifestFile, m.ContractVersion, QrelBlindSmokeContractVersion)
	}
	if m.Evaluation != QrelBlindSmokeEvaluationName {
		return fmt.Errorf("retrieval %s: %s names evaluation %q", QrelBlindSmokeEvaluationName, BlindEvalSidecarManifestFile, m.Evaluation)
	}
	if m.PreRegistrationSHA256 != pre.SHA256 {
		return fmt.Errorf("retrieval %s: %s binds pre-registration %s, but this run's pre-registration addresses to %s; a manifest from another run does not bind this one",
			QrelBlindSmokeEvaluationName, BlindEvalSidecarManifestFile, m.PreRegistrationSHA256, pre.SHA256)
	}
	return nil
}
