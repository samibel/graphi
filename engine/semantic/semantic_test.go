package semantic

import (
	"reflect"
	"testing"
)

// TestDefaultOff pins the shipped default: without the opt-in the registry is
// exactly the go/types resolver — byte-identical product behavior, and the
// capability surface claims nothing the passes do not run.
func TestDefaultOff(t *testing.T) {
	t.Setenv(EnvJVM, "")
	if got, want := Languages(), []string{"go"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("default registry = %v, want %v", got, want)
	}
	t.Setenv(EnvJVM, "0")
	if got := Languages(); !reflect.DeepEqual(got, []string{"go"}) {
		t.Fatalf("explicit 0 must stay off: %v", got)
	}
}

// TestOptInRegistersJVM pins the experimental opt-in: the JVM registrants
// appear, and Languages() — the trust surface's source — reflects them.
func TestOptInRegistersJVM(t *testing.T) {
	t.Setenv(EnvJVM, "1")
	if got, want := Languages(), []string{"go", "java", "kotlin"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("opt-in registry = %v, want %v", got, want)
	}
	langs := map[string]bool{}
	for _, r := range NewRegistry().Resolvers() {
		langs[r.Language()] = true
	}
	if !langs["go"] || !langs["java"] || !langs["kotlin"] {
		t.Fatalf("resolvers = %v", langs)
	}
}
