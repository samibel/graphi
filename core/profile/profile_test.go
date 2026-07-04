package profile

import (
	"os"
	"testing"
)

func TestParse(t *testing.T) {
	cases := []struct {
		in      string
		want    Profile
		wantErr bool
	}{
		{"fast", Fast, false},
		{"balanced", Balanced, false},
		{"deep", Deep, false},
		{"FAST", Fast, false},
		{" Balanced ", Balanced, false},
		{"turbo", "", true},
		{"", "", true},
	}
	for _, c := range cases {
		got, err := Parse(c.in)
		if (err != nil) != c.wantErr {
			t.Fatalf("Parse(%q) error = %v, wantErr %v", c.in, err, c.wantErr)
		}
		if err == nil && got != c.want {
			t.Fatalf("Parse(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}

func TestResolveProfile(t *testing.T) {
	fast := "fast"
	cases := []struct {
		name    string
		flag    *string
		env     string
		want    Profile
		wantErr bool
	}{
		{"default", nil, "", Balanced, false},
		{"env only", nil, "deep", Deep, false},
		{"flag only", &fast, "", Fast, false},
		{"flag overrides env", &fast, "deep", Fast, false},
		{"invalid flag", stringPtr("turbo"), "", "", true},
		{"invalid env", nil, "xxl", "", true},
	}
	for _, c := range cases {
		got, err := ResolveProfile(c.flag, c.env)
		if (err != nil) != c.wantErr {
			t.Fatalf("%s: error = %v, wantErr %v", c.name, err, c.wantErr)
		}
		if err == nil && got != c.want {
			t.Fatalf("%s: profile = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestEnvName(t *testing.T) {
	if EnvName != "GRAPHI_INDEX_PROFILE" {
		t.Fatalf("EnvName = %q", EnvName)
	}
}

func TestAll(t *testing.T) {
	got := All()
	want := []Profile{Fast, Balanced, Deep}
	if len(got) != len(want) {
		t.Fatalf("All() = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("All()[%d] = %v, want %v", i, got[i], want[i])
		}
	}
}

func stringPtr(s string) *string { return &s }

// TestEnvIntegration verifies that os.Getenv works through the public EnvName.
func TestEnvIntegration(t *testing.T) {
	t.Setenv(EnvName, "deep")
	if os.Getenv(EnvName) != "deep" {
		t.Fatalf("env not set")
	}
}
