package model2vec

import (
	"context"
	"net"
	"os/exec"
	"strings"
	"sync/atomic"
	"testing"
)

const spikePackage = "github.com/samibel/graphi/internal/spike/model2vec"

// forbiddenDeps are packages whose presence in the spike's import closure would
// mean it could dial or exec. encoding/json is the non-vacuity control.
var forbiddenDeps = []string{"net", "net/http", "net/url", "os/exec", "crypto/tls", "syscall/js"}

// AC-6 (static half): the package's transitive, non-test import closure carries
// no networking or process-execution package.
func TestSpike_ImportClosureHasNoNetwork(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skipf("go toolchain unavailable: %v", err)
	}
	out, err := exec.Command("go", "list", "-deps", spikePackage).Output()
	if err != nil {
		t.Fatalf("go list -deps: %v", err)
	}
	deps := map[string]bool{}
	for _, d := range strings.Fields(string(out)) {
		deps[d] = true
	}
	if !deps["encoding/json"] {
		t.Fatalf("go list returned %d packages and none was encoding/json — the scan is broken", len(deps))
	}
	for _, f := range forbiddenDeps {
		if deps[f] {
			t.Errorf("%s imports %q (transitively); the spike must be zero-egress", spikePackage, f)
		}
	}
}

// failingDialer records any dial ATTEMPT and always fails — the pattern of
// core/parse/egress_test.go: installed as the process resolver so a DNS or
// socket dial flips a flag instead of opening a live socket.
type failingDialer struct{ dialed atomic.Bool }

func (d *failingDialer) DialContext(_ context.Context, network, address string) (net.Conn, error) {
	d.dialed.Store(true)
	return nil, &net.OpError{Op: "dial", Net: network, Err: errDialBlocked{address}}
}

type errDialBlocked struct{ addr string }

func (e errDialBlocked) Error() string { return "egress blocked by canary dialer: " + e.addr }

// AC-6 (runtime half): Embed under the sentinel dialer attempts no dial. Runs
// on the synthetic model always, and on the pinned artifact when present.
func TestSpike_EmbedAttemptsNoDial(t *testing.T) {
	dialer := &failingDialer{}
	orig := net.DefaultResolver
	net.DefaultResolver = &net.Resolver{PreferGo: true, Dial: dialer.DialContext}
	t.Cleanup(func() { net.DefaultResolver = orig })

	texts := []string{"", "hello world", "認証トークンを検証する関数", "func engine/auth.ValidateToken", "http://example.com/dial?me=now"}
	m := loadSyntheticModel(t)
	_ = m.Embed(texts)
	_ = m.EmbedFloat32(texts)
	if dialer.dialed.Load() {
		t.Fatal("Embed on the synthetic model attempted an outbound dial — zero-egress violated")
	}
	if artifactPresent(artifactDir()) {
		_ = loadPinnedModel(t).Embed(texts)
		if dialer.dialed.Load() {
			t.Fatal("Embed on the pinned artifact attempted an outbound dial — zero-egress violated")
		}
	}
}
