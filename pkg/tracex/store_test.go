package tracex

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"go.opentelemetry.io/otel/attribute"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
)

// TestStoreRingEviction covers the bounded ring buffer: capacity cap, oldest
// eviction, newest-first List ordering, and limit clamping.
func TestStoreRingEviction(t *testing.T) {
	s := NewStore()
	total := TraceBufferSize + 50
	for i := 0; i < total; i++ {
		s.Add(&Trace{TraceID: fmt.Sprintf("trace-%d", i), Spans: []Span{{SpanID: fmt.Sprintf("span-%d", i)}}})
	}
	if got := s.Len(); got != TraceBufferSize {
		t.Fatalf("Len() = %d, want %d", got, TraceBufferSize)
	}
	if _, ok := s.Get("trace-0"); ok {
		t.Fatal("oldest trace should have been evicted")
	}
	oldestRetained := total - TraceBufferSize // 50
	if _, ok := s.Get(fmt.Sprintf("trace-%d", oldestRetained)); !ok {
		t.Fatalf("oldest retained trace trace-%d missing", oldestRetained)
	}
	if _, ok := s.Get(fmt.Sprintf("trace-%d", total-1)); !ok {
		t.Fatal("newest trace should be retained")
	}

	list := s.List(10000)
	if len(list) != TraceBufferSize {
		t.Fatalf("List(10000) len = %d, want %d", len(list), TraceBufferSize)
	}
	if list[0].TraceID != fmt.Sprintf("trace-%d", total-1) {
		t.Fatalf("List[0] = %s, want newest trace-%d", list[0].TraceID, total-1)
	}
	if list[len(list)-1].TraceID != fmt.Sprintf("trace-%d", oldestRetained) {
		t.Fatalf("List[last] = %s, want trace-%d", list[len(list)-1].TraceID, oldestRetained)
	}

	// Limit clamps: 0 -> 1, negative -> 1, over cap -> cap.
	if got := len(s.List(0)); got != 1 {
		t.Fatalf("List(0) len = %d, want 1", got)
	}
	if got := len(s.List(-5)); got != 1 {
		t.Fatalf("List(-5) len = %d, want 1", got)
	}
	if got := len(s.List(TraceBufferSize + 10)); got != TraceBufferSize {
		t.Fatalf("List(cap+10) len = %d, want %d", got, TraceBufferSize)
	}

	// Get returns a copy: mutating it must not affect the store.
	tr, ok := s.Get(fmt.Sprintf("trace-%d", total-1))
	if !ok {
		t.Fatal("Get on retained trace failed")
	}
	tr.TraceID = "mutated"
	if again, _ := s.Get(fmt.Sprintf("trace-%d", total-1)); again == nil || again.TraceID != fmt.Sprintf("trace-%d", total-1) {
		t.Fatal("store state mutated by caller")
	}
}

// newTestProvider wires a store-backed exporter through a synchronous
// SimpleSpanProcessor so spans export deterministically at End().
func newTestProvider(store *Store) *sdktrace.TracerProvider {
	return sdktrace.NewTracerProvider(
		sdktrace.WithSpanProcessor(sdktrace.NewSimpleSpanProcessor(newExporter(store))),
	)
}

// TestExporterFinalizesOnRootArrival drives real OTel spans through the
// exporter: children export first (pending), then the root arrives and the
// trace is finalized with root fields, component order, and masked body events.
func TestExporterFinalizesOnRootArrival(t *testing.T) {
	store := NewStore()
	tp := newTestProvider(store)
	defer tp.Shutdown(context.Background())

	tr := tp.Tracer("test")
	ctx, root := tr.Start(context.Background(), "http.GET /api/v1/devices",
		trace.WithAttributes(
			attribute.String("nms.component", "api"),
			attribute.String("http.method", "GET"),
			attribute.String("http.route", "/api/v1/devices"),
			attribute.Int("http.status_code", 200),
		),
		trace.WithSpanKind(trace.SpanKindServer),
	)
	root.AddEvent("request.body", BodyEvent("request.body", []byte(`{"payload":{"password":"hunter2"}}`)))
	_, child := tr.Start(ctx, "entityService.get",
		trace.WithAttributes(attribute.String("nms.component", "entity")))
	child.End()
	root.End() // root ends last: arrival finalizes the complete trace

	tid := root.SpanContext().TraceID().String()
	got, ok := store.Get(tid)
	if !ok {
		t.Fatal("trace not finalized on root arrival")
	}
	if got.RootSpanID != root.SpanContext().SpanID().String() {
		t.Errorf("RootSpanID = %s, want %s", got.RootSpanID, root.SpanContext().SpanID().String())
	}
	if got.SpanCount != 2 {
		t.Errorf("SpanCount = %d, want 2", got.SpanCount)
	}
	if got.Method != "GET" || got.Path != "/api/v1/devices" || got.StatusCode != 200 {
		t.Errorf("root fields = %q %q %d, want GET /api/v1/devices 200", got.Method, got.Path, got.StatusCode)
	}
	if got.Error {
		t.Error("Error should be false")
	}
	if len(got.ComponentIDs) != 2 || got.ComponentIDs[0] != "api" || got.ComponentIDs[1] != "entity" {
		t.Errorf("ComponentIDs = %v, want [api entity]", got.ComponentIDs)
	}
	if len(got.Spans) != 2 {
		t.Fatalf("Spans = %d, want 2", len(got.Spans))
	}
	rootSpan := got.Spans[0]
	if rootSpan.Component != "api" || rootSpan.Kind != "server" {
		t.Errorf("root span component/kind = %q/%q, want api/server", rootSpan.Component, rootSpan.Kind)
	}
	if childSpan := got.Spans[1]; childSpan.Component != "entity" || childSpan.ParentID != rootSpan.SpanID {
		t.Errorf("child span component/parent = %q/%q, want entity/%s", childSpan.Component, childSpan.ParentID, rootSpan.SpanID)
	}
	if len(rootSpan.Events) != 1 {
		t.Fatalf("root events = %d, want 1", len(rootSpan.Events))
	}
	body, _ := rootSpan.Events[0].Attributes["body"].(string)
	if body != `{"payload":"[HIDDEN]"}` {
		t.Errorf("body event not masked: %s", body)
	}
	if got.DurationMS < 0 {
		t.Errorf("DurationMS = %v, want >= 0", got.DurationMS)
	}
}

// TestExporterRootByAttribute covers the nms.root=true root signal: a span with
// a valid parent is still treated as the root and finalizes the trace.
func TestExporterRootByAttribute(t *testing.T) {
	store := NewStore()
	tp := newTestProvider(store)
	defer tp.Shutdown(context.Background())

	// Synthetic valid parent (no live span) so Parent().IsValid() is true.
	parent := trace.NewSpanContext(trace.SpanContextConfig{
		TraceID:    trace.TraceID{0x01},
		SpanID:     trace.SpanID{0x02},
		TraceFlags: trace.FlagsSampled,
	})
	ctx := trace.ContextWithSpanContext(context.Background(), parent)
	_, span := tp.Tracer("test").Start(ctx, "flagged-root",
		trace.WithAttributes(attribute.Bool("nms.root", true), attribute.String("nms.component", "api")))
	span.End()

	got, ok := store.Get(span.SpanContext().TraceID().String())
	if !ok {
		t.Fatal("trace not finalized via nms.root=true attribute")
	}
	if got.RootSpanID != span.SpanContext().SpanID().String() {
		t.Errorf("RootSpanID = %s, want %s", got.RootSpanID, span.SpanContext().SpanID().String())
	}
	if got.SpanCount != 1 {
		t.Errorf("SpanCount = %d, want 1", got.SpanCount)
	}
	if got.ComponentIDs == nil || len(got.ComponentIDs) != 1 || got.ComponentIDs[0] != "api" {
		t.Errorf("ComponentIDs = %v, want [api]", got.ComponentIDs)
	}
}

// TestMaskJSON covers sensitive-key redaction (including keys nested inside
// non-sensitive objects and arrays) and passthrough of invalid JSON.
func TestMaskJSON(t *testing.T) {
	in := []byte(`{"ok":true,"data":{"password":"hunter2","token":"abc","nested":{"secret":"x","Authorization":"Bearer y","items":[{"token":"z"}]}}}`)
	out := MaskJSON(in)
	var v map[string]any
	if err := json.Unmarshal(out, &v); err != nil {
		t.Fatalf("unmarshal masked output: %v", err)
	}
	if v["ok"] != true {
		t.Errorf("non-sensitive field altered: %v", v["ok"])
	}
	data := v["data"].(map[string]any)
	if data["password"] != "[HIDDEN]" || data["token"] != "[HIDDEN]" {
		t.Errorf("nested password/token not masked: %v", data)
	}
	nested := data["nested"].(map[string]any)
	if nested["secret"] != "[HIDDEN]" || nested["Authorization"] != "[HIDDEN]" {
		t.Errorf("deeper secret/authorization not masked: %v", nested)
	}
	arr := nested["items"].([]any)
	if arr[0].(map[string]any)["token"] != "[HIDDEN]" {
		t.Errorf("array-nested token not masked: %v", arr)
	}

	// A sensitive key is redacted wholesale, even when its value is an object.
	withPayload := []byte(`{"payload":{"password":"x"}}`)
	if got := string(MaskJSON(withPayload)); got != `{"payload":"[HIDDEN]"}` {
		t.Errorf("payload object should collapse to [HIDDEN], got %s", got)
	}

	bad := []byte(`{not json`)
	if got := MaskJSON(bad); string(got) != string(bad) {
		t.Error("invalid JSON should pass through unchanged")
	}
	if got := MaskJSON(nil); got != nil {
		t.Error("nil input should return nil")
	}
}
