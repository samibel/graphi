package retrieval

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
)

// Run directory layout. The published report sits beside the raw samples it
// was derived from and beside the dataset bytes it was scored against, so
// `-aggregate <dir>` needs nothing outside the directory.
const (
	RunIndexFile = "run.json"
	ReportFile   = "report.json"
	// CobraV1ReportFile is the dataset-named alias the AC-9 gate reads.
	// Every other historical run directory also carries a `cobra-v1-report.json`
	// alongside `report.json` (they are byte-identical); the gate binds to
	// the dataset-named path because it is invariant across harnesses —
	// `report.json` is a convention the runner chose, the dataset-named
	// alias is the contract. The runner writes both; the index lists
	// `report.json` (the canonical name `-aggregate` reads) and the
	// dataset-named file is the gate's reading copy. SW-263 review /
	// item 6: bind the gate to the dataset-named path, fail closed if
	// it is missing (gate is a property of the tree, not of mtime).
	CobraV1ReportFile = "cobra-v1-report.json"
	DatasetFile       = "dataset.json"
	RawDir            = "raw"
)

// RawFileRef is one raw file as the index lists it: its digest is over the
// bytes on disk, so a hand-edited sample file is caught before any arithmetic.
type RawFileRef struct {
	Series    string   `json:"series"`
	Baseline  Baseline `json:"baseline"`
	File      string   `json:"file"`
	Digest    string   `json:"sha256"`
	Collected bool     `json:"collected"`
	Samples   int      `json:"samples"`
}

// rawIdentity is the self-description a raw payload carries. Every field of it is
// also in the RawFileRef that points at the payload, and checkRefPayload requires
// the two to agree.
type rawIdentity struct {
	formatVersion  int
	harnessVersion string
	series         string
	baseline       Baseline
	collected      bool
	samples        int
}

// checkRefPayload closes the gap between "the file the index points at" and "the
// file that says what it is" (SW-258 review round 3). The digest proves the bytes
// were not edited after the run; it does NOT prove the index and the payload agree
// about which baseline and series those bytes are. Without this check a payload that
// calls itself hybrid_v1 can be stored and read under the lexical ref — the index
// key wins and the payload's own claim is never consulted — so a run directory
// re-stamped after swapping two files would reproduce cleanly. Fail closed on every
// field both sides carry.
func checkRefPayload(ref RawFileRef, got rawIdentity) error {
	mismatch := func(field string, want, have any) error {
		return fmt.Errorf("retrieval: %s: the run index calls it %s %v but the file says %v; the index and its payload disagree", ref.File, field, want, have)
	}
	if got.formatVersion != FormatVersion {
		return fmt.Errorf("retrieval: %s has format_version %d, not the supported %d", ref.File, got.formatVersion, FormatVersion)
	}
	if got.harnessVersion != HarnessVersion {
		return fmt.Errorf("retrieval: %s was produced by harness %q, not %q", ref.File, got.harnessVersion, HarnessVersion)
	}
	if got.series != ref.Series {
		return mismatch("series", ref.Series, got.series)
	}
	if got.baseline != ref.Baseline {
		return mismatch("baseline", ref.Baseline, got.baseline)
	}
	if got.collected != ref.Collected {
		return mismatch("collected", ref.Collected, got.collected)
	}
	if got.samples != ref.Samples {
		return mismatch("samples", ref.Samples, got.samples)
	}
	return nil
}

// RunIndex is the run directory's table of contents.
type RunIndex struct {
	FormatVersion  int    `json:"format_version"`
	HarnessVersion string `json:"harness_version"`
	ScorerVersion  string `json:"scorer_version"`

	Date        string `json:"date"`
	RunnerClass string `json:"runner_class"`
	Repo        string `json:"repo"`

	Report        string       `json:"report"`
	ReportSHA256  string       `json:"report_sha256"`
	Dataset       string       `json:"dataset"`
	DatasetSHA256 string       `json:"dataset_sha256"`
	Raw           []RawFileRef `json:"raw"`

	Environment Environment `json:"environment"`
	Notes       string      `json:"notes"`
}

// RunIndexNotes explains the directory to a reader who has only the files.
const RunIndexNotes = "SW-258 retrieval-eval run directory: report.json is the published artifact, dataset.json the exact judged " +
	"bytes it was scored against, raw/hits-<baseline>.json every ranking (the scorer's input, nothing derived) and " +
	"raw/latency-<baseline>.json every timed execution plus the single-sample measures (index_ms, peak_rss_mb, vector_sidecar_bytes) " +
	"with their status and reason; an unavailable baseline's records say collected: false and carry the typed reason. " +
	"`go run ./cmd/retrieval-eval -aggregate <dir>` recomputes every published statistic from these and exits non-zero on a discrepancy."

// RawFileName names a series file for a baseline.
func RawFileName(series string, b Baseline) string {
	return RawDir + "/" + series + "-" + string(b) + ".json"
}

// WriteRunDir writes the complete run directory: index, report, dataset copy
// and one raw file per (series, baseline). The report is written exactly as
// MarshalReport renders it, so the digest recorded in the index is over the
// same bytes a reader will see.
func WriteRunDir(dir string, res *Result, dataset *Loaded, date string) (*RunIndex, error) {
	if res == nil || res.Report == nil || res.Raw == nil {
		return nil, fmt.Errorf("retrieval: nothing to export")
	}
	if dataset == nil || dataset.SHA256 != res.Report.Reproducible.Dataset.SHA256 {
		return nil, fmt.Errorf("retrieval: the dataset handed to the export is not the one the report was scored against")
	}
	if err := os.MkdirAll(filepath.Join(dir, RawDir), 0o755); err != nil {
		return nil, fmt.Errorf("retrieval: create run dir: %w", err)
	}
	index := &RunIndex{
		FormatVersion:  FormatVersion,
		HarnessVersion: HarnessVersion,
		ScorerVersion:  ScorerVersion,
		Date:           date,
		RunnerClass:    res.Report.Reproducible.RunnerClass,
		Repo:           res.Report.Reproducible.Repo.Name,
		Report:         ReportFile,
		Dataset:        DatasetFile,
		DatasetSHA256:  dataset.SHA256,
		Environment:    res.Report.Environment,
		Notes:          RunIndexNotes,
	}

	reportBytes, err := MarshalReport(res.Report)
	if err != nil {
		return nil, err
	}
	if err := os.WriteFile(filepath.Join(dir, ReportFile), reportBytes, 0o644); err != nil {
		return nil, fmt.Errorf("retrieval: write report: %w", err)
	}
	// Dataset-named alias the AC-9 gate reads (CobraV1ReportFile). Same
	// bytes, second copy: the index's ReportSHA256 is the canonical
	// report's digest, and the gate's reading copy is byte-identical.
	// The runner writes both so a fresh checkout has the file the gate
	// expects without the orchestrator having to manually copy it.
	if err := os.WriteFile(filepath.Join(dir, CobraV1ReportFile), reportBytes, 0o644); err != nil {
		return nil, fmt.Errorf("retrieval: write %s: %w", CobraV1ReportFile, err)
	}
	index.ReportSHA256 = SHA256Hex(reportBytes)
	if err := os.WriteFile(filepath.Join(dir, DatasetFile), dataset.Raw, 0o644); err != nil {
		return nil, fmt.Errorf("retrieval: write dataset copy: %w", err)
	}

	for _, b := range res.Report.Reproducible.Baselines {
		hits, ok := res.Raw.Hits[b.Name]
		if !ok {
			return nil, fmt.Errorf("retrieval: baseline %s is published without a hit set", b.Name)
		}
		ref, err := writeRawFile(dir, RawSeriesHits, b.Name, hits, hits.Collected, hits.Samples)
		if err != nil {
			return nil, err
		}
		index.Raw = append(index.Raw, ref)
		lat, ok := res.Raw.Latency[b.Name]
		if !ok {
			return nil, fmt.Errorf("retrieval: baseline %s is published without a latency set", b.Name)
		}
		ref, err = writeRawFile(dir, RawSeriesLatency, b.Name, lat, lat.Collected, lat.Samples)
		if err != nil {
			return nil, err
		}
		index.Raw = append(index.Raw, ref)
	}

	raw, err := marshalStable(index)
	if err != nil {
		return nil, err
	}
	if err := os.WriteFile(filepath.Join(dir, RunIndexFile), raw, 0o644); err != nil {
		return nil, fmt.Errorf("retrieval: write run index: %w", err)
	}
	return index, nil
}

func writeRawFile(dir, series string, b Baseline, v any, collected bool, samples int) (RawFileRef, error) {
	name := RawFileName(series, b)
	raw, err := marshalStable(v)
	if err != nil {
		return RawFileRef{}, err
	}
	if err := os.WriteFile(filepath.Join(dir, filepath.FromSlash(name)), raw, 0o644); err != nil {
		return RawFileRef{}, fmt.Errorf("retrieval: write %s: %w", name, err)
	}
	return RawFileRef{Series: series, Baseline: b, File: name, Digest: SHA256Hex(raw), Collected: collected, Samples: samples}, nil
}

// RunDir is a run directory read back: everything an aggregate needs.
type RunDir struct {
	Index   RunIndex
	Report  Report
	Dataset *Loaded
	Hits    map[Baseline]RawHitSet
	Latency map[Baseline]RawLatencySet
	// MissingRaw names the (series, baseline) files the index lists but that
	// are not on disk; an absence is reported, never an error.
	MissingRaw []string
}

// ReadRunDir loads a run directory, refusing versions this build cannot read
// and any file whose bytes no longer match the index's digest. A listed raw
// file that is absent is recorded in MissingRaw; one that is present but
// unreadable or tampered is an error.
func ReadRunDir(dir string) (*RunDir, error) {
	raw, err := os.ReadFile(filepath.Join(dir, RunIndexFile))
	if err != nil {
		return nil, fmt.Errorf("retrieval: read %s: %w", RunIndexFile, err)
	}
	var out RunDir
	if err := json.Unmarshal(raw, &out.Index); err != nil {
		return nil, fmt.Errorf("retrieval: parse %s: %w", RunIndexFile, err)
	}
	idx := out.Index
	if idx.FormatVersion != FormatVersion || idx.HarnessVersion != HarnessVersion || idx.ScorerVersion != ScorerVersion {
		return nil, fmt.Errorf("retrieval: run index versions %d/%s/%s are not this build's %d/%s/%s",
			idx.FormatVersion, idx.HarnessVersion, idx.ScorerVersion, FormatVersion, HarnessVersion, ScorerVersion)
	}

	reportBytes, err := os.ReadFile(filepath.Join(dir, idx.Report))
	if err != nil {
		return nil, fmt.Errorf("retrieval: read report: %w", err)
	}
	if idx.ReportSHA256 != "" && SHA256Hex(reportBytes) != idx.ReportSHA256 {
		return nil, fmt.Errorf("retrieval: %s does not match the digest the run index recorded; the report was edited after the run", idx.Report)
	}
	if err := json.Unmarshal(reportBytes, &out.Report); err != nil {
		return nil, fmt.Errorf("retrieval: parse report: %w", err)
	}
	if err := CheckReportVersion(&out.Report); err != nil {
		return nil, err
	}

	ds, err := LoadDataset(filepath.Join(dir, idx.Dataset))
	if err != nil {
		return nil, err
	}
	if ds.SHA256 != idx.DatasetSHA256 {
		return nil, fmt.Errorf("retrieval: %s does not match the digest the run index recorded", idx.Dataset)
	}
	out.Dataset = ds

	out.Hits = map[Baseline]RawHitSet{}
	out.Latency = map[Baseline]RawLatencySet{}
	for _, ref := range idx.Raw {
		p := filepath.Join(dir, filepath.FromSlash(ref.File))
		b, err := os.ReadFile(p)
		if os.IsNotExist(err) {
			out.MissingRaw = append(out.MissingRaw, ref.File)
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("retrieval: read %s: %w", ref.File, err)
		}
		if SHA256Hex(b) != ref.Digest {
			return nil, fmt.Errorf("retrieval: %s does not match the digest the run index recorded; raw samples were edited after the run", ref.File)
		}
		switch ref.Series {
		case RawSeriesHits:
			var set RawHitSet
			if err := json.Unmarshal(b, &set); err != nil {
				return nil, fmt.Errorf("retrieval: parse %s: %w", ref.File, err)
			}
			if err := checkRefPayload(ref, rawIdentity{
				formatVersion:  set.FormatVersion,
				harnessVersion: set.HarnessVersion,
				series:         set.Series,
				baseline:       set.Baseline,
				collected:      set.Collected,
				samples:        set.Samples,
			}); err != nil {
				return nil, err
			}
			out.Hits[ref.Baseline] = set
		case RawSeriesLatency:
			var set RawLatencySet
			if err := json.Unmarshal(b, &set); err != nil {
				return nil, fmt.Errorf("retrieval: parse %s: %w", ref.File, err)
			}
			if err := checkRefPayload(ref, rawIdentity{
				formatVersion:  set.FormatVersion,
				harnessVersion: set.HarnessVersion,
				series:         set.Series,
				baseline:       set.Baseline,
				collected:      set.Collected,
				samples:        set.Samples,
			}); err != nil {
				return nil, err
			}
			out.Latency[ref.Baseline] = set
		default:
			return nil, fmt.Errorf("retrieval: %s: unknown raw series %q", ref.File, ref.Series)
		}
	}
	sort.Strings(out.MissingRaw)
	return &out, nil
}
