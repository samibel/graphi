package ledgeraudit

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
)

// canonicalEntry is the stable projection of a LedgerEntry used for hashing and
// restart-safe serialization. Field order is fixed (Go marshals struct fields in
// declaration order) so the byte encoding is canonical and deterministic.
type canonicalEntry struct {
	Model           string `json:"model"`
	SessionID       string `json:"session_id"`
	Seq             int64  `json:"seq"`
	InputTokens     int64  `json:"input_tokens"`
	OutputTokens    int64  `json:"output_tokens"`
	BaseInputTokens int64  `json:"base_input_tokens"`
	BaseOutTokens   int64  `json:"base_output_tokens"`
	SavingsMicroUSD int64  `json:"savings_micro_usd"`
	CapApplied      bool   `json:"cap_applied"`
}

func toCanonical(e LedgerEntry) canonicalEntry {
	return canonicalEntry{
		Model:           e.Op.Model,
		SessionID:       e.Op.SessionID,
		Seq:             e.Op.Seq,
		InputTokens:     int64(e.Op.InputTokens),
		OutputTokens:    int64(e.Op.OutputTokens),
		BaseInputTokens: int64(e.Op.BaselineInputTokens),
		BaseOutTokens:   int64(e.Op.BaselineOutputTokens),
		SavingsMicroUSD: int64(e.SavingsMicroUSD),
		CapApplied:      e.CapApplied,
	}
}

// canonicalEntryBytes returns the canonical byte encoding of an entry.
func canonicalEntryBytes(e LedgerEntry) ([]byte, error) {
	b, err := json.Marshal(toCanonical(e))
	if err != nil {
		return nil, err
	}
	return b, nil
}

// EntryHash returns the hex sha256 over the canonical encoding of an entry. The
// hash is content-addressed: identical entries (same identity + value) always
// hash identically; any drift changes the hash.
func EntryHash(e LedgerEntry) (string, error) {
	b, err := canonicalEntryBytes(e)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:]), nil
}

// sessionTotal is one sorted entry in the canonical session-totals map.
type sessionTotal struct {
	SessionID string `json:"session_id"`
	Total     int64  `json:"total_micro_usd"`
}

// canonicalState is the canonical, restart-surviving projection of the ledger.
type canonicalState struct {
	Entries  []canonicalEntry `json:"entries"`
	Total    int64            `json:"total_micro_usd"`
	Sessions []sessionTotal   `json:"sessions"`
	Hashes   []string         `json:"hashes"`
}

// LedgerState is the canonicalized projection verified across restarts.
type LedgerState struct {
	Entries  []LedgerEntry
	Total    MicroUSD
	Sessions map[string]MicroUSD
	Hashes   []string
}

// NewLedgerState builds a canonical state from a ledger, with monotonic entry
// ordering (as recorded) and content-addressed entry hashes.
func NewLedgerState(ledger Ledger) (LedgerState, error) {
	entries := ledger.Entries()
	hashes := make([]string, 0, len(entries))
	for _, e := range entries {
		h, err := EntryHash(e)
		if err != nil {
			return LedgerState{}, err
		}
		hashes = append(hashes, h)
	}
	sessions := map[string]MicroUSD{}
	for _, id := range ledger.SessionIDs() {
		sessions[id] = ledger.SessionTotal(id)
	}
	return LedgerState{
		Entries:  entries,
		Total:    ledger.Total(),
		Sessions: sessions,
		Hashes:   hashes,
	}, nil
}

// Serialize canonicalizes the state to deterministic bytes for persistence.
func Serialize(state LedgerState) ([]byte, error) {
	cs := canonicalState{Total: int64(state.Total)}
	cs.Entries = make([]canonicalEntry, 0, len(state.Entries))
	for _, e := range state.Entries {
		cs.Entries = append(cs.Entries, toCanonical(e))
	}
	ids := make([]string, 0, len(state.Sessions))
	for id := range state.Sessions {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	cs.Sessions = make([]sessionTotal, 0, len(ids))
	for _, id := range ids {
		cs.Sessions = append(cs.Sessions, sessionTotal{SessionID: id, Total: int64(state.Sessions[id])})
	}
	cs.Hashes = append(cs.Hashes, state.Hashes...)
	return json.Marshal(cs)
}

// Deserialize is the inverse of Serialize (used after a simulated restart).
func Deserialize(data []byte) (LedgerState, error) {
	var cs canonicalState
	if err := json.Unmarshal(data, &cs); err != nil {
		return LedgerState{}, err
	}
	entries := make([]LedgerEntry, 0, len(cs.Entries))
	for _, ce := range cs.Entries {
		entries = append(entries, LedgerEntry{
			Op: Op{
				Model: ce.Model, SessionID: ce.SessionID, Seq: ce.Seq,
				InputTokens: TokenCount(ce.InputTokens), OutputTokens: TokenCount(ce.OutputTokens),
				BaselineInputTokens:  TokenCount(ce.BaseInputTokens),
				BaselineOutputTokens: TokenCount(ce.BaseOutTokens),
			},
			SavingsMicroUSD: MicroUSD(ce.SavingsMicroUSD),
			CapApplied:      ce.CapApplied,
		})
	}
	sessions := make(map[string]MicroUSD, len(cs.Sessions))
	for _, st := range cs.Sessions {
		sessions[st.SessionID] = MicroUSD(st.Total)
	}
	return LedgerState{
		Entries: entries, Total: MicroUSD(cs.Total),
		Sessions: sessions, Hashes: append([]string(nil), cs.Hashes...),
	}, nil
}

// VerifyRestart asserts that the state read after a process restart is identical
// to the pre-restart state in totals, ordering, session totals, and entry hashes
// (no drift, double-counting, reorder, or replay). It compares canonical bytes
// plus explicit hash-chain equality.
func VerifyRestart(before, after LedgerState) error {
	bb, err := Serialize(before)
	if err != nil {
		return err
	}
	ab, err := Serialize(after)
	if err != nil {
		return err
	}
	if !bytes.Equal(bb, ab) {
		return fmt.Errorf("restart integrity: canonical state differs (totals/ordering/sessions drift)")
	}
	if len(before.Hashes) != len(after.Hashes) {
		return fmt.Errorf("restart integrity: hash-chain length differs (%d vs %d) — possible replay/truncation", len(before.Hashes), len(after.Hashes))
	}
	for i := range before.Hashes {
		if before.Hashes[i] != after.Hashes[i] {
			return fmt.Errorf("restart integrity: entry hash %d differs — content drift", i)
		}
	}
	return nil
}
