package api

import (
	"encoding/json"
	"net/http"
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
