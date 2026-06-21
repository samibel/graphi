package workspace

import (
	"context"
	"testing"
)

func TestModulesFromData_SingleUse(t *testing.T) {
	mods, err := ModulesFromData([]byte("go 1.26\n\nuse .\n"))
	if err != nil {
		t.Fatal(err)
	}
	if len(mods) != 1 || mods[0] != "." {
		t.Errorf("mods=%v, want [.]", mods)
	}
}

func TestModulesFromData_Block(t *testing.T) {
	data := []byte("go 1.26\n\nuse (\n\t.\n\t./mod2\n)\n")
	mods, err := ModulesFromData(data)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{".", "./mod2"}
	if len(mods) != len(want) {
		t.Fatalf("mods=%v, want %v", mods, want)
	}
	for i, m := range mods {
		if m != want[i] {
			t.Errorf("mods[%d]=%q, want %q", i, m, want[i])
		}
	}
}

func TestModulesFromData_NoneErrors(t *testing.T) {
	if _, err := ModulesFromData([]byte("go 1.26\n")); err == nil {
		t.Error("expected error when no use directives")
	}
}

func TestModules_ReadsGoWork(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping live go.work read in -short mode")
	}
	mods, err := Modules(context.Background())
	if err != nil {
		t.Fatalf("Modules: %v", err)
	}
	if len(mods) == 0 {
		t.Error("expected at least one module from go.work")
	}
}
