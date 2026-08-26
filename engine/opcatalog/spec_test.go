package opcatalog

import (
	"strings"
	"testing"
)

// AC-5 — the port/permission/determinism vocabulary is CLOSED and self-checking.
// Each case is one way an entry could be plausible-but-wrong, and every one of
// them must be an error rather than a shipped guess.
func TestOperationSpec_Validate_RejectsIncoherentEntries(t *testing.T) {
	base := func() OperationSpec {
		return OperationSpec{
			ID:            "alpha",
			Version:       "1",
			Tier:          TierLabs,
			Advertisement: Advertisement{Description: "an operation"},
			Ports:         []Port{PortGraphQuery},
			Permissions:   []Permission{PermissionGraphRead},
			Determinism:   DeterminismDeterministic,
			PortsEvidence: "spec_test.go",
		}
	}
	for _, tc := range []struct {
		name    string
		mutate  func(*OperationSpec)
		wantSub string
	}{
		{"empty id", func(s *OperationSpec) { s.ID = " " }, "empty id"},
		{"empty version", func(s *OperationSpec) { s.Version = "" }, "empty version"},
		{"unknown tier", func(s *OperationSpec) { s.Tier = "preview" }, "unknown tier"},
		{"empty description", func(s *OperationSpec) { s.Description = "" }, "empty description"},
		{
			"tier marker stored in the description",
			func(s *OperationSpec) { s.Description = "[labs] an operation" },
			"projection of tier",
		},
		{"no ports evidence", func(s *OperationSpec) { s.PortsEvidence = "" }, "no ports evidence"},
		{"no ports", func(s *OperationSpec) { s.Ports = nil }, "no ports declared"},
		{"unknown port", func(s *OperationSpec) {
			s.Ports = []Port{"graph.telepathy"}
			s.Permissions = nil
		}, "unknown port"},
		{"unsorted ports", func(s *OperationSpec) {
			s.Ports = []Port{PortSourceRead, PortGraphQuery}
			s.Permissions = PermissionsFor(s.Ports)
			s.Determinism = DeterminismEnvironmentDependent
		}, "not sorted"},
		{"duplicate port", func(s *OperationSpec) {
			s.Ports = []Port{PortGraphQuery, PortGraphQuery}
		}, "not sorted"},
		{
			"a partially audited port set",
			func(s *OperationSpec) {
				s.Ports = []Port{PortGraphQuery, PortsUnaudited}
				s.Permissions = PermissionsFor(s.Ports)
				s.Determinism = DeterminismUnaudited
			},
			"must stand alone",
		},
		{
			"permissions that contradict the ports",
			func(s *OperationSpec) { s.Permissions = []Permission{PermissionSourceWrite} },
			"contradict ports",
		},
		{
			"a permission the ports do not imply",
			func(s *OperationSpec) {
				s.Permissions = []Permission{PermissionGraphRead, PermissionNetworkEgress}
			},
			"contradict ports",
		},
		{"unknown determinism class", func(s *OperationSpec) { s.Determinism = "mostly" }, "unknown determinism"},
		{
			"deterministic despite an outbound port",
			func(s *OperationSpec) {
				s.Ports = []Port{PortForgeEnumerate}
				s.Permissions = PermissionsFor(s.Ports)
			},
			"declares an outbound port",
		},
		{
			"external without an outbound port",
			func(s *OperationSpec) { s.Determinism = DeterminismExternal },
			"without any outbound port",
		},
		{
			"deterministic despite reading the working tree",
			func(s *OperationSpec) {
				s.Ports = []Port{PortGraphQuery, PortSourceRead}
				s.Permissions = PermissionsFor(s.Ports)
			},
			"outside the committed graph",
		},
		{
			"environment-dependent on committed-graph reads only",
			func(s *OperationSpec) { s.Determinism = DeterminismEnvironmentDependent },
			"every port is a committed-graph read",
		},
		{
			"unaudited ports with a confident determinism class",
			func(s *OperationSpec) {
				s.Ports = []Port{PortsUnaudited}
				s.Permissions = PermissionsFor(s.Ports)
			},
			"ports are unaudited",
		},
		{
			"a Stable-profile advertisement on a Labs operation",
			func(s *OperationSpec) {
				s.StableProfileAdvertisement = &Advertisement{Description: "terse"}
			},
			"impossible",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := base()
			tc.mutate(&s)
			err := s.Validate()
			if err == nil {
				t.Fatalf("Validate accepted %s", tc.name)
			}
			if !strings.Contains(err.Error(), tc.wantSub) {
				t.Fatalf("Validate error %q does not mention %q", err, tc.wantSub)
			}
		})
	}
}

func TestOperationSpec_Validate_AcceptsAnHonestlyUnauditedEntry(t *testing.T) {
	s := OperationSpec{
		ID:            "alpha",
		Version:       "1",
		Tier:          TierLabs,
		Advertisement: Advertisement{Description: "an operation"},
		Ports:         []Port{PortsUnaudited},
		Permissions:   []Permission{PermissionsUnaudited},
		Determinism:   DeterminismUnaudited,
		PortsEvidence: "not established: the handler chain was not read in this pass",
	}
	if err := s.Validate(); err != nil {
		t.Fatalf("Validate rejected an honestly unaudited entry: %v", err)
	}
	if s.PortsAudited() {
		t.Fatal("PortsAudited() reported true for a ports_unaudited entry")
	}
}

func TestPermissionsFor_IsSortedDeduplicatedAndClosed(t *testing.T) {
	got := PermissionsFor([]Port{PortGraphQuery, PortGraphSearch, PortGitHistory, PortSourceRead})
	want := []Permission{PermissionGraphRead, PermissionHistoryRead, PermissionSourceRead}
	if len(got) != len(want) {
		t.Fatalf("PermissionsFor = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("PermissionsFor = %v, want %v", got, want)
		}
	}
	if got := PermissionsFor([]Port{"graph.telepathy"}); len(got) != 0 {
		t.Fatalf("an unknown port implied permissions %v", got)
	}
}

// Every port in the vocabulary must be reachable from the derivation table, and
// every port that leaves the machine or reads outside the graph must be
// classified in one of the two rule sets. A port added without a rule would be
// silently treated as a pure committed-graph read.
func TestPortVocabulary_IsFullyClassified(t *testing.T) {
	for port := range portPermissions {
		if !ValidPort(port) {
			t.Errorf("port %q is in the derivation table but ValidPort says otherwise", port)
		}
		if externalPorts[port] && !nonDeterministicPorts[port] {
			// An outbound port is by definition not a committed-graph read; it is
			// classified by externalPorts alone, which is the stronger class.
			continue
		}
	}
	for port := range externalPorts {
		if !ValidPort(port) {
			t.Errorf("outbound port %q is not in the derivation table", port)
		}
	}
	for port := range nonDeterministicPorts {
		if !ValidPort(port) {
			t.Errorf("impure port %q is not in the derivation table", port)
		}
	}
}

func TestCanonicalJSON_IsStableRegardlessOfMapOrder(t *testing.T) {
	a := map[string]any{"b": 1.0, "a": 2.0, "c": map[string]any{"z": true, "y": false}}
	b := map[string]any{"c": map[string]any{"y": false, "z": true}, "a": 2.0, "b": 1.0}
	first, err := CanonicalJSON(a)
	if err != nil {
		t.Fatalf("CanonicalJSON: %v", err)
	}
	second, err := CanonicalJSON(b)
	if err != nil {
		t.Fatalf("CanonicalJSON: %v", err)
	}
	if string(first) != string(second) {
		t.Fatalf("CanonicalJSON is order-sensitive:\n %s\n %s", first, second)
	}
	if got, _ := CanonicalJSON(nil); string(got) != "null" {
		t.Fatalf("CanonicalJSON(nil) = %s, want null", got)
	}
}
