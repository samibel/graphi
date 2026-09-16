//go:build windows

package retrieval

import (
	"fmt"
	"os"
	"syscall"
)

func qualificationFileIdentity(file *os.File) (string, error) {
	var info syscall.ByHandleFileInformation
	if err := syscall.GetFileInformationByHandle(syscall.Handle(file.Fd()), &info); err != nil {
		return "", err
	}
	return qualificationWindowsIdentity(info.VolumeSerialNumber, info.FileIndexHigh, info.FileIndexLow), nil
}

func qualificationWindowsIdentity(volume, indexHigh, indexLow uint32) string {
	identity := fmt.Sprintf("volume=%d;index-high=%d;index-low=%d", volume, indexHigh, indexLow)
	return SHA256Hex([]byte(identity))
}
