//go:build !windows

package mcpconfig

import (
	"fmt"
	"os"

	"golang.org/x/sys/unix"
)

func lockConfig(path string) (func(), error) {
	lockPath := path + ".graphi.lock"
	file, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("mcpconfig: open lock %s: %w", lockPath, err)
	}
	if err := file.Chmod(0o600); err != nil {
		_ = file.Close()
		return nil, fmt.Errorf("mcpconfig: protect lock %s: %w", lockPath, err)
	}
	if err := unix.Flock(int(file.Fd()), unix.LOCK_EX); err != nil {
		_ = file.Close()
		return nil, fmt.Errorf("mcpconfig: lock %s: %w", lockPath, err)
	}
	return func() {
		_ = unix.Flock(int(file.Fd()), unix.LOCK_UN)
		_ = file.Close()
	}, nil
}
