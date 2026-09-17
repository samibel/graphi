//go:build !linux && !darwin

package retrieval

import "fmt"

func probeQualificationReferenceMachine() (ReferenceMachine, error) {
	return ReferenceMachine{}, fmt.Errorf("embedded-model qualification measure: reference-machine probe unsupported on this platform")
}
