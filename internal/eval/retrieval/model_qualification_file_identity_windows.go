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
	identity := fmt.Sprintf("volume=%d;index-high=%d;index-low=%d", info.VolumeSerialNumber, info.FileIndexHigh, info.FileIndexLow)
	return SHA256Hex([]byte(identity)), nil
}
