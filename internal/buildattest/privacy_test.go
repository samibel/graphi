package buildattest

import (
	"reflect"
	"strings"
	"testing"
)

func validPrivacy() Privacy {
	return Privacy{
		SchemaVersion:  PrivacySchemaVersion,
		Status:         "PASS",
		GateID:         PrivacyGateID,
		Scope:          PrivacyScope,
		SourceRevision: strings.Repeat("a", 40),
		EvidenceDigest: strings.Repeat("b", 64),
		CGOEnabled:     "0",
		GOOS:           "linux",
		GOARCH:         "amd64",
	}
}

func TestPrivacyRoundTrip(t *testing.T) {
	want := validPrivacy()
	encoded, err := Encode(want)
	if err != nil {
		t.Fatal(err)
	}
	got, err := Decode(encoded)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("decoded attestation = %+v, want %+v", got, want)
	}
}

func TestPrivacyRejectsUnbackedOrIncompleteClaims(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*Privacy)
	}{
		{name: "non-pass", mutate: func(a *Privacy) { a.Status = "UNVERIFIED" }},
		{name: "unknown gate", mutate: func(a *Privacy) { a.GateID = "other" }},
		{name: "missing revision", mutate: func(a *Privacy) { a.SourceRevision = "" }},
		{name: "missing evidence digest", mutate: func(a *Privacy) { a.EvidenceDigest = "" }},
		{name: "cgo enabled", mutate: func(a *Privacy) { a.CGOEnabled = "1" }},
		{name: "missing platform", mutate: func(a *Privacy) { a.GOARCH = "" }},
		{name: "unsorted tags", mutate: func(a *Privacy) { a.BuildTags = []string{"z", "a"} }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := validPrivacy()
			tc.mutate(&a)
			if _, err := Encode(a); err == nil {
				t.Fatal("invalid attestation was accepted")
			}
		})
	}
}
