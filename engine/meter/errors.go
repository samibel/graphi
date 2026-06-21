package meter

import "errors"

// ErrNoReader is returned by Record when the Meter has no FileReader to compute
// the baseline from. It is a typed sentinel so callers can match with errors.Is.
var ErrNoReader = errors.New("meter: no file reader configured")
