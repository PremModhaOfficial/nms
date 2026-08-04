package api

import (
	"bytes"
	"io"
	"net/http"

	"nms/pkg/tracex"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
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

// maxTraceRequestBody caps how much of a request body is kept for the trace
// event. The capture reads limit+1 bytes so a body larger than the API's 1 MiB
// MaxBodyBytes cap still trips MaxBytesReader after being restored, preserving
// the original rejection semantics.
const maxTraceRequestBody = 1 << 20 // 1 MiB

// TraceMiddleware wraps every HTTP request in a root OTel span and captures
// request/response bodies as trace events (credentials are masked by tracex).
// It must wrap the outermost handler so auth failures and 4xx/5xx responses
// are traced too. The span context is threaded through to handlers via the
// request context.
func TraceMiddleware() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// r.Pattern is only populated by ServeMux dispatch; when this
			// middleware sits outside the mux (as it must, to see auth
			// failures), fall back to the concrete path.
			pattern := r.Pattern
			if pattern == "" {
				pattern = r.URL.Path
			}
			ctx, span := tracex.Start(r.Context(), "api", "HTTP "+r.Method+" "+pattern)
			defer span.End()

			span.SetAttributes(
				attribute.String("http.method", r.Method),
				attribute.String("http.route", pattern),
				attribute.Bool("nms.root", true),
			)

			// Capture the request body, then restore it for the handler. GET
			// and HEAD carry no meaningful body.
			if r.Body != nil && r.Method != http.MethodGet && r.Method != http.MethodHead {
				if buf, err := io.ReadAll(io.LimitReader(r.Body, maxTraceRequestBody+1)); err == nil {
					r.Body = io.NopCloser(bytes.NewReader(buf))
					body := buf
					if len(body) > maxTraceRequestBody {
						body = body[:maxTraceRequestBody]
					}
					span.AddEvent("request.body", tracex.BodyEvent("request.body", tracex.MaskJSON(body)))
				}
			}

			cw := &captureResponseWriter{ResponseWriter: w}
			next.ServeHTTP(cw, r.WithContext(ctx))

			status := cw.Status()
			span.SetAttributes(attribute.Int("http.status_code", status))
			if status >= 400 {
				span.SetStatus(codes.Error, http.StatusText(status))
			}
			if status >= 500 {
				// Frontend uses this flag to surface error traces.
				span.SetAttributes(attribute.Bool("nms.trace.error", true))
			}
			if body := cw.Bytes(); len(body) > 0 {
				span.AddEvent("response.body", tracex.BodyEvent("response.body", tracex.MaskJSON(body)))
			}
		})
	}
}
