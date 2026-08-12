package classify

import "testing"

func TestIsTestPath(t *testing.T) {
	cases := []struct {
		path string
		want bool
	}{
		{"", false},
		{"engine/query/service.go", false},
		{"engine/query/service_test.go", true},
		{"web/src/app.test.ts", true},
		{"web/src/app.spec.ts", true},
		{"spec/models/user_spec.rb", true},
		{"tests/test_auth.py", true},
		{"pkg/tests/helper.go", true},
		{"test/fixtures/a.go", true},            // leading segment, safe_delete's HasPrefix case
		{"pkg/testdata/sample.go", true},        // union addition over triage's set
		{"src/__tests__/app.jsx", true},         // JS convention
		{"internal\\parity\\run_test.go", true}, // separator-normalized
		{"contest/entry.go", false},             // "test" inside a word, no pattern hit
		{"protester/march.go", false},
	}
	for _, c := range cases {
		if got := IsTestPath(c.path); got != c.want {
			t.Errorf("IsTestPath(%q) = %v, want %v", c.path, got, c.want)
		}
	}
}

func TestIsGeneratedPath(t *testing.T) {
	cases := []struct {
		path string
		want bool
	}{
		{"api/types.gen.go", true},
		{"api/service_pb.go", true},
		{"client/api.generated.ts", true},
		{"vendor/golang.org/x/sys/unix/zerrors.go", true},
		{"web/node_modules/react/index.js", true},
		{"generated/schema.go", true},
		{"engine/query/service.go", false},
		{"", false},
	}
	for _, c := range cases {
		if got := IsGeneratedPath(c.path); got != c.want {
			t.Errorf("IsGeneratedPath(%q) = %v, want %v", c.path, got, c.want)
		}
	}
}

func TestIsConfigPath(t *testing.T) {
	cases := []struct {
		path string
		want bool
	}{
		{"go.mod", true},
		{"go.sum", true},
		{"Dockerfile", true},
		{"Makefile", true},
		{"deploy/config.yaml", true},
		{"settings.yml", true},
		{"package.json", true},
		{"Cargo.toml", true},
		{"app.ini", true},
		{"package-lock.lock", true},
		{".github/workflows/ci.yml", true},
		{"engine/query/service.go", false},
		{"readme.md", false},
		{"", false},
	}
	for _, c := range cases {
		if got := IsConfigPath(c.path); got != c.want {
			t.Errorf("IsConfigPath(%q) = %v, want %v", c.path, got, c.want)
		}
	}
}

func TestLanguage(t *testing.T) {
	cases := []struct {
		path string
		want string
	}{
		{"engine/query/service.go", "Go"},
		{"web/src/App.tsx", "TypeScript"},
		{"scripts/build.sh", "Bash"},
		{"docs/readme.md", "Markdown"},
		{"unknown.zig", ""},
		{"", ""},
	}
	for _, c := range cases {
		if got := Language(c.path); got != c.want {
			t.Errorf("Language(%q) = %q, want %q", c.path, got, c.want)
		}
	}
}

func TestMatchAnyPattern(t *testing.T) {
	if !MatchAnyPattern("api/types.gen.go", []string{"*.gen.go"}) {
		t.Error("basename glob should match")
	}
	if !MatchAnyPattern("vendor/x/y.go", []string{"vendor/"}) {
		t.Error("directory-substring pattern should match")
	}
	if MatchAnyPattern("api/types.go", []string{"*.gen.go", ""}) {
		t.Error("empty pattern must not match everything")
	}
}
