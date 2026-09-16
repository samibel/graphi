package coderank

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/samibel/graphi/engine/embed"
)

func validManifest() Manifest {
	instruction := "Represent this query for searching relevant code: "
	digest := sha256.Sum256([]byte(instruction))
	return Manifest{
		SchemaVersion: 1, Protocol: "graphi-coderank/1", Endpoint: "http://127.0.0.1:8765",
		Model:     ArtifactPin{ID: "nomic-ai/CodeRankEmbed", Revision: "model-revision", SHA256: strings.Repeat("a", 64)},
		Tokenizer: ArtifactPin{ID: "nomic-ai/CodeRankEmbed", Revision: "tokenizer-revision", SHA256: strings.Repeat("b", 64)},
		Runtime:   RuntimePin{Name: "sentence-transformers", Version: "runtime-version", SHA256: strings.Repeat("c", 64)},
		Dimension: 768, Precision: "float32", Normalization: "l2", Compute: "cpu",
		Admission: AdmissionPin{MaxTokens: 8190, Reserve: 2, Algorithm: "first-n-tokens", AlgorithmVersion: "1"},
		Query:     QueryProfilePin{ID: "coderank-code-search", Version: "1", Instruction: instruction, InstructionSHA256: hex.EncodeToString(digest[:])},
	}
}

// Omitting any embedding-space pin must invalidate durable identity.
func TestManifestValidateAndIdentityDigest(t *testing.T) {
	m := validManifest()
	if err := m.Validate(); err != nil {
		t.Fatal(err)
	}
	if got := m.IdentityDigest(); len(got) != 64 {
		t.Fatalf("digest=%q", got)
	}
	var visit func(reflect.Value, string)
	visit = func(v reflect.Value, path string) {
		for i := 0; i < v.NumField(); i++ {
			field := v.Field(i)
			name := path + v.Type().Field(i).Name
			if name == "Endpoint" {
				continue
			}
			if field.Kind() == reflect.Struct {
				visit(field, name+".")
				continue
			}
			t.Run(name, func(t *testing.T) {
				before := reflect.New(field.Type()).Elem()
				before.Set(field)
				defer field.Set(before)
				if field.Kind() == reflect.String {
					field.SetString(field.String() + "changed")
				} else {
					field.SetInt(field.Int() + 1)
				}
				if m.IdentityDigest() == validManifest().IdentityDigest() {
					t.Fatal("changed pin did not change identity")
				}
			})
		}
	}
	visit(reflect.ValueOf(&m).Elem(), "")
	for _, endpoint := range []string{"http://localhost:8766", "http://[::1]:8767"} {
		m.Endpoint = endpoint
		if m.IdentityDigest() != validManifest().IdentityDigest() {
			t.Fatal("loopback relocation changed identity")
		}
	}
	// Delimiter characters inside fields cannot move bytes into a neighboring field.
	a, b := validManifest(), validManifest()
	a.Model.ID, a.Model.Revision = "a\nb", "c"
	b.Model.ID, b.Model.Revision = "a", "b\nc"
	if a.IdentityDigest() == b.IdentityDigest() {
		t.Fatal("field boundary collision")
	}
}

func TestManifestEndpoints(t *testing.T) {
	for _, endpoint := range []string{
		"http://127.0.0.1:8765", "http://127.255.255.255:80", "http://127.1.2.3", "http://[::1]:8765", "http://localhost:8765",
	} {
		t.Run("accept/"+endpoint, func(t *testing.T) {
			m := validManifest()
			m.Endpoint = endpoint
			if err := m.Validate(); err != nil {
				t.Fatal(err)
			}
		})
	}
	for _, endpoint := range []string{
		"", "https://example.com:8443", "https://127.0.0.1:8443", "http://192.168.1.5:8080",
		"http://user:pass@127.0.0.1:8080", "http://@127.0.0.1:8080", "http://coderank.local:8080",
		"http://localhost.example:8080", "http://localhost.:8080", "http://127.0.0.1/", "http://127.0.0.1/path",
		"http://127.0.0.1?", "http://127.0.0.1?x=1", "http://127.0.0.1#", "http://127.0.0.1#x",
		"http://[::2]:8080", "http://[::ffff:127.0.0.1]:8080", "http://[::1%25lo0]:8080",
		"http://2130706433:8080", "http://127.1:8080", "http://0177.0.0.1:8080", "//127.0.0.1:8080",
		"http://127.0.0.1:", "http://127.0.0.1:0", "http://127.0.0.1:65536", "http://127.0.0.1:nope",
	} {
		t.Run("reject/"+endpoint, func(t *testing.T) {
			m := validManifest()
			m.Endpoint = endpoint
			if err := m.Validate(); err == nil {
				t.Fatalf("accepted %q", endpoint)
			}
		})
	}
}

func TestManifestRejectsInvalidPins(t *testing.T) {
	cases := map[string]func(*Manifest){
		"schema":                      func(m *Manifest) { m.SchemaVersion = 2 },
		"protocol":                    func(m *Manifest) { m.Protocol = "graphi-coderank/2" },
		"dimension":                   func(m *Manifest) { m.Dimension = 512 },
		"precision":                   func(m *Manifest) { m.Precision = "float16" },
		"normalization":               func(m *Manifest) { m.Normalization = "none" },
		"compute":                     func(m *Manifest) { m.Compute = "gpu" },
		"max tokens":                  func(m *Manifest) { m.Admission.MaxTokens = 0 },
		"negative tokens":             func(m *Manifest) { m.Admission.MaxTokens = -1 },
		"negative reserve":            func(m *Manifest) { m.Admission.Reserve = -1 },
		"model id":                    func(m *Manifest) { m.Model.ID = "" },
		"model revision":              func(m *Manifest) { m.Model.Revision = " " },
		"tokenizer id":                func(m *Manifest) { m.Tokenizer.ID = "" },
		"tokenizer revision":          func(m *Manifest) { m.Tokenizer.Revision = "" },
		"runtime name":                func(m *Manifest) { m.Runtime.Name = "" },
		"runtime version":             func(m *Manifest) { m.Runtime.Version = "" },
		"algorithm":                   func(m *Manifest) { m.Admission.Algorithm = "" },
		"algorithm version":           func(m *Manifest) { m.Admission.AlgorithmVersion = "" },
		"query id":                    func(m *Manifest) { m.Query.ID = "" },
		"query version":               func(m *Manifest) { m.Query.Version = "" },
		"instruction digest mismatch": func(m *Manifest) { m.Query.InstructionSHA256 = strings.Repeat("a", 64) },
		"different instruction": func(m *Manifest) {
			m.Query.Instruction = "different instruction"
			sum := sha256.Sum256([]byte(m.Query.Instruction))
			m.Query.InstructionSHA256 = hex.EncodeToString(sum[:])
		},
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			m := validManifest()
			mutate(&m)
			if err := m.Validate(); err == nil {
				t.Fatal("accepted invalid pin")
			}
		})
	}
	for _, bad := range []string{"", strings.Repeat("A", 64), strings.Repeat("g", 64), strings.Repeat("a", 63), strings.Repeat("a", 65), strings.Repeat("0", 64)} {
		for _, field := range []string{"model", "tokenizer", "runtime", "instruction"} {
			t.Run(field+"/"+bad, func(t *testing.T) {
				m := validManifest()
				switch field {
				case "model":
					m.Model.SHA256 = bad
				case "tokenizer":
					m.Tokenizer.SHA256 = bad
				case "runtime":
					m.Runtime.SHA256 = bad
				case "instruction":
					m.Query.InstructionSHA256 = bad
				}
				if err := m.Validate(); err == nil {
					t.Fatal("accepted invalid digest")
				}
			})
		}
	}
}

func TestManifestAdmissionSpec(t *testing.T) {
	want := embed.AdmissionSpec{
		TokenizerID: "nomic-ai/CodeRankEmbed", TokenizerSHA256: strings.Repeat("b", 64), TokenizerVersion: "tokenizer-revision",
		MaxTokens: 8190, Reserve: 2, Algorithm: "first-n-tokens", AlgorithmVersion: "1",
	}
	if got := validManifest().AdmissionSpec(); got != want {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}

func TestManifestAllowsZeroReserve(t *testing.T) {
	m := validManifest()
	m.Admission.Reserve = 0
	if err := m.Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestLoadManifest(t *testing.T) {
	m := validManifest()
	data, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "manifest.json")
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	if got, err := LoadManifest(path); err != nil || got != m {
		t.Fatalf("got %+v, err=%v", got, err)
	}
	for _, invalid := range []string{
		`{`, `null`, `{}`, string(data) + ` {}`, string(data) + ` trailing`,
		strings.Replace(string(data), `"schema_version":1`, `"schema_version":2`, 1),
		strings.Replace(string(data), `"schema_version":1`, `"unexpected":true,"schema_version":1`, 1),
		strings.Replace(string(data), `"id":"nomic-ai/CodeRankEmbed"`, `"unexpected":true,"id":"nomic-ai/CodeRankEmbed"`, 1),
	} {
		if err := os.WriteFile(path, []byte(invalid), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := LoadManifest(path); err == nil {
			t.Fatalf("accepted invalid JSON %s", invalid)
		}
	}
	if _, err := LoadManifest(path + ".missing"); err == nil {
		t.Fatal("accepted missing file")
	}
}

func TestDecodeResponseRejectsUnknownAndTrailingFields(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		out        func() any
	}{
		{"attestation", `{"protocol":"graphi-coderank/1","identity_digest":"digest","epoch":"epoch","dimension":768}`, func() any { return &attestationResponse{} }},
		{"admit", `{"protocol":"graphi-coderank/1","identity_digest":"digest","epoch":"epoch","text":"code","token_count":1}`, func() any { return &admitResponse{} }},
		{"embed", `{"protocol":"graphi-coderank/1","identity_digest":"digest","epoch":"epoch","vectors":[[1,0]]}`, func() any { return &embedResponse{} }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := decodeResponse(strings.NewReader(tc.body), tc.out()); err != nil {
				t.Fatal(err)
			}
			for _, body := range []string{`{"unexpected":true,` + tc.body[1:], tc.body + ` {}`, tc.body + ` trailing`} {
				if err := decodeResponse(strings.NewReader(body), tc.out()); err == nil {
					t.Fatalf("accepted %s", body)
				}
			}
		})
	}
}
