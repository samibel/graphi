// Package static's pin table. The pin table is the single source of truth
// for the SHA-256 of every file `graphi setup-embedder` downloads and the
// loader validates against. The table here MUST agree with the oracle
// fixture's `files` block (testdata/oracle/oracle.json) and with the SW-259
// spike's PINNED.md; TestStatic_PinTableAgreesWithOracle pins the agreement
// in tests.
//
// The init() that registers scheme `static` lives in static.go (it imports
// engine/embed for RegisterScheme; pins.go is the data file).
package static

// PinnedSHA256 is the canonical pin table. The full hex digest is recorded
// for every pinned file; AC-2's Embedder.ID() uses the config hash's first
// 12 hex digits as the inference-configuration signature.
//
// The four files sum to ~32 MiB (33,514,749 bytes per PINNED.md); the loader
// enforces a 34 MiB ceiling (maxArtifactBytes) so a corrupted download
// cannot allocate (AC-7).
var PinnedSHA256 = map[string]string{
	"config.json":       "148e5691a6fcc553437156859701fba017a1ba5d340b170f17e0f3668fb861a7",
	"tokenizer.json":    "107bbdcbad4bff1d299b7a4c3a2fb17c52890688b7dd0e4c9deab79d3c4f3d45",
	"model.safetensors": "75cf7a6c2171b230ad19b1e7d8e0b1aee86da5a02af8e7cacedd9921d227623c",
	"modules.json":      "a68dcbed0429dcdd5bfdca92b0b03cc30d09122c0a3fcf4758787d4b244e45b2",
}

// PinnedModel is the user-facing model name (without the "minishlab/" prefix
// the HuggingFace id carries). The full HuggingFace id is "minishlab/" +
// PinnedModel; the user-facing selector is "static:" + PinnedModel + "@" +
// PinnedRevision.
const PinnedModel = "potion-code-16M-v2"

// PinnedRevision is the HuggingFace commit SHA-1 `graphi setup-embedder`
// downloads from and the one the production embedder pins. The pin table is
// keyed by revision: a different revision requires a separate pin entry
// (backlog: arbitrary Model2Vec models without a pin-table entry are
// deliberately not supported — the story's Out-of-scope section says so).
const PinnedRevision = "e9d2a44ca6a05ac6685f3b23709ea57eb7352d5b"

// PinnedHuggingFaceURL is the HuggingFace base URL `graphi setup-embedder`
// fetches from. HTTPS only (AC-4: `graphi setup-embedder static:...` shall
// download over HTTPS only).
const PinnedHuggingFaceURL = "https://huggingface.co/minishlab/" + PinnedModel + "/resolve/" + PinnedRevision + "/"

// PinnedFileNames is the sorted list of file names the loader requires. The
// list drives both the setup-embedder download loop and the loader's
// pre-allocation existence check.
var PinnedFileNames = []string{"config.json", "tokenizer.json", "model.safetensors", "modules.json"}

// PinnedSelector is the canonical user-facing selector for the production
// embedder. It is the single source of the "static:<model>@<revision>"
// string the help text, the print message, and the runtime wiring all
// reference — a divergence between the three used to be a copy/paste
// bug; now they all read this constant.
const PinnedSelector = "static:" + PinnedModel + "@" + PinnedRevision

// PinnedSelectorWithSetupPrefix is `graphi setup-embedder <PinnedSelector>`,
// the copy-pasteable command the help text and the print message both
// reference.
const PinnedSelectorWithSetupPrefix = "graphi setup-embedder " + PinnedSelector
