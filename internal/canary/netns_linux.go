//go:build linux

// Package canary — Linux loopback-only network-namespace isolator.
//
// Run creates a fresh network namespace, brings up loopback (so in-process
// local servers / IPC on 127.0.0.0/8 and ::1 keep working), and leaves every
// other interface DOWN — so non-loopback dials cannot reach the wire. The
// calling thread must have CAP_SYS_ADMIN (root or appropriate caps); if not,
// IsAvailable() reports false and the canary hard-fails rather than silently
// passing.
//
// This is pure-Go + golang.org/x/sys/unix (already an indirect dependency via
// modernc/sqlite) — no CGo, no libpcap, fully consistent with the CGo-free
// default build.
package canary

import (
	"os"
	"runtime"
	"unsafe"

	"golang.org/x/sys/unix"
)

type linuxNetns struct{}

func newPlatformIsolator() Isolator { return linuxNetns{} }

// IsAvailable reports whether unshare(CLONE_NEWNET) succeeds, i.e. whether this
// runner can actually create an isolated network namespace. On an unprivileged
// runner this returns false and the canary hard-fails.
func (linuxNetns) IsAvailable() bool {
	// Probe on a locked thread: capture the original namespace, unshare into a
	// fresh one, then restore. If any step fails we cannot isolate, and the
	// canary must hard-fail (never silently pass). The original-namespace FD
	// MUST be captured before the unshare — afterwards /proc/self/ns/net names
	// the new namespace and can no longer reach the original.
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	origFD, err := openNetnsPath("/proc/self/ns/net")
	if err != nil {
		return false
	}
	defer func() { _ = origFD.Close() }()

	if err := unix.Unshare(unix.CLONE_NEWNET); err != nil {
		return false
	}
	// Restore the caller's namespace on this thread.
	return unix.Setns(int(origFD.Fd()), unix.CLONE_NEWNET) == nil
}

// Run executes fn inside a fresh loopback-only network namespace, then restores
// the original namespace on return (best-effort).
func (n linuxNetns) Run(fn func() error) error {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	origFD, err := openNetnsPath("/proc/self/ns/net")
	if err != nil {
		return &IsolationError{Reason: "open original netns: " + err.Error()}
	}
	defer func() { _ = origFD.Close() }()

	if err := unix.Unshare(unix.CLONE_NEWNET); err != nil {
		return &IsolationError{Reason: "unshare(CLONE_NEWNET): " + err.Error()}
	}
	// Always restore the original namespace before returning.
	defer func() { _ = unix.Setns(int(origFD.Fd()), unix.CLONE_NEWNET) }()

	lo, err := unix.Socket(unix.AF_INET, unix.SOCK_DGRAM, 0)
	if err != nil {
		return &IsolationError{Reason: "socket for loopback up: " + err.Error()}
	}
	defer unix.Close(lo)
	ifreq := struct {
		Name  [16]byte
		Flags [16]byte // ifru_flags lives here for SIOCGIFFLAGS/SIOCSIFFLAGS
	}{}
	copy(ifreq.Name[:], "lo")
	const IFF_UP = 0x1
	// SIOCSIFFLAGS expects ifru_flags in the union; on amd64/arm64 the flags
	// field is the first 16-bit slot after the name in the ioctl union layout
	// we built above. We set IFF_UP to bring loopback up.
	setFlags := ifreq
	*(*uint16)(unsafe.Pointer(&setFlags.Flags[0])) = IFF_UP
	const SIOCSIFFLAGS = 0x8914
	if _, _, errno := unix.Syscall(unix.SYS_IOCTL, uintptr(lo), SIOCSIFFLAGS, uintptr(unsafe.Pointer(&setFlags))); errno != 0 {
		return &IsolationError{Reason: "bring loopback up: " + errno.Error()}
	}

	return fn()
}

// openNetnsPath opens a network-namespace file (e.g. /proc/self/ns/net).
func openNetnsPath(path string) (*os.File, error) {
	return os.Open(path)
}
