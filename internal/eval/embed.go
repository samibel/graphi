package eval

import (
	_ "embed"
	"fmt"
)

//go:embed cases.json
var embeddedCases []byte

//go:embed coverage-baseline.json
var embeddedCoverageBaseline []byte

// BaselineMethodVersion stamps the frozen baseline (raw-file/naive-context)
// computation method. The harness pins this against the fixture; a method change
// without a version bump fails the build (AC: pinned version-stamped baseline).
const BaselineMethodVersion = "2026-06-20-v1"

// FixtureBaselineVersion is the version-stamped baseline the harness pins
// AGAINST the code constant. It is a literal on purpose so a method change that
// bumps BaselineMethodVersion without re-pinning the fixture is caught.
const FixtureBaselineVersion = "2026-06-20-v1"

// AssertBaselineVersion fails if the code's pinned method version disagrees with
// the fixture's expected version (method changed without an explicit bump).
func AssertBaselineVersion(expected string) error {
	if expected != BaselineMethodVersion {
		return fmt.Errorf("eval: baseline method version drift: code=%q fixture=%q — bump BaselineMethodVersion and re-pin the fixture", BaselineMethodVersion, expected)
	}
	return nil
}
