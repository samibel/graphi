// Package store is the fixture's key/value persistence boundary: one
// interface, two implementations.
package store

import "errors"

// ErrNotFound is returned by Get when the key has no value.
var ErrNotFound = errors.New("store: not found")

// Store is the persistence contract the HTTP handler depends on. Both
// MemoryStore and FileStore implement it.
type Store interface {
	// Get returns the value stored under key or ErrNotFound.
	Get(key string) ([]byte, error)
	// Put stores value under key, replacing any previous value.
	Put(key string, value []byte) error
	// Keys lists every stored key in sorted order.
	Keys() ([]string, error)
}

// Open selects an implementation by kind ("memory" or "file").
func Open(kind, root string) (Store, error) {
	switch kind {
	case "memory":
		return NewMemoryStore(), nil
	case "file":
		return NewFileStore(root)
	}
	return nil, errors.New("store: unknown kind " + kind)
}
