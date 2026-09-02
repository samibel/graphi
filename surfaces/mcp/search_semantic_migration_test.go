package mcp

import (
	"context"
	"encoding/json"
	"testing"
)

func TestSearchSemanticMigration_RejectsMissingSymbol(t *testing.T) {
	server := NewServerWithClient(allToolsClient{}, WithLabs())
	_, rpcErr := server.toolsCall(context.Background(), json.RawMessage(`{"name":"search_semantic","arguments":{"depth":3}}`))
	if rpcErr == nil || rpcErr.Code != -32602 || rpcErr.Message != "missing required argument: symbol" {
		t.Fatalf("error=%+v, want exact legacy missing-symbol rejection", rpcErr)
	}
}
