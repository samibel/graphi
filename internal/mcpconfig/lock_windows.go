//go:build windows

package mcpconfig

import (
	"fmt"
	"os"

	"golang.org/x/sys/windows"
)

func lockConfig(path string) (func(), error) {
	lockPath := path + ".graphi.lock"
	file, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("mcpconfig: open lock %s: %w", lockPath, err)
	}
	var overlapped windows.Overlapped
	if err := windows.LockFileEx(windows.Handle(file.Fd()), windows.LOCKFILE_EXCLUSIVE_LOCK, 0, 1, 0, &overlapped); err != nil {
		_ = file.Close()
		return nil, fmt.Errorf("mcpconfig: lock %s: %w", lockPath, err)
	}
	return func() {
		_ = windows.UnlockFileEx(windows.Handle(file.Fd()), 0, 1, 0, &overlapped)
		_ = file.Close()
	}, nil
}
