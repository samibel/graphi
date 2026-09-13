package compact

import (
	"testing"

	compactv9 "github.com/samibel/graphi/engine/agenttools/taskctx/compact/v9"
)

func TestPublicVersionTracksProductionSelectorVersion(t *testing.T) {
	if Version != compactv9.CompactTaskContextVersion {
		t.Fatalf("public compact version = %q, production selector = %q", Version, compactv9.CompactTaskContextVersion)
	}
}

func TestDefaultSourceBudgetUsesMeasuredWireFrontier(t *testing.T) {
	if DefaultSourceBudget != 325 {
		t.Fatalf("default source budget = %d, want measured 325-field frontier", DefaultSourceBudget)
	}
}
