// Package coderank defines the development-only pinned CodeRank sidecar contract.
package coderank

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/netip"
	"net/url"
	"os"
	"strconv"
	"strings"

	"github.com/samibel/graphi/engine/embed"
)

// QueryInstruction is the fixed query-only preparation for this protocol.
const QueryInstruction = "Represent this query for searching relevant code: "

// Manifest pins the embedding space independently of the serving process.
type Manifest struct {
	SchemaVersion int             `json:"schema_version"`
	Protocol      string          `json:"protocol"`
	Endpoint      string          `json:"endpoint"`
	Model         ArtifactPin     `json:"model"`
	Tokenizer     ArtifactPin     `json:"tokenizer"`
	Runtime       RuntimePin      `json:"runtime"`
	Dimension     int             `json:"dimension"`
	Precision     string          `json:"precision"`
	Normalization string          `json:"normalization"`
	Compute       string          `json:"compute"`
	Admission     AdmissionPin    `json:"admission"`
	Query         QueryProfilePin `json:"query"`
}

type ArtifactPin struct {
	ID       string `json:"id"`
	Revision string `json:"revision"`
	SHA256   string `json:"sha256"`
}

type RuntimePin struct {
	Name    string `json:"name"`
	Version string `json:"version"`
	SHA256  string `json:"sha256"`
}

type AdmissionPin struct {
	MaxTokens        int    `json:"max_tokens"`
	Reserve          int    `json:"reserve"`
	Algorithm        string `json:"algorithm"`
	AlgorithmVersion string `json:"algorithm_version"`
}

type QueryProfilePin struct {
	ID                string `json:"id"`
	Version           string `json:"version"`
	Instruction       string `json:"instruction"`
	InstructionSHA256 string `json:"instruction_sha256"`
}

// LoadManifest reads exactly one strict JSON manifest and validates its pins.
// Loading performs no network requests, DNS resolution, or process launches.
func LoadManifest(path string) (Manifest, error) {
	f, err := os.Open(path)
	if err != nil {
		return Manifest{}, fmt.Errorf("coderank: open manifest: %w", err)
	}
	defer f.Close()
	var m Manifest
	if err := decodeJSON(f, &m); err != nil {
		return Manifest{}, fmt.Errorf("coderank: decode manifest: %w", err)
	}
	if err := m.Validate(); err != nil {
		return Manifest{}, err
	}
	return m, nil
}

// Validate checks the supported profile and literal loopback endpoint. It does
// not prove artifact contents; the serving sidecar must attest those separately.
func (m Manifest) Validate() error {
	if m.SchemaVersion != 1 || m.Protocol != ProtocolVersion {
		return errors.New("coderank: unsupported manifest schema or protocol")
	}
	if m.Dimension != 768 || m.Precision != "float32" || m.Normalization != "l2" || m.Compute != "cpu" {
		return errors.New("coderank: profile requires dimension 768, float32, l2, and cpu")
	}
	if err := validateEndpoint(m.Endpoint); err != nil {
		return err
	}
	for _, field := range []struct{ name, value string }{
		{"model.id", m.Model.ID}, {"model.revision", m.Model.Revision},
		{"tokenizer.id", m.Tokenizer.ID}, {"tokenizer.revision", m.Tokenizer.Revision},
		{"runtime.name", m.Runtime.Name}, {"runtime.version", m.Runtime.Version},
		{"admission.algorithm", m.Admission.Algorithm}, {"admission.algorithm_version", m.Admission.AlgorithmVersion},
		{"query.id", m.Query.ID}, {"query.version", m.Query.Version},
	} {
		if strings.TrimSpace(field.value) == "" {
			return fmt.Errorf("coderank: %s must be nonempty", field.name)
		}
	}
	for _, field := range []struct{ name, value string }{
		{"model.sha256", m.Model.SHA256}, {"tokenizer.sha256", m.Tokenizer.SHA256},
		{"runtime.sha256", m.Runtime.SHA256}, {"query.instruction_sha256", m.Query.InstructionSHA256},
	} {
		if !validDigest(field.value) {
			return fmt.Errorf("coderank: %s must be a nonzero 64-character lowercase SHA-256 digest", field.name)
		}
	}
	if m.Admission.MaxTokens <= 0 || m.Admission.Reserve < 0 {
		return errors.New("coderank: admission max_tokens must be positive and reserve must be nonnegative")
	}
	if m.Query.Instruction != QueryInstruction {
		return errors.New("coderank: unsupported query instruction")
	}
	sum := sha256.Sum256([]byte(m.Query.Instruction))
	if hex.EncodeToString(sum[:]) != m.Query.InstructionSHA256 {
		return errors.New("coderank: query instruction digest mismatch")
	}
	return nil
}

func validDigest(value string) bool {
	if len(value) != 64 || value == strings.Repeat("0", 64) {
		return false
	}
	for _, c := range value {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

func validateEndpoint(endpoint string) error {
	u, err := url.Parse(endpoint)
	if err != nil || u.Scheme != "http" || u.Opaque != "" || u.User != nil ||
		u.Path != "" || u.RawQuery != "" || u.ForceQuery || strings.Contains(endpoint, "#") {
		return errors.New("coderank: endpoint must be an http loopback origin without credentials, path, query, or fragment")
	}
	host := u.Hostname()
	if host != "localhost" && host != "::1" {
		addr, err := netip.ParseAddr(host)
		if err != nil || !addr.Is4() || addr.As4()[0] != 127 {
			return errors.New("coderank: endpoint host must be localhost, ::1, or literal 127.0.0.0/8")
		}
	}
	if port := u.Port(); port != "" {
		n, err := strconv.Atoi(port)
		if err != nil || n < 1 || n > 65535 {
			return errors.New("coderank: endpoint port must be between 1 and 65535")
		}
	} else if strings.HasSuffix(u.Host, ":") {
		return errors.New("coderank: endpoint port must not be empty")
	}
	return nil
}

// IdentityDigest hashes all durable fields in the order below. Each value is
// encoded as its UTF-8 byte length in decimal, a colon, and the value; fields
// are separated by a newline with no final newline. Integers use base 10.
// Endpoint and process epoch are excluded so a pinned space can be relocated.
func (m Manifest) IdentityDigest() string {
	parts := []string{
		strconv.Itoa(m.SchemaVersion), m.Protocol,
		m.Model.ID, m.Model.Revision, m.Model.SHA256,
		m.Tokenizer.ID, m.Tokenizer.Revision, m.Tokenizer.SHA256,
		m.Runtime.Name, m.Runtime.Version, m.Runtime.SHA256,
		strconv.Itoa(m.Dimension), m.Precision, m.Normalization, m.Compute,
		strconv.Itoa(m.Admission.MaxTokens), strconv.Itoa(m.Admission.Reserve), m.Admission.Algorithm, m.Admission.AlgorithmVersion,
		m.Query.ID, m.Query.Version, m.Query.Instruction, m.Query.InstructionSHA256,
	}
	var canonical strings.Builder
	for i, value := range parts {
		if i > 0 {
			canonical.WriteByte('\n')
		}
		canonical.WriteString(strconv.Itoa(len(value)))
		canonical.WriteByte(':')
		canonical.WriteString(value)
	}
	sum := sha256.Sum256([]byte(canonical.String()))
	return hex.EncodeToString(sum[:])
}

// AdmissionSpec exposes the tokenizer and preparation pins to the document builder.
func (m Manifest) AdmissionSpec() embed.AdmissionSpec {
	return embed.AdmissionSpec{
		TokenizerID: m.Tokenizer.ID, TokenizerSHA256: m.Tokenizer.SHA256, TokenizerVersion: m.Tokenizer.Revision,
		MaxTokens: m.Admission.MaxTokens, Reserve: m.Admission.Reserve,
		Algorithm: m.Admission.Algorithm, AlgorithmVersion: m.Admission.AlgorithmVersion,
	}
}
