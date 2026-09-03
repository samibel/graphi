// Command static-embedder-archcheck records and compares the exact float32
// output bits produced by the pinned static embedder. It is deliberately
// offline: cmd/graphi/staticfetch remains the only model-download boundary.
package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"math"
	"os"
	"reflect"
	"sort"
	"strings"

	"github.com/samibel/graphi/engine/embed/static"
)

const recordFormatVersion = 1

type inputSet struct {
	FormatVersion int     `json:"format_version"`
	Inputs        []input `json:"inputs"`
}

type input struct {
	ID   string `json:"id"`
	Text string `json:"text"`
}

type vectorRecord struct {
	FormatVersion  int               `json:"format_version"`
	Selector       string            `json:"selector"`
	ArtifactSHA256 map[string]string `json:"artifact_sha256"`
	InputsSHA256   string            `json:"inputs_sha256"`
	Vectors        []vector          `json:"vectors"`
}

type vector struct {
	ID          string   `json:"id"`
	Text        string   `json:"text"`
	Float32Bits []string `json:"float32_bits"`
}

func main() {
	if err := run(context.Background(), os.Args[1:], os.Stdout); err != nil {
		fmt.Fprintf(os.Stderr, "static-embedder-archcheck: %v\n", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string, stdout io.Writer) error {
	if len(args) == 0 {
		return usageError()
	}
	switch args[0] {
	case "selector":
		if len(args) != 1 {
			return fmt.Errorf("selector: unexpected arguments %q", args[1:])
		}
		_, err := fmt.Fprintln(stdout, static.PinnedSelector)
		return err
	case "verify":
		fs := flag.NewFlagSet("verify", flag.ContinueOnError)
		fs.SetOutput(io.Discard)
		modelDir := fs.String("model-dir", "", "directory containing the pinned model artifact")
		if err := fs.Parse(args[1:]); err != nil {
			return fmt.Errorf("verify: %w", err)
		}
		if fs.NArg() != 0 || strings.TrimSpace(*modelDir) == "" {
			return fmt.Errorf("verify: -model-dir is required and positional arguments are not accepted")
		}
		if err := static.VerifyPins(*modelDir); err != nil {
			return fmt.Errorf("verify pinned artifact: %w", err)
		}
		_, err := fmt.Fprintf(stdout, "static embedder artifact: verified %s (%d pinned files)\n", static.PinnedSelector, len(static.PinnedFileNames))
		return err
	case "record":
		fs := flag.NewFlagSet("record", flag.ContinueOnError)
		fs.SetOutput(io.Discard)
		modelDir := fs.String("model-dir", "", "directory containing the pinned model artifact")
		inputs := fs.String("inputs", "", "checked-in JSON input set")
		out := fs.String("out", "", "destination for the canonical vector record")
		if err := fs.Parse(args[1:]); err != nil {
			return fmt.Errorf("record: %w", err)
		}
		if fs.NArg() != 0 || strings.TrimSpace(*modelDir) == "" || strings.TrimSpace(*inputs) == "" || strings.TrimSpace(*out) == "" {
			return fmt.Errorf("record: -model-dir, -inputs, and -out are required and positional arguments are not accepted")
		}
		record, err := recordVectors(ctx, *modelDir, *inputs)
		if err != nil {
			return err
		}
		body, err := marshalRecord(record)
		if err != nil {
			return err
		}
		if err := os.WriteFile(*out, body, 0o644); err != nil {
			return fmt.Errorf("write vector record %s: %w", *out, err)
		}
		_, err = fmt.Fprintf(stdout, "static embedder vectors: wrote %d inputs x %d dimensions to %s (sha256 %s)\n", len(record.Vectors), vectorDimensions(record), *out, sha256Hex(body))
		return err
	case "compare":
		fs := flag.NewFlagSet("compare", flag.ContinueOnError)
		fs.SetOutput(io.Discard)
		left := fs.String("left", "", "first canonical vector record")
		right := fs.String("right", "", "second canonical vector record")
		if err := fs.Parse(args[1:]); err != nil {
			return fmt.Errorf("compare: %w", err)
		}
		if fs.NArg() != 0 || strings.TrimSpace(*left) == "" || strings.TrimSpace(*right) == "" {
			return fmt.Errorf("compare: -left and -right are required and positional arguments are not accepted")
		}
		return compareFiles(*left, *right, stdout)
	default:
		return usageError()
	}
}

func usageError() error {
	return errors.New("usage: static-embedder-archcheck <selector|verify|record|compare> [flags]")
}

func recordVectors(ctx context.Context, modelDir, inputPath string) (vectorRecord, error) {
	inputBytes, err := os.ReadFile(inputPath)
	if err != nil {
		return vectorRecord{}, fmt.Errorf("read inputs %s: %w", inputPath, err)
	}
	inputs, err := decodeInputs(inputBytes)
	if err != nil {
		return vectorRecord{}, fmt.Errorf("read inputs %s: %w", inputPath, err)
	}
	if err := static.VerifyPins(modelDir); err != nil {
		return vectorRecord{}, fmt.Errorf("pinned artifact is required; no skip or download is permitted: %w", err)
	}
	model, err := static.LoadModel(modelDir)
	if err != nil {
		return vectorRecord{}, fmt.Errorf("load pinned artifact: %w", err)
	}
	texts := make([]string, len(inputs.Inputs))
	for i := range inputs.Inputs {
		texts[i] = inputs.Inputs[i].Text
	}
	produced, err := model.Embed(ctx, texts)
	if err != nil {
		return vectorRecord{}, fmt.Errorf("embed cross-architecture inputs: %w", err)
	}
	if len(produced) != len(inputs.Inputs) {
		return vectorRecord{}, fmt.Errorf("embed returned %d vectors for %d inputs", len(produced), len(inputs.Inputs))
	}

	record := vectorRecord{
		FormatVersion:  recordFormatVersion,
		Selector:       static.PinnedSelector,
		ArtifactSHA256: clonePins(static.PinnedSHA256),
		InputsSHA256:   sha256Hex(inputBytes),
		Vectors:        make([]vector, len(produced)),
	}
	for i, values := range produced {
		if len(values) == 0 {
			return vectorRecord{}, fmt.Errorf("input %q produced an empty vector", inputs.Inputs[i].ID)
		}
		bits := make([]string, len(values))
		for component, value := range values {
			bits[component] = fmt.Sprintf("%08x", math.Float32bits(value))
		}
		record.Vectors[i] = vector{ID: inputs.Inputs[i].ID, Text: inputs.Inputs[i].Text, Float32Bits: bits}
	}
	return record, nil
}

func decodeInputs(body []byte) (inputSet, error) {
	var inputs inputSet
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&inputs); err != nil {
		return inputSet{}, err
	}
	if err := requireEOF(dec); err != nil {
		return inputSet{}, err
	}
	if inputs.FormatVersion != recordFormatVersion {
		return inputSet{}, fmt.Errorf("format_version = %d, want %d", inputs.FormatVersion, recordFormatVersion)
	}
	if len(inputs.Inputs) == 0 {
		return inputSet{}, errors.New("inputs must not be empty")
	}
	seen := make(map[string]struct{}, len(inputs.Inputs))
	for i, in := range inputs.Inputs {
		if strings.TrimSpace(in.ID) == "" || strings.TrimSpace(in.Text) == "" {
			return inputSet{}, fmt.Errorf("inputs[%d] requires non-empty id and text", i)
		}
		if _, ok := seen[in.ID]; ok {
			return inputSet{}, fmt.Errorf("duplicate input id %q", in.ID)
		}
		seen[in.ID] = struct{}{}
	}
	return inputs, nil
}

func compareFiles(leftPath, rightPath string, stdout io.Writer) error {
	leftBytes, err := os.ReadFile(leftPath)
	if err != nil {
		return fmt.Errorf("read left record %s: %w", leftPath, err)
	}
	rightBytes, err := os.ReadFile(rightPath)
	if err != nil {
		return fmt.Errorf("read right record %s: %w", rightPath, err)
	}
	left, err := decodeRecord(leftBytes)
	if err != nil {
		return fmt.Errorf("decode left record %s: %w", leftPath, err)
	}
	right, err := decodeRecord(rightBytes)
	if err != nil {
		return fmt.Errorf("decode right record %s: %w", rightPath, err)
	}
	if err := compareRecords(left, right); err != nil {
		return err
	}
	if !bytes.Equal(leftBytes, rightBytes) {
		return errors.New("records have identical vector bits but non-identical canonical bytes")
	}
	_, err = fmt.Fprintf(stdout, "static embedder cross-architecture vectors: byte-exact (%d inputs x %d dimensions, sha256 %s)\n", len(left.Vectors), vectorDimensions(left), sha256Hex(leftBytes))
	return err
}

func compareRecords(left, right vectorRecord) error {
	if left.FormatVersion != right.FormatVersion {
		return fmt.Errorf("record format differs: left=%d right=%d", left.FormatVersion, right.FormatVersion)
	}
	if left.Selector != right.Selector {
		return fmt.Errorf("selector differs: left=%q right=%q", left.Selector, right.Selector)
	}
	if !reflect.DeepEqual(left.ArtifactSHA256, right.ArtifactSHA256) {
		return fmt.Errorf("artifact SHA-256 pins differ: left=%v right=%v", sortedPins(left.ArtifactSHA256), sortedPins(right.ArtifactSHA256))
	}
	if left.InputsSHA256 != right.InputsSHA256 {
		return fmt.Errorf("input artifact differs: left sha256=%s right sha256=%s", left.InputsSHA256, right.InputsSHA256)
	}
	if len(left.Vectors) != len(right.Vectors) {
		return fmt.Errorf("vector count differs: left=%d right=%d", len(left.Vectors), len(right.Vectors))
	}
	for i := range left.Vectors {
		lv, rv := left.Vectors[i], right.Vectors[i]
		if lv.ID != rv.ID || lv.Text != rv.Text {
			return fmt.Errorf("input[%d] differs: left id=%q text=%q; right id=%q text=%q", i, lv.ID, lv.Text, rv.ID, rv.Text)
		}
		if len(lv.Float32Bits) != len(rv.Float32Bits) {
			return fmt.Errorf("static embedder cross-architecture divergence: input %q (%q) dimension differs: left=%d right=%d", lv.ID, lv.Text, len(lv.Float32Bits), len(rv.Float32Bits))
		}
		for component := range lv.Float32Bits {
			if lv.Float32Bits[component] != rv.Float32Bits[component] {
				return fmt.Errorf("static embedder cross-architecture divergence: input %q (%q), component %d: left=0x%s right=0x%s", lv.ID, lv.Text, component, lv.Float32Bits[component], rv.Float32Bits[component])
			}
		}
	}
	return nil
}

func decodeRecord(body []byte) (vectorRecord, error) {
	var record vectorRecord
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&record); err != nil {
		return vectorRecord{}, err
	}
	if err := requireEOF(dec); err != nil {
		return vectorRecord{}, err
	}
	if record.FormatVersion != recordFormatVersion {
		return vectorRecord{}, fmt.Errorf("format_version = %d, want %d", record.FormatVersion, recordFormatVersion)
	}
	if record.Selector == "" || len(record.ArtifactSHA256) == 0 || record.InputsSHA256 == "" || len(record.Vectors) == 0 {
		return vectorRecord{}, errors.New("record is incomplete")
	}
	return record, nil
}

func requireEOF(dec *json.Decoder) error {
	var extra any
	if err := dec.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("unexpected trailing JSON value")
		}
		return err
	}
	return nil
}

func marshalRecord(record vectorRecord) ([]byte, error) {
	body, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("encode vector record: %w", err)
	}
	return append(body, '\n'), nil
}

func vectorDimensions(record vectorRecord) int {
	if len(record.Vectors) == 0 {
		return 0
	}
	return len(record.Vectors[0].Float32Bits)
}

func clonePins(pins map[string]string) map[string]string {
	clone := make(map[string]string, len(pins))
	for name, sum := range pins {
		clone[name] = sum
	}
	return clone
}

func sortedPins(pins map[string]string) []string {
	names := make([]string, 0, len(pins))
	for name := range pins {
		names = append(names, name)
	}
	sort.Strings(names)
	out := make([]string, len(names))
	for i, name := range names {
		out[i] = name + "=" + pins[name]
	}
	return out
}

func sha256Hex(body []byte) string {
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:])
}
