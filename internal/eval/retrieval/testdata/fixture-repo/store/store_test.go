package store

import (
	"errors"
	"testing"
)

func TestStoreImplementations(t *testing.T) {
	impls := map[string]Store{"memory": NewMemoryStore()}
	fs, err := NewFileStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	impls["file"] = fs

	for name, s := range impls {
		t.Run(name, func(t *testing.T) {
			if _, err := s.Get("missing"); !errors.Is(err, ErrNotFound) {
				t.Errorf("Get missing: err = %v", err)
			}
			if err := s.Put("k", []byte("v")); err != nil {
				t.Fatal(err)
			}
			got, err := s.Get("k")
			if err != nil || string(got) != "v" {
				t.Errorf("Get k = %q, %v", got, err)
			}
			keys, err := s.Keys()
			if err != nil || len(keys) != 1 || keys[0] != "k" {
				t.Errorf("Keys = %v, %v", keys, err)
			}
		})
	}
}
