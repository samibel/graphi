package mcpregistration_test

import (
	"testing"

	"github.com/samibel/graphi/internal/mcpconfig"
	"github.com/samibel/graphi/internal/mcpregistration"
)

func TestManualEntryRequiresAdoption(t *testing.T) {
	manifest := mcpregistration.NewManifest()
	desired := mcpconfig.GraphiEntry("/bin/graphi", []string{"mcp", "-db", "/state/one/db.sqlite", "-meta", "/state/one/meta"})
	current := map[string]any{"command": desired.Command, "args": []any{"mcp", "-db", "/state/one/db.sqlite", "-meta", "/state/one/meta"}}
	err := mcpregistration.AuthorizeEntry(manifest, mcpregistration.OwnershipRequest{
		CheckoutID: "1111111111111111", ClientID: "claude", ConfigPath: "/config/claude.json", ServerName: "graphi-service",
		Current: current, Desired: desired,
	})
	if err == nil {
		t.Fatal("manual entry was managed without explicit adoption")
	}

	if err := mcpregistration.AuthorizeEntry(manifest, mcpregistration.OwnershipRequest{
		CheckoutID: "1111111111111111", ClientID: "claude", ConfigPath: "/config/claude.json", ServerName: "graphi-service",
		Current: current, Desired: desired, Adopt: true,
	}); err != nil {
		t.Fatalf("matching explicit adoption failed: %v", err)
	}
}

func TestAdoptRejectsDifferentStore(t *testing.T) {
	desired := mcpconfig.GraphiEntry("/bin/graphi", []string{"mcp", "-db", "/state/one/db.sqlite", "-meta", "/state/one/meta"})
	current := map[string]any{"command": "/bin/graphi", "args": []any{"mcp", "-db", "/state/two/db.sqlite", "-meta", "/state/two/meta"}}
	err := mcpregistration.AuthorizeEntry(mcpregistration.NewManifest(), mcpregistration.OwnershipRequest{
		CheckoutID: "1111111111111111", ClientID: "claude", ConfigPath: "/config/claude.json", ServerName: "graphi-service",
		Current: current, Desired: desired, Adopt: true,
	})
	if err == nil {
		t.Fatal("adoption accepted an entry bound to a different store")
	}
}

func TestPendingReceiptRecovery(t *testing.T) {
	receipt := mcpregistration.ClientReceipt{
		CheckoutID: "1111111111111111", ClientID: "claude", ConfigPath: "/config/claude.json", ServerName: "graphi-service",
		ManagedDigest: "managed", EntryDigest: "entry",
	}
	manifest := mcpregistration.NewManifest()
	manifest.Pending = []mcpregistration.PendingChange{{
		ID: "change-1", BeforeDigest: "before", TargetDigest: "target", Receipt: receipt,
	}}
	status, err := mcpregistration.RecoverPending(&manifest, "change-1", "target")
	if err != nil || status != mcpregistration.RecoveryCompleted {
		t.Fatalf("target recovery = (%q, %v)", status, err)
	}
	if len(manifest.Pending) != 0 || len(manifest.Receipts) != 1 || manifest.Receipts[0].EntryDigest != "entry" {
		t.Fatalf("completed recovery did not confirm receipt: %#v", manifest)
	}

	manifest.Pending = []mcpregistration.PendingChange{{ID: "change-2", BeforeDigest: "before", TargetDigest: "target", Receipt: receipt}}
	status, err = mcpregistration.RecoverPending(&manifest, "change-2", "before")
	if err != nil || status != mcpregistration.RecoveryRetry || len(manifest.Pending) != 0 {
		t.Fatalf("before-state recovery = (%q, %v), pending=%#v", status, err, manifest.Pending)
	}

	manifest.Pending = []mcpregistration.PendingChange{{ID: "change-3", BeforeDigest: "before", TargetDigest: "target", Receipt: receipt}}
	status, err = mcpregistration.RecoverPending(&manifest, "change-3", "external")
	if err == nil || status != mcpregistration.RecoveryConflict || len(manifest.Pending) != 1 {
		t.Fatalf("conflict recovery = (%q, %v), pending=%#v", status, err, manifest.Pending)
	}
}

func TestPendingRemovalRecoveryDropsReceipt(t *testing.T) {
	receipt := mcpregistration.ClientReceipt{
		CheckoutID: "1111111111111111", ClientID: "claude", ConfigPath: "/config/claude.json", ServerName: "graphi-service",
		ManagedDigest: "managed", EntryDigest: "entry",
	}
	manifest := mcpregistration.NewManifest()
	manifest.Receipts = []mcpregistration.ClientReceipt{receipt}
	manifest.Pending = []mcpregistration.PendingChange{{
		ID: "remove-1", BeforeDigest: "before", TargetDigest: "target", Receipt: receipt, Remove: true,
	}}
	status, err := mcpregistration.RecoverPending(&manifest, "remove-1", "target")
	if err != nil || status != mcpregistration.RecoveryCompleted {
		t.Fatalf("removal recovery = (%q, %v)", status, err)
	}
	if len(manifest.Pending) != 0 || len(manifest.Receipts) != 0 {
		t.Fatalf("completed removal recovery retained ownership: %#v", manifest)
	}
}
