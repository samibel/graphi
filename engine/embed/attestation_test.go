package embed

import (
	"strings"
	"testing"
)

func TestValidateRuntimeAttestation(t *testing.T) {
	valid := RuntimeAttestation{
		IdentityDigest: strings.Repeat("a", 64),
		Epoch:          "pid-42-start-1700000000",
	}
	if err := ValidateRuntimeAttestation(valid); err != nil {
		t.Fatal(err)
	}

	for _, bad := range []RuntimeAttestation{
		{IdentityDigest: "abc", Epoch: valid.Epoch},
		{IdentityDigest: strings.Repeat("A", 64), Epoch: valid.Epoch},
		{IdentityDigest: valid.IdentityDigest, Epoch: ""},
		{IdentityDigest: valid.IdentityDigest, Epoch: strings.Repeat("x", 129)},
	} {
		if err := ValidateRuntimeAttestation(bad); err == nil {
			t.Fatalf("accepted %#v", bad)
		}
	}
}
