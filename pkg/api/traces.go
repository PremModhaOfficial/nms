package api

import (
	"net/http"
	"strconv"

	"nms/pkg/tracex"
)

const (
	defaultTraceLimit = 50  // traces returned when ?limit= is absent
	maxTraceLimit     = 200 // hard cap on trace list size
	minTraceLimit     = 1   // floor so an explicit 0 cannot return nothing
)

// TopologyHandler serves the declared component graph. The topology is the
// exact replica of ARCHITECTURE.md that the dashboard renders live.
func TopologyHandler(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, tracex.Topology())
}

// TracesListHandler lists trace metadata (no span trees) from the in-memory
// trace store. The response shape is {"traces": [...]}.
func TracesListHandler(w http.ResponseWriter, r *http.Request) {
	limit := defaultTraceLimit
	if raw := r.URL.Query().Get("limit"); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil {
			limit = n
		}
	}
	if limit < minTraceLimit {
		limit = minTraceLimit
	}
	if limit > maxTraceLimit {
		limit = maxTraceLimit
	}

	traces := tracex.Default().List(limit)
	// Metadata only: drop spans so the list response stays small and fast.
	for i := range traces {
		traces[i].Spans = nil
	}
	writeJSON(w, http.StatusOK, map[string]any{"traces": traces})
}

// TraceGetHandler serves a single trace with its full span tree.
func TraceGetHandler(w http.ResponseWriter, r *http.Request) {
	t, ok := tracex.Default().Get(r.PathValue("id"))
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "trace not found"})
		return
	}
	writeJSON(w, http.StatusOK, t)
}
