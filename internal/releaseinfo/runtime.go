package releaseinfo

import "runtime"

func goos() string   { return runtime.GOOS }
func goarch() string { return runtime.GOARCH }
