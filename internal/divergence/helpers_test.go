package divergence

import (
	"bytes"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeClock hands out a strictly increasing sequence so first-seen and
// last-seen are distinguishable without sleeping.
type clock struct {
	mu sync.Mutex
	at time.Time
}

func fakeClock(start time.Time) *clock { return &clock{at: start} }

func (c *clock) next() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.at = c.at.Add(time.Second)
	return c.at
}

func renderString(t *testing.T, doc Document) string {
	t.Helper()
	var buf bytes.Buffer
	if err := RenderHuman(&buf, doc); err != nil {
		t.Fatalf("RenderHuman: %v", err)
	}
	return buf.String()
}

func jsonBytes(t *testing.T, doc Document) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := RenderJSON(&buf, doc); err != nil {
		t.Fatalf("RenderJSON: %v", err)
	}
	return buf.Bytes()
}

func contains(haystack, needle string) bool { return strings.Contains(haystack, needle) }

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
