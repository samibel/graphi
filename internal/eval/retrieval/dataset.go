package retrieval

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path"
	"sort"
	"strings"
)

// SchemaVersion is the dataset schema this package reads. A file stating any
// other version is refused rather than half-read.
const SchemaVersion = 1

// Strata are the query classes (AC-1). Every dataset query names exactly one.
const (
	StratumExactIdentifier  = "exact_identifier"
	StratumExactPath        = "exact_path"
	StratumNLBehaviour      = "nl_behaviour"
	StratumArchitectureFlow = "architecture_flow"
	StratumConfigDocs       = "config_docs"
	StratumAmbiguous        = "ambiguous"
	StratumNoHit            = "no_hit"
)

// Strata lists every stratum in report order.
var Strata = []string{
	StratumExactIdentifier,
	StratumExactPath,
	StratumNLBehaviour,
	StratumArchitectureFlow,
	StratumConfigDocs,
	StratumAmbiguous,
	StratumNoHit,
}

// Splits. A holdout query may be measured but no ranking weight may be tuned
// on it (AC-2); the split is data so a tuning script can exclude it.
const (
	SplitDev     = "dev"
	SplitHoldout = "holdout"
)

// Grade bounds (AC-1): 3 = exact answer span, 2 = directly relevant,
// 1 = marginal, 0 = irrelevant / negative example.
const (
	GradeMin = 0
	GradeMax = 3
)

// DefaultRelevantMinGrade is the grade at or above which a judged span counts
// as relevant unless the dataset says otherwise.
const DefaultRelevantMinGrade = 2

// EvidenceClassAgentHumanReviewed labels a dataset annotated by a delegated
// agent and reviewed by the orchestrator (AC-2).
const EvidenceClassAgentHumanReviewed = "agent-annotated, human-reviewed"

// Dataset is one versioned query set against one pinned repository.
type Dataset struct {
	SchemaVersion int    `json:"schema_version"`
	ID            string `json:"id"`
	// Repo names the corpus/manifest.json entry (or the built-in "fixture");
	// RepoSHA is the pinned checkout the judgements cite, empty for the
	// in-tree fixture whose pin is the tree itself.
	Repo          string `json:"repo"`
	RepoSHA       string `json:"repo_sha,omitempty"`
	Language      string `json:"language"`
	EvidenceClass string `json:"evidence_class"`
	// RelevantMinGrade overrides DefaultRelevantMinGrade when non-zero.
	RelevantMinGrade int     `json:"relevant_min_grade,omitempty"`
	Notes            string  `json:"notes,omitempty"`
	Queries          []Query `json:"queries"`
}

// Query is one evaluation query with its graded judgements.
type Query struct {
	ID         string      `json:"id"`
	Stratum    string      `json:"stratum"`
	Language   string      `json:"language"`
	Split      string      `json:"split"`
	Text       string      `json:"query"`
	Judgements []Judgement `json:"judgements"`
}

// Judgement is one graded source span. Anchor is a substring that must occur
// inside the span at the pinned commit; it is what turns "the file still has
// that many lines" into "the judged code is still there" (AC-9).
type Judgement struct {
	Path      string `json:"path"`
	StartLine int    `json:"start_line"`
	EndLine   int    `json:"end_line"`
	Anchor    string `json:"anchor"`
	Grade     int    `json:"grade"`
	Reason    string `json:"reason"`
	Annotator string `json:"annotator"`
	Reviewer  string `json:"reviewer"`
}

// Loaded is a dataset together with where it came from and the digest of the
// exact bytes, which every report and raw export stamps.
type Loaded struct {
	Dataset *Dataset
	Path    string
	Raw     []byte
	SHA256  string
}

// SHA256Hex digests b.
func SHA256Hex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// LoadDataset reads, parses and validates a dataset file. Any failure is an
// error: an unreadable dataset never yields an empty run.
func LoadDataset(p string) (*Loaded, error) {
	raw, err := os.ReadFile(p)
	if err != nil {
		return nil, fmt.Errorf("retrieval: read dataset: %w", err)
	}
	var ds Dataset
	if err := json.Unmarshal(raw, &ds); err != nil {
		return nil, fmt.Errorf("retrieval: parse dataset %s: %w", p, err)
	}
	if err := ds.Validate(); err != nil {
		return nil, fmt.Errorf("retrieval: dataset %s: %w", p, err)
	}
	return &Loaded{Dataset: &ds, Path: p, Raw: raw, SHA256: SHA256Hex(raw)}, nil
}

// MinGrade is the relevance threshold in force for this dataset.
func (d *Dataset) MinGrade() int {
	if d.RelevantMinGrade > 0 {
		return d.RelevantMinGrade
	}
	return DefaultRelevantMinGrade
}

// RelevantSpans returns the judgements at or above minGrade, in file order.
func (q Query) RelevantSpans(minGrade int) []Judgement {
	var out []Judgement
	for _, j := range q.Judgements {
		if j.Grade >= minGrade {
			out = append(out, j)
		}
	}
	return out
}

var validStrata = func() map[string]bool {
	m := map[string]bool{}
	for _, s := range Strata {
		m[s] = true
	}
	return m
}()

// Validate checks the schema and every per-query / per-judgement rule. It
// reports the first violation, naming the query and judgement index.
func (d *Dataset) Validate() error {
	if d.SchemaVersion != SchemaVersion {
		return fmt.Errorf("schema_version %d is not the supported %d", d.SchemaVersion, SchemaVersion)
	}
	if strings.TrimSpace(d.ID) == "" {
		return fmt.Errorf("dataset id is empty")
	}
	if strings.TrimSpace(d.Repo) == "" {
		return fmt.Errorf("dataset repo is empty")
	}
	if strings.TrimSpace(d.Language) == "" {
		return fmt.Errorf("dataset language is empty")
	}
	if strings.TrimSpace(d.EvidenceClass) == "" {
		return fmt.Errorf("dataset evidence_class is empty")
	}
	if d.RelevantMinGrade < 0 || d.RelevantMinGrade > GradeMax {
		return fmt.Errorf("relevant_min_grade %d is outside 0..%d", d.RelevantMinGrade, GradeMax)
	}
	if len(d.Queries) == 0 {
		return fmt.Errorf("dataset has no queries")
	}
	minGrade := d.MinGrade()
	seen := map[string]bool{}
	for i, q := range d.Queries {
		if strings.TrimSpace(q.ID) == "" {
			return fmt.Errorf("query %d has no id", i)
		}
		if seen[q.ID] {
			return fmt.Errorf("duplicate query id %q", q.ID)
		}
		seen[q.ID] = true
		if !validStrata[q.Stratum] {
			return fmt.Errorf("query %q: stratum %q is not one of %s", q.ID, q.Stratum, strings.Join(Strata, ", "))
		}
		if q.Split != SplitDev && q.Split != SplitHoldout {
			return fmt.Errorf("query %q: split %q is not %s or %s", q.ID, q.Split, SplitDev, SplitHoldout)
		}
		if strings.TrimSpace(q.Language) == "" {
			return fmt.Errorf("query %q: language is empty", q.ID)
		}
		if strings.TrimSpace(q.Text) == "" {
			return fmt.Errorf("query %q: query text is empty", q.ID)
		}
		relevant := 0
		for k, j := range q.Judgements {
			if err := j.validate(); err != nil {
				return fmt.Errorf("query %q judgement %d: %w", q.ID, k, err)
			}
			if j.Grade >= minGrade {
				relevant++
			}
		}
		switch {
		case q.Stratum == StratumNoHit && relevant > 0:
			return fmt.Errorf("query %q: a no_hit query must not carry a relevant (grade >= %d) span", q.ID, minGrade)
		case q.Stratum != StratumNoHit && relevant == 0:
			return fmt.Errorf("query %q: no relevant (grade >= %d) span; every non-no_hit query needs one", q.ID, minGrade)
		}
	}
	return nil
}

func (j Judgement) validate() error {
	if err := validateSpanPath(j.Path); err != nil {
		return err
	}
	if j.StartLine < 1 {
		return fmt.Errorf("start_line %d must be >= 1", j.StartLine)
	}
	if j.EndLine < j.StartLine {
		return fmt.Errorf("end_line %d is before start_line %d", j.EndLine, j.StartLine)
	}
	if strings.TrimSpace(j.Anchor) == "" {
		return fmt.Errorf("anchor is empty")
	}
	if j.Grade < GradeMin || j.Grade > GradeMax {
		return fmt.Errorf("grade %d is outside %d..%d", j.Grade, GradeMin, GradeMax)
	}
	if strings.TrimSpace(j.Reason) == "" {
		return fmt.Errorf("reason is empty")
	}
	if strings.TrimSpace(j.Annotator) == "" {
		return fmt.Errorf("annotator is empty")
	}
	if strings.TrimSpace(j.Reviewer) == "" {
		return fmt.Errorf("reviewer is empty")
	}
	return nil
}

// validateSpanPath accepts only a clean, repo-relative POSIX path: what the
// ingest walk emits for a node's SourcePath, so the matching rule compares
// like with like.
func validateSpanPath(p string) error {
	switch {
	case strings.TrimSpace(p) == "":
		return fmt.Errorf("path is empty")
	case strings.HasPrefix(p, "/"), strings.Contains(p, `\`):
		return fmt.Errorf("path %q must be a repo-relative POSIX path", p)
	case p != path.Clean(p), p == ".", strings.HasPrefix(p, "../"), p == "..":
		return fmt.Errorf("path %q must be clean and inside the repository", p)
	}
	return nil
}

// StratumCounts is how many queries each stratum holds per split.
type StratumCounts struct {
	Dev     map[string]int
	Holdout map[string]int
}

// Counts tallies the dataset per stratum and split.
func (d *Dataset) Counts() StratumCounts {
	c := StratumCounts{Dev: map[string]int{}, Holdout: map[string]int{}}
	for _, q := range d.Queries {
		if q.Split == SplitHoldout {
			c.Holdout[q.Stratum]++
		} else {
			c.Dev[q.Stratum]++
		}
	}
	return c
}

// CheckDevelopmentRequirements is AC-2 as a predicate: at least minDev
// development queries, at least minPerStratum of them in every stratum, and
// at least minHoldout holdout queries. Every shortfall is listed.
func (d *Dataset) CheckDevelopmentRequirements(minDev, minPerStratum, minHoldout int) error {
	c := d.Counts()
	dev, holdout := 0, 0
	for _, n := range c.Dev {
		dev += n
	}
	for _, n := range c.Holdout {
		holdout += n
	}
	var problems []string
	if dev < minDev {
		problems = append(problems, fmt.Sprintf("%d dev queries, need %d", dev, minDev))
	}
	if holdout < minHoldout {
		problems = append(problems, fmt.Sprintf("%d holdout queries, need %d", holdout, minHoldout))
	}
	for _, s := range Strata {
		if c.Dev[s] < minPerStratum {
			problems = append(problems, fmt.Sprintf("stratum %s has %d dev queries, need %d", s, c.Dev[s], minPerStratum))
		}
	}
	if len(problems) == 0 {
		return nil
	}
	sort.Strings(problems)
	return fmt.Errorf("dataset %s: %s", d.ID, strings.Join(problems, "; "))
}
