package model2vec

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"os"
)

// safetensorsTensor is one entry of a safetensors header: dtype, shape and the
// byte range of its data relative to the end of the header.
type safetensorsTensor struct {
	Dtype       string   `json:"dtype"`
	Shape       []int    `json:"shape"`
	DataOffsets [2]int64 `json:"data_offsets"`
}

// loadF16Matrix reads the named 2-D F16 tensor from a safetensors file. The
// format is: an 8-byte little-endian header length N, N bytes of JSON mapping
// tensor names to {dtype, shape, data_offsets}, then the concatenated tensor
// data. The tensor is returned as its raw little-endian binary16 bit patterns
// in row-major order (rows × cols); decoding happens at lookup time so the
// table stays the size of the artifact.
func loadF16Matrix(path, name string) (rows, cols int, data []uint16, err error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return 0, 0, nil, err
	}
	if len(raw) < 8 {
		return 0, 0, nil, fmt.Errorf("safetensors %s: file shorter than its header length field", path)
	}
	n := binary.LittleEndian.Uint64(raw[:8])
	if n > uint64(len(raw)-8) {
		return 0, 0, nil, fmt.Errorf("safetensors %s: header length %d exceeds file size %d", path, n, len(raw))
	}
	header := map[string]json.RawMessage{}
	if err := json.Unmarshal(raw[8:8+n], &header); err != nil {
		return 0, 0, nil, fmt.Errorf("safetensors %s: header: %w", path, err)
	}
	entry, ok := header[name]
	if !ok {
		return 0, 0, nil, fmt.Errorf("safetensors %s: no tensor %q in header", path, name)
	}
	var t safetensorsTensor
	if err := json.Unmarshal(entry, &t); err != nil {
		return 0, 0, nil, fmt.Errorf("safetensors %s: tensor %q: %w", path, name, err)
	}
	if t.Dtype != "F16" {
		return 0, 0, nil, fmt.Errorf("safetensors %s: tensor %q has dtype %s; this spike reads F16 only", path, name, t.Dtype)
	}
	if len(t.Shape) != 2 || t.Shape[0] <= 0 || t.Shape[1] <= 0 {
		return 0, 0, nil, fmt.Errorf("safetensors %s: tensor %q has shape %v; want a 2-D matrix", path, name, t.Shape)
	}
	rows, cols = t.Shape[0], t.Shape[1]
	body := raw[8+n:]
	start, end := t.DataOffsets[0], t.DataOffsets[1]
	if start < 0 || end < start || end > int64(len(body)) {
		return 0, 0, nil, fmt.Errorf("safetensors %s: tensor %q data offsets [%d,%d) outside %d data bytes", path, name, start, end, len(body))
	}
	want := int64(rows) * int64(cols) * 2
	if end-start != want {
		return 0, 0, nil, fmt.Errorf("safetensors %s: tensor %q carries %d bytes, shape %v needs %d", path, name, end-start, t.Shape, want)
	}
	src := body[start:end]
	data = make([]uint16, rows*cols)
	for i := range data {
		data[i] = binary.LittleEndian.Uint16(src[2*i:])
	}
	return rows, cols, data, nil
}
