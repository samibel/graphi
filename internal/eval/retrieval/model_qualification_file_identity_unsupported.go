//go:build !aix && !darwin && !dragonfly && !freebsd && !linux && !netbsd && !openbsd && !solaris && !windows

package retrieval

import (
	"fmt"
	"os"
)

func qualificationFileIdentity(*os.File) (string, error) {
	return "", fmt.Errorf("qualification report publication requires stable filesystem identities on this platform")
}
