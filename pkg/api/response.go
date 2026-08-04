package api

import (
	"bufio"
	"bytes"
	"encoding/json"
	"net"
	"net/http"
	"strings"
)

// respondError sends a structured JSON error response.
func respondError(w http.ResponseWriter, code int, message string) {
	writeJSON(w, code, map[string]any{
		"error": map[string]any{
			"message": message,
			"status":  code,
		},
	})
}

// writeJSON writes v as a JSON response with the given status code.
func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

// maxCapturedResponseBytes caps the response body kept for trace capture.
// Larger responses are silently truncated; binary bodies are skipped.
const maxCapturedResponseBytes = 64 << 10 // 64 KiB

// captureResponseWriter wraps an http.ResponseWriter and records the status
// code plus a bounded copy of the response body for trace capture. Only
// JSON/text responses are captured; other content types are passed through
// untouched to avoid buffering binary payloads. It stays compatible with
// http.ResponseController, so Flush and Hijack continue to work downstream.
type captureResponseWriter struct {
	http.ResponseWriter
	status    int // first status code written; 0 until headers are sent
	body      bytes.Buffer
	decided   bool // whether capture eligibility has been determined
	doCapture bool // set once decided; whether this response body is kept
}

// WriteHeader records the status code and forwards the call. Duplicate calls
// are ignored, matching net/http behavior.
func (c *captureResponseWriter) WriteHeader(code int) {
	if c.status != 0 {
		return
	}
	c.status = code
	c.ResponseWriter.WriteHeader(code)
}

// Write records a bounded copy of the body when the content type is JSON or
// text, then forwards the write unchanged.
func (c *captureResponseWriter) Write(b []byte) (int, error) {
	if c.status == 0 {
		c.status = http.StatusOK
	}
	if !c.decided {
		c.decided = true
		ct := c.Header().Get("Content-Type")
		c.doCapture = strings.Contains(ct, "json") || strings.Contains(ct, "text")
	}
	if c.doCapture && c.body.Len() < maxCapturedResponseBytes {
		remaining := maxCapturedResponseBytes - c.body.Len()
		if len(b) > remaining {
			c.body.Write(b[:remaining]) // silently truncate beyond the cap
		} else {
			c.body.Write(b)
		}
	}
	return c.ResponseWriter.Write(b)
}

// Status returns the recorded status code, defaulting to 200 when the handler
// wrote no headers at all.
func (c *captureResponseWriter) Status() int {
	if c.status == 0 {
		return http.StatusOK
	}
	return c.status
}

// Bytes returns the captured body copy.
func (c *captureResponseWriter) Bytes() []byte {
	return c.body.Bytes()
}

// Unwrap exposes the wrapped writer so http.ResponseController can reach
// capabilities this wrapper does not implement directly.
func (c *captureResponseWriter) Unwrap() http.ResponseWriter {
	return c.ResponseWriter
}

// Flush forwards to the wrapped writer via http.ResponseController so
// streaming handlers keep working.
func (c *captureResponseWriter) Flush() {
	_ = http.NewResponseController(c.ResponseWriter).Flush()
}

// Hijack forwards to the wrapped writer via http.ResponseController so
// protocols that take over the connection (e.g. WebSocket) keep working.
func (c *captureResponseWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	return http.NewResponseController(c.ResponseWriter).Hijack()
}
