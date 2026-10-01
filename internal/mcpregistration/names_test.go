package mcpregistration_test

import (
	"strings"
	"testing"

	"github.com/samibel/graphi/internal/mcpregistration"
)

func TestExistingNamesNeverRename(t *testing.T) {
	existing := mcpregistration.Registration{
		CheckoutID: "1111111111111111",
		Name:       "graphi-service-api",
		RepoFile:   "/state/1111111111111111/repo.json",
	}
	names, err := mcpregistration.AllocateNames([]mcpregistration.NameCandidate{
		{CheckoutID: existing.CheckoutID, Root: "/new/location/service-api"},
		{CheckoutID: "2222222222222222", Root: "/work/beta/service-api"},
	}, mcpregistration.NameInventory{Existing: []mcpregistration.Registration{existing}, HomeDir: "/home/tester"})
	if err != nil {
		t.Fatalf("allocate names: %v", err)
	}
	if got := names[existing.CheckoutID]; got != existing.Name {
		t.Fatalf("existing name = %q, want %q", got, existing.Name)
	}
	if got := names["2222222222222222"]; got == existing.Name {
		t.Fatalf("new checkout reused persistent name %q", got)
	}
}

func TestForeignNameReserved(t *testing.T) {
	names, err := mcpregistration.AllocateNames([]mcpregistration.NameCandidate{
		{CheckoutID: "1111111111111111", Root: "/work/alpha/service-api"},
	}, mcpregistration.NameInventory{Reserved: []string{"graphi-service-api"}, HomeDir: "/home/tester"})
	if err != nil {
		t.Fatalf("allocate names: %v", err)
	}
	if got := names["1111111111111111"]; got != "graphi-service-api-alpha" {
		t.Fatalf("allocated name = %q, want parent-qualified name", got)
	}
}

func TestExplicitNameConflict(t *testing.T) {
	_, err := mcpregistration.AllocateNames([]mcpregistration.NameCandidate{
		{CheckoutID: "1111111111111111", Root: "/work/service", ExplicitName: "graphi-chosen"},
	}, mcpregistration.NameInventory{Reserved: []string{"graphi-chosen"}, HomeDir: "/home/tester"})
	if err == nil {
		t.Fatal("explicit foreign name conflict succeeded")
	}

	_, err = mcpregistration.AllocateNames([]mcpregistration.NameCandidate{
		{CheckoutID: "1111111111111111", Root: "/work/service", ExplicitName: "chosen"},
	}, mcpregistration.NameInventory{HomeDir: "/home/tester"})
	if err == nil {
		t.Fatal("explicit name without graphi- prefix succeeded")
	}
}

func TestLongOrEmptySlug(t *testing.T) {
	longID := "1234567890abcdef"
	emptyID := "fedcba0987654321"
	names, err := mcpregistration.AllocateNames([]mcpregistration.NameCandidate{
		{CheckoutID: longID, Root: "/work/" + strings.Repeat("Very_Long_Service_", 8)},
		{CheckoutID: emptyID, Root: "/work/你好"},
	}, mcpregistration.NameInventory{HomeDir: "/home/tester"})
	if err != nil {
		t.Fatalf("allocate names: %v", err)
	}
	if got := names[longID]; len(got) > 48 || !strings.HasPrefix(got, "graphi-") {
		t.Fatalf("long name = %q (%d bytes), want graphi- prefix and <= 48 bytes", got, len(got))
	}
	if got := names[emptyID]; got != "graphi-repo" {
		t.Fatalf("empty normalized slug = %q, want graphi-repo", got)
	}
}
