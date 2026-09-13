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
