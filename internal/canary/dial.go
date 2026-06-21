package canary

import (
	"net"
	"strings"
)

// DialAttempt captures a single outbound dial attempt observed during a canary
// run. The assertion is on ATTEMPT (the destination the code tried to reach),
// not on packets that made it onto the wire — so even a dial that would be
// blocked by netns is caught (SW-008 refinement finding D2/S1).
type DialAttempt struct {
	// Tool identifies the surface tool/command that triggered the dial.
	Tool string `json:"tool"`
	// Network is the dial network ("tcp", "udp", …).
	Network string `json:"network"`
	// Address is the dial destination (host:port or host).
	Address string `json:"address"`
}

// IsLoopback reports whether the dial destination is loopback
// (127.0.0.0/8 or ::1). Only loopback is permitted by the canary; in-process
// local servers and the daemon's Unix socket / local HTTP+SSE are the
// legitimate loopback users (SW-008 refinement finding S3).
func (d DialAttempt) IsLoopback() bool {
	host := d.Address
	// Unix sockets are local IPC, not network egress.
	if d.Network == "unix" || d.Network == "unixpacket" {
		return true
	}
	if h, _, err := net.SplitHostPort(d.Address); err == nil {
		host = h
	}
	host = strings.TrimSpace(host)
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	if ip == nil {
		// A non-IP host that isn't localhost is treated as non-loopback: it
		// would require DNS (itself non-loopback) and is therefore egress.
		return false
	}
	return ip.IsLoopback()
}

// DialRecorder collects dial attempts observed during a canary run. The default
// recorder is passive (records what a driver reports). Tests and the in-process
// driver hook real dial attempts into it via Record.
type DialRecorder struct {
	attempts []DialAttempt
}

// NewDialRecorder returns an empty recorder.
func NewDialRecorder() *DialRecorder { return &DialRecorder{} }

// Record appends an observed dial attempt.
func (r *DialRecorder) Record(a DialAttempt) { r.attempts = append(r.attempts, a) }

// All returns a copy of all recorded attempts.
func (r *DialRecorder) All() []DialAttempt {
	out := make([]DialAttempt, len(r.attempts))
	copy(out, r.attempts)
	return out
}

// NonLoopback returns only the attempts that are NOT loopback — the violations
// the canary must fail on.
func (r *DialRecorder) NonLoopback() []DialAttempt {
	out := make([]DialAttempt, 0, len(r.attempts))
	for _, a := range r.attempts {
		if !a.IsLoopback() {
			out = append(out, a)
		}
	}
	return out
}
