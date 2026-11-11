package config

import (
	"strings"
	"testing"
)

const scalarDoc = `# Wormgate server configuration
# NOTE: ports 7000/80/443 are already used by frps on this host.
log:
  level: info

server:
  control_addr: 0.0.0.0:7001
  data_dir: /var/lib/wormgate
  http:
    addr: 0.0.0.0:8080
    domain: 5t.pw

# trailing note
metrics:
  enabled: true
`

func TestSetNestedScalarInsertsAndKeepsComments(t *testing.T) {
	out, err := setNestedScalar(scalarDoc, "server", "stream_secret", "abcdef0123456789")
	if err != nil {
		t.Fatalf("insert: %v", err)
	}
	if !strings.Contains(out, "server:\n  stream_secret: abcdef0123456789\n") {
		t.Fatalf("key not inserted under server:\n%s", out)
	}
	for _, want := range []string{
		"# Wormgate server configuration",
		"# NOTE: ports 7000/80/443 are already used by frps on this host.",
		"# trailing note",
		"  http:\n    addr: 0.0.0.0:8080\n    domain: 5t.pw\n",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("lost %q from document:\n%s", want, out)
		}
	}
	// The blank line before `metrics:` must survive.
	if !strings.Contains(out, "5t.pw\n\n# trailing note") {
		t.Fatalf("blank line/order changed:\n%s", out)
	}
}

func TestSetNestedScalarReplacesInPlace(t *testing.T) {
	first, err := setNestedScalar(scalarDoc, "server", "stream_secret", "first-secret-value")
	if err != nil {
		t.Fatalf("insert: %v", err)
	}
	second, err := setNestedScalar(first, "server", "stream_secret", "second-secret-value")
	if err != nil {
		t.Fatalf("replace: %v", err)
	}
	if strings.Contains(second, "first-secret-value") {
		t.Fatalf("old value kept:\n%s", second)
	}
	if strings.Count(second, "stream_secret:") != 1 {
		t.Fatalf("duplicated key:\n%s", second)
	}
	if !strings.Contains(second, "  stream_secret: second-secret-value\n") {
		t.Fatalf("value not replaced:\n%s", second)
	}
	// Replacement must not move the key or reorder neighbours.
	if strings.Index(second, "stream_secret") > strings.Index(second, "data_dir") {
		t.Fatalf("key moved below data_dir:\n%s", second)
	}
}

func TestSetNestedScalarRemovesKey(t *testing.T) {
	withKey, err := setNestedScalar(scalarDoc, "server", "stream_secret", "abcdef0123456789")
	if err != nil {
		t.Fatalf("insert: %v", err)
	}
	out, err := setNestedScalar(withKey, "server", "stream_secret", "")
	if err != nil {
		t.Fatalf("remove: %v", err)
	}
	if strings.Contains(out, "stream_secret") {
		t.Fatalf("key not removed:\n%s", out)
	}
	if out != scalarDoc {
		t.Fatalf("document did not return to its original shape:\n--- got ---\n%s\n--- want ---\n%s", out, scalarDoc)
	}
}

func TestSetNestedScalarMissingSection(t *testing.T) {
	if _, err := setNestedScalar("log:\n  level: info\n", "server", "stream_secret", "x"); err == nil {
		t.Fatal("expected an error when the section is absent")
	}
	// Removing a key from a missing section is a no-op, not an error.
	out, err := setNestedScalar("log:\n  level: info\n", "server", "stream_secret", "")
	if err != nil {
		t.Fatalf("remove from missing section: %v", err)
	}
	if out != "log:\n  level: info\n" {
		t.Fatalf("document changed: %q", out)
	}
}

func TestSetNestedScalarQuotesUnsafeValues(t *testing.T) {
	out, err := setNestedScalar(scalarDoc, "server", "stream_secret", "has space # and hash")
	if err != nil {
		t.Fatalf("insert: %v", err)
	}
	if !strings.Contains(out, `stream_secret: "has space # and hash"`) {
		t.Fatalf("unsafe value was not quoted:\n%s", out)
	}
}
