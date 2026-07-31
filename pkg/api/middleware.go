package api

import (
	"net/http"
)

// MaxBodyBytes limits the request body size so an unbounded JSON body cannot
// be read fully into memory. Oversized bodies fail decoding with a 400.
func MaxBodyBytes(limit int64) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			r.Body = http.MaxBytesReader(w, r.Body, limit)
			next.ServeHTTP(w, r)
		})
	}
}
