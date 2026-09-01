// Package static's safetensors loader (AC-7). Every validation runs
// BEFORE the embedding table is allocated, with overflow-safe arithmetic
// and a typed error per violation. Each violation has a dedicated
// corrupted-fixture test (see static_test.go).
package static

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"math"
	"os"
)

// TensorErrorKind enumerates the safetensors validation violations the
// loader detects. Each carries the offending tensor name, file path, and
// the specific failure (a missing name, a wrong dtype, an invalid shape,
// out-of-bounds offsets, an out-of-range byte count, or a corruption
// detected at parse). The kind is the discriminator callers use in
// errors.As to render an actionable message.
type TensorErrorKind int

const (
	// TensorErrShortFile: file shorter than the 8-byte header length field.
	TensorErrShortFile TensorErrorKind = iota + 1
	// TensorErrHeaderLength: declared header length exceeds file size.
	TensorErrHeaderLength
	// TensorErrHeaderJSON: header JSON failed to parse.
	TensorErrHeaderJSON
	// TensorErrNameMissing: the requested tensor name is not in the header.
	TensorErrNameMissing
	// TensorErrEntryJSON: a header entry failed to parse.
	TensorErrEntryJSON
	// TensorErrDtype: the tensor's dtype is not F16.
	TensorErrDtype
	// TensorErrShape: the tensor's shape is not a 2-D matrix with positive dims.
	TensorErrShape
	// TensorErrOffsets: data offsets are not a non-empty range inside the body.
	TensorErrOffsets
	// TensorErrByteCount: the declared body byte count does not match rows*cols*2.
	TensorErrByteCount
	// TensorErrShapeOverflow: rows*cols*2 overflows int64 or uintptr (the
	// allocation would panic at `make`).
	TensorErrShapeOverflow
	// TensorErrVocabMismatch: tensor rows differ from tokenizer vocabulary.
	TensorErrVocabMismatch
	// TensorErrTotalSize: the pinned artifact exceeds its total-size ceiling.
	TensorErrTotalSize
)

// TensorError is the typed error the loader returns on every AC-7
// violation. The Kind field is the discriminator callers use in
// errors.As; the message is the operator-facing detail. Every
// validation has a dedicated corrupted-fixture test.
type TensorError struct {
	Kind   TensorErrorKind
	File   string
	Tensor string
	Detail string
}

func (e *TensorError) Error() string {
	switch e.Kind {
	case TensorErrShortFile:
		return fmt.Sprintf("static: safetensors %s: file shorter than its header length field (corrupted or truncated)", e.File)
	case TensorErrHeaderLength:
		return fmt.Sprintf("static: safetensors %s: header length exceeds file size (truncated download?)", e.File)
	case TensorErrHeaderJSON:
		return fmt.Sprintf("static: safetensors %s: header JSON: %s", e.File, e.Detail)
	case TensorErrNameMissing:
		return fmt.Sprintf("static: safetensors %s: no tensor %q in header (the artifact does not contain the expected embedding table; the expected tensor name is %q)", e.File, e.Tensor, e.Tensor)
	case TensorErrEntryJSON:
		return fmt.Sprintf("static: safetensors %s: tensor %q: %s", e.File, e.Tensor, e.Detail)
	case TensorErrDtype:
		return fmt.Sprintf("static: safetensors %s: tensor %q has dtype %s; this embedder reads F16 only (AC-7)", e.File, e.Tensor, e.Detail)
	case TensorErrShape:
		return fmt.Sprintf("static: safetensors %s: tensor %q has shape %s; want a 2-D matrix with positive dims (AC-7)", e.File, e.Tensor, e.Detail)
	case TensorErrOffsets:
		return fmt.Sprintf("static: safetensors %s: tensor %q data offsets %s outside the file body (truncated download?)", e.File, e.Tensor, e.Detail)
	case TensorErrByteCount:
		return fmt.Sprintf("static: safetensors %s: tensor %q byte count does not match rows*cols*2: %s (the body is corrupt)", e.File, e.Tensor, e.Detail)
	case TensorErrShapeOverflow:
		return fmt.Sprintf("static: safetensors %s: tensor %q shape product rows*cols*2 overflows int64 (%s); refusing to allocate (AC-7)", e.File, e.Tensor, e.Detail)
	case TensorErrVocabMismatch:
		return fmt.Sprintf("static: safetensors %s: tensor %q shape does not match tokenizer vocabulary: %s; refusing to allocate (AC-7)", e.File, e.Tensor, e.Detail)
	case TensorErrTotalSize:
		return fmt.Sprintf("static: artifact %s exceeds the pinned total-size limit: %s; refusing to allocate (AC-7)", e.File, e.Detail)
	default:
		return fmt.Sprintf("static: safetensors %s: tensor %q: %s", e.File, e.Tensor, e.Detail)
	}
}

// safetensorsTensor is one entry of a safetensors header: dtype, shape and
// the byte range of its data relative to the end of the header.
type safetensorsTensor struct {
	Dtype       string   `json:"dtype"`
	Shape       []int    `json:"shape"`
	DataOffsets [2]int64 `json:"data_offsets"`
}

// shapeBytes returns rows*cols*2 as int64 if it fits, or an error if it
// would overflow. The embedding table's row-major size is rows*cols uint16
// (2 bytes per element), so the byte count is rows*cols*2. The overflow
// check is the difference between a typed error and a runtime panic at
// `make` (which AC-7's "validate before allocation" requires).
func shapeBytes(rows, cols int) (int64, error) {
	if rows < 0 || cols < 0 {
		return 0, fmt.Errorf("rows=%d cols=%d negative", rows, cols)
	}
	r := int64(rows)
	c := int64(cols)
	if r > 0 && c > math.MaxInt64/r {
		return 0, fmt.Errorf("rows*cols overflows int64: rows=%d cols=%d", rows, cols)
	}
	prod := r * c
	maxInt := int64(^uint(0) >> 1)
	if prod > maxInt {
		return 0, fmt.Errorf("rows*cols exceeds int capacity: rows=%d cols=%d", rows, cols)
	}
	if prod > math.MaxInt64/2 {
		return 0, fmt.Errorf("rows*cols overflows int64/2: rows=%d cols=%d", rows, cols)
	}
	return prod * 2, nil
}

// loadF16Matrix reads the named 2-D F16 tensor from a safetensors file.
//
// Every AC-7 check runs here, BEFORE the embedding table is allocated.
// The order of checks is fixed so the test for each violation exercises
// the same path the production code follows:
//
//  1. file length is at least 8 bytes (the 8-byte little-endian header
//     length field);
//  2. the declared header length fits inside the file;
//  3. the header JSON parses;
//  4. the requested tensor name is in the header;
//  5. the header entry parses;
//  6. the dtype is F16;
//  7. the shape is a 2-D matrix with positive dims;
//  8. rows*cols*2 does not overflow int64 or int;
//  9. tensor rows equal the already-parsed tokenizer vocabulary;
//  10. the data offsets are a non-empty range inside the body;
//  11. the declared byte count equals rows*cols*2;
//  12. the bytes are copied into the table (the only allocation).
func loadF16Matrix(path, name string, expectedRows int) (rows, cols int, data []uint16, err error) {
	raw, rerr := os.ReadFile(path)
	if rerr != nil {
		return 0, 0, nil, rerr
	}
	if len(raw) < 8 {
		return 0, 0, nil, &TensorError{Kind: TensorErrShortFile, File: path, Tensor: name, Detail: fmt.Sprintf("file has %d bytes, need ≥ 8", len(raw))}
	}
	n := binary.LittleEndian.Uint64(raw[:8])
	if n > uint64(len(raw)-8) {
		return 0, 0, nil, &TensorError{Kind: TensorErrHeaderLength, File: path, Tensor: name, Detail: fmt.Sprintf("declared header length %d exceeds file size minus 8 (%d)", n, len(raw)-8)}
	}
	header := map[string]json.RawMessage{}
	if jerr := json.Unmarshal(raw[8:8+n], &header); jerr != nil {
		return 0, 0, nil, &TensorError{Kind: TensorErrHeaderJSON, File: path, Tensor: name, Detail: jerr.Error()}
	}
	entry, ok := header[name]
	if !ok {
		keys := make([]string, 0, len(header))
		for k := range header {
			keys = append(keys, k)
		}
		return 0, 0, nil, &TensorError{Kind: TensorErrNameMissing, File: path, Tensor: name, Detail: fmt.Sprintf("available tensors: %v", keys)}
	}
	var t safetensorsTensor
	if jerr := json.Unmarshal(entry, &t); jerr != nil {
		return 0, 0, nil, &TensorError{Kind: TensorErrEntryJSON, File: path, Tensor: name, Detail: jerr.Error()}
	}
	if t.Dtype != "F16" {
		return 0, 0, nil, &TensorError{Kind: TensorErrDtype, File: path, Tensor: name, Detail: "dtype=" + t.Dtype}
	}
	if len(t.Shape) != 2 || t.Shape[0] <= 0 || t.Shape[1] <= 0 {
		shapeStr := "["
		for i, s := range t.Shape {
			if i > 0 {
				shapeStr += ","
			}
			shapeStr += fmt.Sprintf("%d", s)
		}
		shapeStr += "]"
		return 0, 0, nil, &TensorError{Kind: TensorErrShape, File: path, Tensor: name, Detail: "shape=" + shapeStr}
	}
	rows, cols = t.Shape[0], t.Shape[1]
	want, overerr := shapeBytes(rows, cols)
	if overerr != nil {
		return 0, 0, nil, &TensorError{Kind: TensorErrShapeOverflow, File: path, Tensor: name, Detail: overerr.Error()}
	}
	if rows != expectedRows {
		return 0, 0, nil, &TensorError{
			Kind:   TensorErrVocabMismatch,
			File:   path,
			Tensor: name,
			Detail: fmt.Sprintf("tensor rows=%d tokenizer vocab=%d", rows, expectedRows),
		}
	}
	body := raw[8+n:]
	start, end := t.DataOffsets[0], t.DataOffsets[1]
	if start < 0 || end < start || end > int64(len(body)) {
		return 0, 0, nil, &TensorError{Kind: TensorErrOffsets, File: path, Tensor: name, Detail: fmt.Sprintf("[%d,%d) outside %d body bytes", start, end, len(body))}
	}
	if end-start != want {
		return 0, 0, nil, &TensorError{Kind: TensorErrByteCount, File: path, Tensor: name, Detail: fmt.Sprintf("declared %d bytes, want %d (shape %dx%d)", end-start, want, rows, cols)}
	}
	// All validations passed. Only now do we allocate.
	src := body[start:end]
	data = make([]uint16, rows*cols)
	for i := range data {
		data[i] = binary.LittleEndian.Uint16(src[2*i:])
	}
	return rows, cols, data, nil
}

// headerKeys is the deterministic, sorted list of tensor names in the
// header. It is reserved for future diagnostics; the typed TensorError
// for the missing-name case carries a similar list.
func headerKeys(h map[string]json.RawMessage) []string {
	out := make([]string, 0, len(h))
	for k := range h {
		out = append(out, k)
	}
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j-1] > out[j]; j-- {
			out[j-1], out[j] = out[j], out[j-1]
		}
	}
	return out
}
