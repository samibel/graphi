// Package guard is graphi's single zero-egress enforcement chokepoint (SW-099).
// Every transport (CLI, MCP stdio, MCP streamable-HTTP, SSE, daemon control RPC)
// constructs its listeners and dialers through this package so the two
// non-negotiable runtime invariants are enforced once and inherited, not
// re-implemented and re-audited per surface:
//
//   - loopback-only listeners: any non-loopback or wildcard bind is refused with
//     a typed ErrNonLoopbackBind BEFORE the listener is opened; and
//   - zero-egress: a default-deny dialer rejects every outbound dial to a
//     non-loopback address with a typed ErrEgressDenied, so graphi never opens an
//     off-host connection.
//
// Layering: guard is a surface-layer package. It depends only on the standard
// library (net, syscall) — no CGo, no engine/core imports.
package guard

import (
	"context"
	"errors"
	"fmt"
	"net"
	"syscall"
)

// ErrNonLoopbackBind is returned when a listener is asked to bind a non-loopback
// or wildcard address. It is typed so callers and tests can match it.
var ErrNonLoopbackBind = errors.New("guard: refusing non-loopback bind")

// ErrEgressDenied is returned by the default-deny dialer when graphi attempts an
// outbound dial to a non-loopback address (zero-egress).
var ErrEgressDenied = errors.New("guard: outbound egress denied")

// AssertLoopback reports whether addr is a loopback-only bind/host. It accepts a
// host ("127.0.0.1", "::1", "localhost"), a host:port, or a loopback IP in
// 127.0.0.0/8; it refuses wildcard ("0.0.0.0", "::", empty host) and any routable
// address with ErrNonLoopbackBind.
func AssertLoopback(addr string) error {
	host := addr
	if h, _, err := net.SplitHostPort(addr); err == nil {
		host = h
	}
	if isLoopbackHost(host) {
		return nil
	}
	return fmt.Errorf("%w: %q", ErrNonLoopbackBind, addr)
}

// ListenLoopback is the single guarded TCP listener factory: it refuses a
// non-loopback bind (ErrNonLoopbackBind) before opening any socket, then listens.
func ListenLoopback(network, addr string) (net.Listener, error) {
	if err := AssertLoopback(addr); err != nil {
		return nil, err
	}
	return net.Listen(network, addr)
}

// NoEgressDialer returns a *net.Dialer whose Control hook denies every outbound
// dial to a non-loopback address (ErrEgressDenied) before the connection is
// attempted. Loopback TCP and unix-domain sockets are permitted (graphi's own
// local-first transports); everything else is default-denied. Pure-Go (no CGo).
func NoEgressDialer() *net.Dialer {
	return &net.Dialer{Control: egressControl}
}

// egressControl is the dial-time gate: it permits unix sockets and loopback
// hosts, and denies all other (off-host) destinations.
func egressControl(network, address string, _ syscall.RawConn) error {
	if network == "unix" || network == "unixgram" || network == "unixpacket" {
		return nil
	}
	host := address
	if h, _, err := net.SplitHostPort(address); err == nil {
		host = h
	}
	if isLoopbackHost(host) {
		return nil
	}
	return fmt.Errorf("%w: %s", ErrEgressDenied, address)
}

// DialContext dials through the default-deny dialer, enforcing zero-egress.
func DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	return NoEgressDialer().DialContext(ctx, network, address)
}

// isLoopbackHost reports whether host is loopback. Empty host (a wildcard bind)
// is NOT loopback. "localhost" is treated as loopback by convention.
func isLoopbackHost(host string) bool {
	if host == "" {
		return false
	}
	if host == "localhost" {
		return true
	}
	if ip := net.ParseIP(host); ip != nil {
		return ip.IsLoopback()
	}
	return false
}
