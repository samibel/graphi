package exthost

// AC-5's instrument.
//
// The go/no-go needs numbers for startup latency, memory and packaging cost, and
// the numbers belong in the decision document — NOT in docs/eval/hero-budgets.json.
// That separation is deliberate and follows the repo's rule about not extending
// one measurement instrument to serve another: hero budgets gate the shipped
// product's performance, and a disposable spike must never become a row a
// release gate depends on.
//
// So this file MEASURES and REPORTS; it asserts only bounds so loose that a
// failure means something is structurally wrong, never that a runner was busy.
// Run it with `-v` to read the numbers:
//
//	go test ./engine/exthost -run TestSW231_AC5_Measurements -v -count=1

import (
	"encoding/json"
	"os"
	"os/exec"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/samibel/graphi/engine/opcatalog"
)

func TestSW231_AC5_Measurements(t *testing.T) {
	descriptor := stageExtension(t, descriptorOptions{})
	loaded, err := LoadDescriptor(descriptor)
	if err != nil {
		t.Fatalf("LoadDescriptor: %v", err)
	}

	// 1. Packaging: how big is the artifact a user has to install, and how long
	//    does verifying it cost on every single activation?
	info, err := os.Stat(loaded.BinaryPath)
	if err != nil {
		t.Fatalf("stat artifact: %v", err)
	}
	verifyStart := time.Now()
	for i := 0; i < 5; i++ {
		if _, err := LoadDescriptor(descriptor); err != nil {
			t.Fatalf("LoadDescriptor: %v", err)
		}
	}
	verifyEach := time.Since(verifyStart) / 5

	// 2. Startup: spawn + handshake, measured over enough runs to report a
	//    median rather than one sample.
	const runs = 11
	starts := make([]time.Duration, 0, runs)
	firstCalls := make([]time.Duration, 0, runs)
	var rssKB int
	for i := 0; i < runs; i++ {
		t0 := time.Now()
		ext, _ := startExample(t, descriptor, "")
		starts = append(starts, time.Since(t0))

		t1 := time.Now()
		if _, err := ext.Call(callCtx(t), exampleOperation, json.RawMessage(`{"symbol":"Hel"}`)); err != nil {
			t.Fatalf("Call: %v", err)
		}
		firstCalls = append(firstCalls, time.Since(t1))
		if i == runs/2 {
			rssKB = childRSSKB(ext)
		}
		_ = ext.Close()
	}

	// 3. Host-side memory: what does holding a running extension cost the graphi
	//    process itself? Measured as the live-heap delta across start/close, with
	//    the GC forced so the number is the retained working set rather than
	//    whatever the allocator had not swept yet.
	runtime.GC()
	var before runtime.MemStats
	runtime.ReadMemStats(&before)
	held := make([]*Extension, 0, 4)
	for i := 0; i < 4; i++ {
		ext, _ := startExample(t, descriptor, "")
		held = append(held, ext)
	}
	runtime.GC()
	var during runtime.MemStats
	runtime.ReadMemStats(&during)
	for _, ext := range held {
		_ = ext.Close()
	}
	hostBytesPerExtension := int64(during.HeapAlloc-before.HeapAlloc) / int64(len(held))

	t.Logf("SW-231 AC-5 measurements (%s/%s, go %s)", runtime.GOOS, runtime.GOARCH, runtime.Version())
	t.Logf("  example extension artifact:        %d bytes (%.2f MiB)",
		info.Size(), float64(info.Size())/(1<<20))
	t.Logf("  descriptor load + SHA-256 verify:  %v per activation (whole artifact is hashed)", verifyEach)
	t.Logf("  spawn + handshake:                 median %v  (min %v, max %v, n=%d)",
		median(starts), minOf(starts), maxOf(starts), runs)
	t.Logf("  first call round trip:             median %v  (min %v, max %v, n=%d)",
		median(firstCalls), minOf(firstCalls), maxOf(firstCalls), runs)
	if rssKB > 0 {
		t.Logf("  child process RSS:                 %d KiB (%.1f MiB)", rssKB, float64(rssKB)/1024)
	} else {
		t.Logf("  child process RSS:                 unavailable on this platform")
	}
	t.Logf("  host heap held per live extension: %d bytes", hostBytesPerExtension)

	// Bounds loose enough that only a structural regression trips them.
	if m := median(starts); m > 5*time.Second {
		t.Errorf("spawn + handshake median %v — an extension that takes seconds to say hello is not "+
			"a usable interactive capability", m)
	}
	if hostBytesPerExtension > 4<<20 {
		t.Errorf("the host retains %d bytes per live extension; the buffers are supposed to be "+
			"bounded (32 KiB read buffer, 8 KiB stderr ring)", hostBytesPerExtension)
	}
}

// TestSW231_AC5_PackagingEffort records what an author has to produce, as a
// number rather than an impression.
//
// The three counts below are the whole packaging story for tier C, and each is
// something a tier-A pack author does NOT have to do: compile a per-platform
// executable, hash it, and hand-write a descriptor whose port list has no
// tooling behind it (there is no `graphi extension init --kind process-analyzer`;
// extpack's scaffold cannot express this kind — see KindProcessAnalyzer).
func TestSW231_AC5_PackagingEffort(t *testing.T) {
	descriptor := stageExtension(t, descriptorOptions{})
	raw, err := os.ReadFile(descriptor)
	if err != nil {
		t.Fatalf("read descriptor: %v", err)
	}
	src, err := os.ReadFile("../../extensions/example-analyzer/main.go")
	if err != nil {
		t.Fatalf("read example source: %v", err)
	}
	t.Logf("SW-231 AC-5 packaging effort")
	t.Logf("  descriptor:                        %d lines of YAML, hand-written", countLines(string(raw)))
	t.Logf("  example extension source:          %d lines of Go (of which ~%d are protocol plumbing)",
		countLines(string(src)), protocolPlumbingLines(string(src)))
	t.Logf("  scaffold available:                NO — extpack's `graphi extension init` scaffolds " +
		"tier-A pack kinds only; a tier-C author starts from a blank file")
	t.Logf("  cross-platform artifacts required: one per GOOS/GOARCH the user might run, each with " +
		"its own sha256 — the descriptor schema pins exactly ONE artifact")
	t.Logf("  language neutrality:               the wire is plain JSON, but the only implementation " +
		"of the codec is Go (engine/exthost), so a non-Go author reimplements framing from prose")

	// The one packaging fact worth ASSERTING rather than logging: the descriptor
	// schema pins exactly ONE artifact, so a multi-platform extension cannot be
	// described by a single descriptor. That is a real distribution limitation
	// and it belongs in the go/no-go, so it is pinned rather than remembered.
	if _, err := ParseDescriptor([]byte(strings.Replace(string(raw),
		"artifact:", "artifacts:", 1))); err == nil {
		t.Fatal("the descriptor schema accepted a plural `artifacts` key; if multi-platform " +
			"artifacts have been added, the decision document's distribution finding is stale")
	}
}

// BenchmarkSpikeStartHandshake measures the cost of activating an extension:
// hash verification, spawn, handshake.
func BenchmarkSpikeStartHandshake(b *testing.B) {
	descriptor := stageExtension(b, descriptorOptions{})
	port := &searchPort{}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		ext, err := Start(b.Context(), Config{
			Activated: true, DescriptorPath: descriptor,
			Ports: map[opcatalog.Port]PortHandler{opcatalog.PortGraphSearch: port.handle},
		})
		if err != nil {
			b.Fatalf("Start: %v", err)
		}
		_ = ext.Close()
	}
}

// BenchmarkSpikeCall measures one round trip on a WARM extension: the marginal
// cost of the process boundary once the process exists.
func BenchmarkSpikeCall(b *testing.B) {
	descriptor := stageExtension(b, descriptorOptions{})
	port := &searchPort{}
	ext, err := Start(b.Context(), Config{
		Activated: true, DescriptorPath: descriptor,
		Ports: map[opcatalog.Port]PortHandler{opcatalog.PortGraphSearch: port.handle},
	})
	if err != nil {
		b.Fatalf("Start: %v", err)
	}
	defer func() { _ = ext.Close() }()
	args := json.RawMessage(`{"symbol":"Hel"}`)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := ext.Call(b.Context(), exampleOperation, args); err != nil {
			b.Fatalf("Call: %v", err)
		}
	}
}

// childRSSKB reads the child's resident set size through ps. Best-effort: a
// platform without ps reports 0 and the measurement is logged as unavailable
// rather than guessed at.
func childRSSKB(e *Extension) int {
	if e.cmd == nil || e.cmd.Process == nil {
		return 0
	}
	out, err := exec.Command("ps", "-o", "rss=", "-p", strconv.Itoa(e.cmd.Process.Pid)).Output()
	if err != nil {
		return 0
	}
	kb, err := strconv.Atoi(strings.TrimSpace(string(out)))
	if err != nil {
		return 0
	}
	return kb
}

func countLines(s string) int { return len(strings.Split(strings.TrimSpace(s), "\n")) }

// protocolPlumbingLines counts the lines of the example that exist only to speak
// the protocol — handshake, frame loop, port round trip — as opposed to the
// analysis itself. It is a rough number and is reported as one; its job is to
// answer "how much of an extension is boilerplate?" with an order of magnitude.
func protocolPlumbingLines(src string) int {
	n := 0
	for _, line := range strings.Split(src, "\n") {
		if strings.Contains(line, "exthost.") {
			n++
		}
	}
	return n
}

func median(d []time.Duration) time.Duration {
	if len(d) == 0 {
		return 0
	}
	c := append([]time.Duration(nil), d...)
	sort.Slice(c, func(i, j int) bool { return c[i] < c[j] })
	return c[len(c)/2]
}

func minOf(d []time.Duration) time.Duration {
	m := d[0]
	for _, v := range d {
		if v < m {
			m = v
		}
	}
	return m
}

func maxOf(d []time.Duration) time.Duration {
	m := d[0]
	for _, v := range d {
		if v > m {
			m = v
		}
	}
	return m
}
