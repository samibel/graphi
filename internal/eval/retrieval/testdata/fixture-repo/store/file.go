package store

import (
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// FileStore keeps one file per key under Root. Keys are restricted to a safe
// character set so a key can never escape the root directory.
type FileStore struct {
	Root string
}

// NewFileStore creates root if needed and returns a FileStore over it.
func NewFileStore(root string) (*FileStore, error) {
	if err := os.MkdirAll(root, 0o755); err != nil {
		return nil, err
	}
	return &FileStore{Root: root}, nil
}

// Get implements Store.
func (f *FileStore) Get(key string) ([]byte, error) {
	p, err := f.path(key)
	if err != nil {
		return nil, err
	}
	b, err := os.ReadFile(p)
	if errors.Is(err, os.ErrNotExist) {
		return nil, ErrNotFound
	}
	return b, err
}

// Put implements Store.
func (f *FileStore) Put(key string, value []byte) error {
	p, err := f.path(key)
	if err != nil {
		return err
	}
	return os.WriteFile(p, value, 0o644)
}

// Keys implements Store.
func (f *FileStore) Keys() ([]string, error) {
	entries, err := os.ReadDir(f.Root)
	if err != nil {
		return nil, err
	}
	keys := make([]string, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() {
			keys = append(keys, e.Name())
		}
	}
	sort.Strings(keys)
	return keys, nil
}

// path maps a key to its file, rejecting anything that is not a plain name.
func (f *FileStore) path(key string) (string, error) {
	if key == "" || strings.ContainsAny(key, `/\`) || strings.HasPrefix(key, ".") {
		return "", errors.New("store: invalid key " + key)
	}
	return filepath.Join(f.Root, key), nil
}
