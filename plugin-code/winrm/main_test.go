package main

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestSanitizeJSON(t *testing.T) {
	// BOM + whitespace must be stripped, content preserved.
	raw := "\uFEFF  {\"cpu\":{\"total\":42}}  "
	got := sanitizeJSON(raw)
	if !json.Valid(got) {
		t.Fatalf("sanitized output not valid JSON: %q", string(got))
	}
	if string(got) != `{"cpu":{"total":42}}` {
		t.Fatalf("sanitized = %q", string(got))
	}
}

func TestProcessTaskRejectsEmptyTarget(t *testing.T) {
	out := processTask(Input{})
	if out.Success {
		t.Fatal("empty target must not succeed")
	}
	if out.Error == "" {
		t.Fatal("empty target must produce an error")
	}
}

func TestProcessTaskRejectsMissingCredentials(t *testing.T) {
	out := processTask(Input{Target: "192.168.1.10"})
	if out.Success {
		t.Fatal("missing credentials must not succeed")
	}
	if !strings.Contains(out.Error, "username") {
		t.Fatalf("error = %q, want username failure", out.Error)
	}
}

func TestProcessTaskRejectsBadPort(t *testing.T) {
	out := processTask(Input{Target: "192.168.1.10", Port: 70000})
	if out.Success {
		t.Fatal("invalid port must not succeed")
	}
	if !strings.Contains(out.Error, "invalid port") {
		t.Fatalf("error = %q, want invalid port", out.Error)
	}
}

func TestProcessTaskDefaultsToTLS(t *testing.T) {
	// Default port must be 5986 (HTTPS); plaintext 5985 only via -insecure.
	out := processTask(Input{Target: "192.168.1.10"})
	if out.Port != defaultPort {
		t.Fatalf("default port = %d, want %d", out.Port, defaultPort)
	}
}
