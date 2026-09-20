//go:build !aix && !darwin && !dragonfly && !freebsd && !linux && !netbsd && !openbsd && !solaris && !windows

package retrieval

import "fmt"

func openQualificationDirectoryDurability(string) (qualificationDirectoryDurability, error) {
	return nil, fmt.Errorf("qualification report publication requires durable directory sync on this platform")
}
