//go:build windows

package retrieval

import (
	"errors"
	"fmt"

	"golang.org/x/sys/windows"
)

type qualificationWindowsDirectoryDurability struct {
	handle        windows.Handle
	identityValue string
}

func openQualificationDirectoryDurability(path string) (qualificationDirectoryDurability, error) {
	pathUTF16, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return nil, err
	}
	handle, err := windows.CreateFile(
		pathUTF16,
		windows.GENERIC_WRITE|windows.FILE_READ_ATTRIBUTES,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		nil,
		windows.OPEN_EXISTING,
		windows.FILE_FLAG_BACKUP_SEMANTICS,
		0,
	)
	if err != nil {
		return nil, err
	}
	var info windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(handle, &info); err != nil {
		return nil, errors.Join(err, windows.CloseHandle(handle))
	}
	identity := qualificationWindowsIdentity(info.VolumeSerialNumber, info.FileIndexHigh, info.FileIndexLow)
	return &qualificationWindowsDirectoryDurability{handle: handle, identityValue: identity}, nil
}

func (handle *qualificationWindowsDirectoryDurability) identity() string { return handle.identityValue }

func (handle *qualificationWindowsDirectoryDurability) sync() error {
	if err := windows.FlushFileBuffers(handle.handle); err != nil {
		return fmt.Errorf("FlushFileBuffers directory handle: %w", err)
	}
	return nil
}

func (handle *qualificationWindowsDirectoryDurability) close() error {
	return windows.CloseHandle(handle.handle)
}
