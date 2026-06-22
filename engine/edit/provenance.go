package edit

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"sync/atomic"
	"time"

	"github.com/samibel/graphi/engine/ingest"
)

// opTypeForRefactorKind maps the engine/edit closed RefactorKind enum onto the
// ingest closed EditOpType enum (the two mirror each other deliberately). An
// unknown kind is rejected so the audit op_type can never be poisoned.
func opTypeForRefactorKind(k RefactorKind) (ingest.EditOpType, error) {
	switch k {
	case RefactorRename:
		return ingest.EditOpRename, nil
	case RefactorExtract:
		return ingest.EditOpExtract, nil
	case RefactorMove:
		return ingest.EditOpMove, nil
	case RefactorSignatureChange:
		return ingest.EditOpSignatureChange, nil
	default:
		return "", fmt.Errorf("%w: unknown refactor kind %q", ErrInvalidOp, k)
	}
}

// editSeq disambiguates two edits minted within the same wall-clock nanosecond
// so every edit id is unique within a process even under a coarse clock.
var editSeq uint64

// nowFn is the clock seam; tests inject a deterministic clock. It returns the
// instant captured ONCE per edit (a single value shared across the edit's whole
// touched set).
func (a *Applier) now() time.Time {
	if a.clock != nil {
		return a.clock()
	}
	return time.Now().UTC()
}

// newEditProvenance mints the edit id ONCE and captures the timestamp ONCE for a
// single edit, returning the bundle threaded into the provenance-aware ingest
// pass. The id combines the captured timestamp, a process-local monotonic
// sequence, and random bytes so it is unique without relying on the clock's
// resolution.
func (a *Applier) newEditProvenance(op ingest.EditOpType) (ingest.EditProvenance, error) {
	ts := a.now()
	var rnd [8]byte
	if _, err := rand.Read(rnd[:]); err != nil {
		return ingest.EditProvenance{}, fmt.Errorf("%w: mint edit id: %v", ErrWrite, err)
	}
	seq := atomic.AddUint64(&editSeq, 1)
	id := fmt.Sprintf("ed_%d_%06x_%s", ts.UnixNano(), seq&0xffffff, hex.EncodeToString(rnd[:]))
	prov := ingest.EditProvenance{EditID: id, OpType: op, Timestamp: ts.UnixNano()}
	if err := prov.Validate(); err != nil {
		return ingest.EditProvenance{}, err
	}
	return prov, nil
}
