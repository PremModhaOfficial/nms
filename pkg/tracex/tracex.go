package tracex

import (
	"context"
	"encoding/json"
	"strings"
	"sync"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
)

var (
	storeOnce sync.Once
	store     *Store

	initOnce sync.Once
	provider trace.TracerProvider
)

// Default returns the process-wide trace store, creating it on first use.
func Default() *Store {
	storeOnce.Do(func() {
		store = NewStore()
	})
	return store
}

// Init builds the OTel tracer provider backed by the in-process store, sets it
// as the global provider, and returns a shutdown hook. It is idempotent:
// subsequent calls return a hook for the original provider without building a
// new one. The hook force-flushes pending spans into the store and shuts the
// provider down.
func Init() func() {
	initOnce.Do(func() {
		exp := newExporter(Default())
		tp := sdktrace.NewTracerProvider(
			sdktrace.WithSpanProcessor(sdktrace.NewBatchSpanProcessor(exp,
				sdktrace.WithMaxQueueSize(4096),
				sdktrace.WithMaxExportBatchSize(512),
			)),
		)
		provider = tp
		otel.SetTracerProvider(tp)
	})
	return func() {
		if tp, ok := provider.(*sdktrace.TracerProvider); ok && tp != nil {
			_ = tp.ForceFlush(context.Background())
			_ = tp.Shutdown(context.Background())
		}
	}
}

// Tracer returns the provider tracer named "nms". Before Init it returns a
// no-op tracer so instrumentation is safe in tests and builds that skip Init.
func Tracer() trace.Tracer {
	if provider == nil {
		return trace.NewNoopTracerProvider().Tracer("nms")
	}
	return provider.Tracer("nms")
}

// Start begins a span tagged with the component that owns it, so the exporter
// can map spans back to topology nodes.
func Start(ctx context.Context, component, name string) (context.Context, trace.Span) {
	return Tracer().Start(ctx, name, trace.WithAttributes(attribute.String("nms.component", component)))
}

// sensitiveKeys are JSON object keys whose values are redacted by MaskJSON.
// Case-insensitive matching covers "Authorization" headers and the like.
var sensitiveKeys = []string{"payload", "password", "secret", "token", "authorization"}

// MaskJSON recursively replaces the value of every sensitive key with the
// string "[HIDDEN]". Invalid JSON is returned unchanged and MaskJSON never
// fails.
func MaskJSON(b []byte) []byte {
	var v any
	if err := json.Unmarshal(b, &v); err != nil {
		return b
	}
	maskJSONValue(v)
	out, err := json.Marshal(v)
	if err != nil {
		return b
	}
	return out
}

func maskJSONValue(v any) {
	switch t := v.(type) {
	case map[string]any:
		for k, val := range t {
			if isSensitiveKey(k) {
				t[k] = "[HIDDEN]"
			} else {
				maskJSONValue(val)
			}
		}
	case []any:
		for _, item := range t {
			maskJSONValue(item)
		}
	}
}

func isSensitiveKey(k string) bool {
	for _, name := range sensitiveKeys {
		if strings.EqualFold(k, name) {
			return true
		}
	}
	return false
}

// BodyEvent returns a span event option that records name with a masked,
// 64 KiB-capped copy of body under the "body" attribute. Credential payloads
// never leave the server.
func BodyEvent(name string, body []byte) trace.EventOption {
	masked := MaskJSON(body)
	if len(masked) > maxBodyAttrBytes {
		masked = masked[:maxBodyAttrBytes]
	}
	return trace.WithAttributes(attribute.String("body", string(masked)))
}
