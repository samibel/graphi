package coderank

import (
	"encoding/json"
	"errors"
	"io"
)

const ProtocolVersion = "graphi-coderank/1"

type responseBinding struct {
	Protocol       string `json:"protocol"`
	IdentityDigest string `json:"identity_digest"`
	Epoch          string `json:"epoch"`
}

type attestationResponse struct {
	responseBinding
	Dimension int `json:"dimension"`
}

type admitRequest struct {
	Protocol string `json:"protocol"`
	Text     string `json:"text"`
}

type admitResponse struct {
	responseBinding
	Text       string `json:"text"`
	TokenCount *int   `json:"token_count"`
}

type embedRequest struct {
	Protocol string   `json:"protocol"`
	Kind     string   `json:"kind"`
	Texts    []string `json:"texts"`
}

type embedResponse struct {
	responseBinding
	Vectors [][]float32 `json:"vectors"`
}

// decodeResponse enforces the wire schema. The adapter must additionally verify
// response binding, dimensions, cardinality, and values before accepting data.
func decodeResponse(r io.Reader, dst any) error {
	return decodeJSON(r, dst)
}

func decodeJSON(r io.Reader, dst any) error {
	decoder := json.NewDecoder(r)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(dst); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err != nil {
			return err
		}
		return errors.New("expected exactly one JSON value")
	}
	return nil
}
