//go:build aix || darwin || dragonfly || freebsd || linux || netbsd || openbsd || solaris

package retrieval

import (
	"errors"
	"fmt"
	"os"
)

type qualificationUnixDirectoryDurability struct {
	file          *os.File
	identityValue string
}

func openQualificationDirectoryDurability(path string) (qualificationDirectoryDurability, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	info, statErr := file.Stat()
	identity, identityErr := qualificationFileIdentity(file)
	if statErr != nil || identityErr != nil || !info.IsDir() {
		return nil, errors.Join(fmt.Errorf("path is not a durable directory"), statErr, identityErr, file.Close())
	}
	return &qualificationUnixDirectoryDurability{file: file, identityValue: identity}, nil
}

func (handle *qualificationUnixDirectoryDurability) identity() string { return handle.identityValue }

func (handle *qualificationUnixDirectoryDurability) sync() error { return handle.file.Sync() }

func (handle *qualificationUnixDirectoryDurability) close() error { return handle.file.Close() }
