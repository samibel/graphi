package http

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/samibel/graphi/engine/embed"
	"github.com/samibel/graphi/surfaces/client"
)

func TestHTTP_SemanticStatus_AC2_ByteIdentity(t *testing.T) {
	t.Setenv(LabsEnvVar, "1")
	meta := t.TempDir()
	reg := embed.NewRegistry()
	if err := reg.Register(embed.NewMockEmbedder(8)); err != nil {
		t.Fatal(err)
	}
	want, _, err := client.SemanticStatus(context.Background(), client.SemanticStatusOptions{MetaDir: meta, Embedder: reg})
	if err != nil {
		t.Fatal(err)
	}
	server := New(&stubClient{}, nil).WithEmbedderRegistry(reg)
	req := httptest.NewRequest("GET", "http://127.0.0.1/semantic/status?meta="+url.QueryEscape(meta), nil)
	recorder := httptest.NewRecorder()
	server.Handler().ServeHTTP(recorder, req)
	if recorder.Code != 200 {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.Bytes())
	}
	if !bytes.Equal(recorder.Body.Bytes(), want) {
		t.Fatalf("HTTP changed canonical bytes:\n got %q\nwant %q", recorder.Body.Bytes(), want)
	}
}

func TestHTTP_SemanticStatus_AC3_NoEmbedderSetupRepair(t *testing.T) {
	t.Setenv(LabsEnvVar, "1")
	t.Setenv(embed.EnvSelector, "")
	server := New(&stubClient{}, nil).WithEmbedderRegistry(embed.NewRegistry())
	req := httptest.NewRequest("GET", "http://127.0.0.1/semantic/status", nil)
	recorder := httptest.NewRecorder()
	server.Handler().ServeHTTP(recorder, req)
	var doc struct {
		Configured bool   `json:"configured"`
		Repair     string `json:"repair"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &doc); err != nil {
		t.Fatal(err)
	}
	if doc.Configured || doc.Repair != "graphi setup-embedder" {
		t.Fatalf("configured=%v repair=%q", doc.Configured, doc.Repair)
	}
}
