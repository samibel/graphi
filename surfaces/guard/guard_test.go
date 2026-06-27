package guard

import (
	"context"
	"errors"
	"testing"
)

func TestAssertLoopback_Table(t *testing.T) {
	loopback := []string{"127.0.0.1:0", "127.0.0.1", "::1", "[::1]:8080", "localhost", "localhost:9000", "127.0.0.5:1"}
	for _, a := range loopback {
		if err := AssertLoopback(a); err != nil {
			t.Errorf("AssertLoopback(%q) = %v, want nil (loopback)", a, err)
		}
	}
	nonLoopback := []string{"0.0.0.0:0", "0.0.0.0", "::", "192.168.1.5:9000", "10.0.0.1:80", "8.8.8.8:53", "example.com:443"}
	for _, a := range nonLoopback {
		if err := AssertLoopback(a); !errors.Is(err, ErrNonLoopbackBind) {
			t.Errorf("AssertLoopback(%q) = %v, want ErrNonLoopbackBind", a, err)
		}
	}
}

func TestListenLoopback_RefusesNonLoopback(t *testing.T) {
	for _, a := range []string{"0.0.0.0:0", "192.168.1.5:0", "::"} {
		if _, err := ListenLoopback("tcp", a); !errors.Is(err, ErrNonLoopbackBind) {
			t.Errorf("ListenLoopback(%q) = %v, want ErrNonLoopbackBind", a, err)
		}
	}
	ln, err := ListenLoopback("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("loopback listen should succeed: %v", err)
	}
	_ = ln.Close()
}

func TestNoEgressDialer_DeniesExternal(t *testing.T) {
	ctx := context.Background()
	// External destination is default-denied before any connection is attempted.
	if _, err := DialContext(ctx, "tcp", "8.8.8.8:53"); !errors.Is(err, ErrEgressDenied) {
		t.Fatalf("external dial err = %v, want ErrEgressDenied", err)
	}
	if _, err := DialContext(ctx, "tcp", "93.184.216.34:80"); !errors.Is(err, ErrEgressDenied) {
		t.Fatalf("external dial err = %v, want ErrEgressDenied", err)
	}
}

func TestNoEgressDialer_AllowsLoopback(t *testing.T) {
	ctx := context.Background()
	ln, err := ListenLoopback("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()
	go func() {
		c, e := ln.Accept()
		if e == nil {
			_ = c.Close()
		}
	}()
	conn, err := DialContext(ctx, "tcp", ln.Addr().String())
	if err != nil {
		t.Fatalf("loopback dial denied (should be allowed): %v", err)
	}
	if errors.Is(err, ErrEgressDenied) {
		t.Fatal("loopback dial must not be egress-denied")
	}
	_ = conn.Close()
}

// TestConformance_SingleChokepoint documents that the guard is the one place the
// loopback/egress policy lives: AssertLoopback and the egress dialer agree on the
// loopback/non-loopback classification for the same address set, so routing every
// transport through guard yields a single, consistent policy.
func TestConformance_SingleChokepoint(t *testing.T) {
	ctx := context.Background()
	for _, a := range []string{"8.8.8.8:53", "192.168.1.5:1234", "example.com:443"} {
		bindErr := AssertLoopback(a)
		_, dialErr := DialContext(ctx, "tcp", a)
		if !errors.Is(bindErr, ErrNonLoopbackBind) || !errors.Is(dialErr, ErrEgressDenied) {
			t.Fatalf("inconsistent policy for %q: bind=%v dial=%v", a, bindErr, dialErr)
		}
	}
}
