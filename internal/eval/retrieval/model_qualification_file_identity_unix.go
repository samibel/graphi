//go:build aix || darwin || dragonfly || freebsd || linux || netbsd || openbsd || solaris

package retrieval

import (
	"fmt"
	"os"
	"syscall"
)

func qualificationFileIdentity(file *os.File) (string, error) {
	var stat syscall.Stat_t
	if err := syscall.Fstat(int(file.Fd()), &stat); err != nil {
		return "", err
	}
	return SHA256Hex([]byte(fmt.Sprintf("dev=%d;ino=%d", uint64(stat.Dev), uint64(stat.Ino)))), nil
}
