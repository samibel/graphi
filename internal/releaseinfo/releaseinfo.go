// Package releaseinfo provides the shared, pure, offline release-metadata
// accessor used by `graphi version` and by `graphi doctor` checks.
//
// It is intentionally separate from internal/release (which concerns the
// release build process) so the runtime metadata is cheap to import and test.
package releaseinfo

import (
	"fmt"
	"runtime/debug"

	"github.com/samibel/graphi/internal/version"
)

// KnownLatestVersion is the latest release version known at build time.
// It is embedded metadata, never fetched at runtime unless explicitly opted in.
const KnownLatestVersion = "0.0.0"

// Info is the single source of truth for runtime release metadata.
type Info struct {
	version   string
	commit    string
	date      string
	arch      string
	isRelease bool
}

// New returns release metadata for the running binary.
// It performs no I/O and no network calls.
func New() Info {
	commit, date := "", ""
	if info, ok := debug.ReadBuildInfo(); ok {
		for _, s := range info.Settings {
			switch s.Key {
			case "vcs.revision":
				commit = s.Value
			case "vcs.time":
				date = s.Value
			}
		}
	}
	return Info{
		version:   version.Version,
		commit:    commit,
		date:      date,
		arch:      fmt.Sprintf("%s/%s", runtimeGOOS(), runtimeGOARCH()),
		isRelease: version.Version != "dev" && version.Version != "",
	}
}

// Version returns the stamped release version (e.g. "1.2.3"), or "dev".
func (i Info) Version() string { return i.version }
func (i Info) Commit() string  { return i.commit }
func (i Info) Date() string    { return i.date }
func (i Info) Arch() string    { return i.arch }
func (i Info) IsRelease() bool { return i.version != "dev" && i.version != "" }
func (i Info) ReleaseMarker() string {
	if i.IsRelease() {
		return "packaged release"
	}
	return "dev / not a packaged release"
}

// VersionString returns the formatted version line used by `graphi version`.
func (i Info) VersionString() string {
	return fmt.Sprintf("graphi version=%s commit=%s date=%s arch=%s release_marker=%s",
		i.version, i.commit, i.date, i.arch, i.ReleaseMarker())
}

func runtimeGOOS() string {
	// Abstracted for tests that override via build tags; default delegates to runtime.
	return goos()
}

func runtimeGOARCH() string {
	return goarch()
}
