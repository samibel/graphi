package http

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
)

// contract.schema.json is the hand-authored Go<->TS single source of truth
// (AC-1/AC-6). These tests load it and validate representative handler responses
// against the relevant definition, failing CI on drift. A full JSON-schema
// engine would be an external dependency the surface forbids, so we run a
// focused validator covering exactly the keywords the contract uses:
// `required`, `additionalProperties:false`, `const`, and `enum`.

type jsonSchema struct {
	XSchemaVersion int                   `json:"x-schema-version"`
	Definitions    map[string]schemaNode `json:"definitions"`
}

type schemaNode struct {
	Type                 string                `json:"type"`
	Const                json.RawMessage       `json:"const"`
	Enum                 []string              `json:"enum"`
	Required             []string              `json:"required"`
	AdditionalProperties *bool                 `json:"additionalProperties"`
	Properties           map[string]schemaNode `json:"properties"`
	Items                *schemaNode           `json:"items"`
}

func loadContract(t *testing.T) jsonSchema {
	t.Helper()
	b, err := os.ReadFile("contract.schema.json")
	if err != nil {
		t.Fatalf("read contract.schema.json: %v", err)
	}
	if !json.Valid(b) {
		t.Fatal("contract.schema.json is not valid JSON")
	}
	var s jsonSchema
	if err := json.Unmarshal(b, &s); err != nil {
		t.Fatalf("decode contract schema: %v", err)
	}
	if s.XSchemaVersion != SchemaVersion {
		t.Fatalf("contract x-schema-version=%d, code SchemaVersion=%d (drift)", s.XSchemaVersion, SchemaVersion)
	}
	return s
}

// validate checks a decoded JSON value against a schema node. It enforces the
// subset of JSON-schema keywords used by contract.schema.json.
func validate(node schemaNode, v any, path string) error {
	if len(node.Const) > 0 {
		var want any
		_ = json.Unmarshal(node.Const, &want)
		if fmt.Sprint(v) != fmt.Sprint(want) {
			return fmt.Errorf("%s: const mismatch: got %v want %v", path, v, want)
		}
	}
	if len(node.Enum) > 0 {
		s, ok := v.(string)
		if !ok {
			return fmt.Errorf("%s: enum value not a string: %T", path, v)
		}
		found := false
		for _, e := range node.Enum {
			if e == s {
				found = true
				break
			}
		}
		if !found {
			return fmt.Errorf("%s: %q not in enum %v", path, s, node.Enum)
		}
	}
	switch node.Type {
	case "object":
		m, ok := v.(map[string]any)
		if !ok {
			return fmt.Errorf("%s: expected object, got %T", path, v)
		}
		for _, req := range node.Required {
			if _, present := m[req]; !present {
				return fmt.Errorf("%s: missing required property %q", path, req)
			}
		}
		if node.AdditionalProperties != nil && !*node.AdditionalProperties {
			for k := range m {
				if _, declared := node.Properties[k]; !declared {
					return fmt.Errorf("%s: additional property %q not allowed", path, k)
				}
			}
		}
		for k, child := range node.Properties {
			if cv, present := m[k]; present {
				if err := validate(child, cv, path+"."+k); err != nil {
					return err
				}
			}
		}
	case "array":
		arr, ok := v.([]any)
		if !ok {
			return fmt.Errorf("%s: expected array, got %T", path, v)
		}
		if node.Items != nil {
			for i, item := range arr {
				if err := validate(*node.Items, item, fmt.Sprintf("%s[%d]", path, i)); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func decodeBody(t *testing.T, body []byte) any {
	t.Helper()
	var v any
	if err := json.Unmarshal(body, &v); err != nil {
		t.Fatalf("decode response body %q: %v", body, err)
	}
	return v
}

// TestContract_QueryEnvelopeConforms validates a success envelope from /query.
func TestContract_QueryEnvelopeConforms(t *testing.T) {
	schema := loadContract(t)
	srv, _, _ := newServer(t)
	req := newLocalRequest(http.MethodGet, "/query/callers?symbol=pkg.Foo", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("code=%d", rec.Code)
	}
	if err := validate(schema.Definitions["envelope"], decodeBody(t, rec.Body.Bytes()), "envelope"); err != nil {
		t.Fatalf("query envelope drift: %v", err)
	}
}

// TestContract_ErrorEnvelopeConforms validates a 400 and a 412 error body.
func TestContract_ErrorEnvelopeConforms(t *testing.T) {
	schema := loadContract(t)
	srv, _, _ := newServer(t)

	cases := []struct {
		name   string
		target string
		header [2]string
	}{
		{"bad_request", "/query/bogus?symbol=x", [2]string{}},
		{"schema_mismatch", "/query/callers?symbol=x", [2]string{"X-Graphi-Schema-Version", "999"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			req := newLocalRequest(http.MethodGet, c.target, nil)
			if c.header[0] != "" {
				req.Header.Set(c.header[0], c.header[1])
			}
			rec := httptest.NewRecorder()
			srv.Handler().ServeHTTP(rec, req)
			if err := validate(schema.Definitions["errorEnvelope"], decodeBody(t, rec.Body.Bytes()), "errorEnvelope"); err != nil {
				t.Fatalf("error envelope drift (%s): %v", c.name, err)
			}
		})
	}
}

// TestContract_ContractResponseConforms validates the /contract response.
func TestContract_ContractResponseConforms(t *testing.T) {
	schema := loadContract(t)
	srv, _, _ := newServer(t)
	srv.WithDescriptors([]string{"impact", "call-chain"})
	req := newLocalRequest(http.MethodGet, "/contract", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("code=%d", rec.Code)
	}
	// /contract is wrapped in the standard envelope; validate the wrapper then
	// the inner contract document.
	var env struct {
		SchemaVersion int             `json:"schema_version"`
		Payload       json.RawMessage `json:"payload"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode contract envelope: %v", err)
	}
	if err := validate(schema.Definitions["contract"], decodeBody(t, env.Payload), "contract"); err != nil {
		t.Fatalf("contract response drift: %v", err)
	}
}
