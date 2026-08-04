package tracex

import "sync"

// TraceBufferSize is the maximum number of traces retained by the ring buffer.
const TraceBufferSize = 1000

// Store is a bounded, thread-safe ring buffer of finalized traces. It never
// allocates beyond TraceBufferSize and always returns deep copies so callers
// cannot mutate stored state.
type Store struct {
	mu    sync.RWMutex
	buf   [TraceBufferSize]*Trace
	start int // index of the oldest retained trace
	count int // number of retained traces (0..TraceBufferSize)
}

// NewStore returns an empty trace store.
func NewStore() *Store {
	return &Store{}
}

// Add stores a copy of t, evicting the oldest trace when the buffer is full.
// Nil traces are rejected (caller bug, never a panic).
func (s *Store) Add(t *Trace) {
	if t == nil {
		return
	}
	cp := cloneTrace(t)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.buf[s.ringIdx(s.start+s.count)] = cp
	if s.count < TraceBufferSize {
		s.count++
	} else {
		s.start = s.ringIdx(s.start + 1)
	}
}

// Len returns the number of retained traces.
func (s *Store) Len() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.count
}

// List returns up to limit traces, newest first. limit is clamped to
// [1, TraceBufferSize]. The returned traces are deep copies.
func (s *Store) List(limit int) []Trace {
	if limit < 1 {
		limit = 1
	}
	if limit > TraceBufferSize {
		limit = TraceBufferSize
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	n := s.count
	if limit < n {
		n = limit
	}
	out := make([]Trace, 0, n)
	for i := 0; i < n; i++ {
		idx := s.ringIdx(s.start + s.count - 1 - i) // newest first
		if s.buf[idx] != nil {
			out = append(out, *cloneTrace(s.buf[idx]))
		}
	}
	return out
}

// Get returns a deep copy of the trace with the given ID, or false if it is
// not retained.
func (s *Store) Get(id string) (*Trace, bool) {
	if id == "" {
		return nil, false
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	for i := 0; i < s.count; i++ {
		idx := s.ringIdx(s.start + i)
		if s.buf[idx] != nil && s.buf[idx].TraceID == id {
			return cloneTrace(s.buf[idx]), true
		}
	}
	return nil, false
}

// AppendSpan merges a late-arriving child span into an already-finalized
// trace. Async channel continuations (event handlers, plugin pools) end after
// the HTTP root span, so they reach the exporter after the trace was finalized.
// Returns false when the trace is not retained, so callers can fall back to
// pending/eviction handling. The span is deep-copied; the store never aliases.
func (s *Store) AppendSpan(traceID string, sp Span) bool {
	if traceID == "" {
		return false
	}
	cp := cloneSpan(sp)
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := 0; i < s.count; i++ {
		idx := s.ringIdx(s.start + i)
		t := s.buf[idx]
		if t == nil || t.TraceID != traceID {
			continue
		}
		// Skip duplicates (retried exports or double-finalized spans).
		for _, existing := range t.Spans {
			if existing.SpanID == cp.SpanID {
				return true
			}
		}
		t.Spans = append(t.Spans, cp)
		t.SpanCount = len(t.Spans)
		if t.EndedAt.Before(cp.EndedAt) {
			t.EndedAt = cp.EndedAt
		}
		t.ComponentIDs = componentIDs(t.Spans)
		return true
	}
	return false
}

// ringIdx normalizes an index into [0, TraceBufferSize) without panicking on
// negative or overflow values (tiger: no unchecked arithmetic on exported paths).
func (s *Store) ringIdx(i int) int {
	i %= TraceBufferSize
	if i < 0 {
		i += TraceBufferSize
	}
	return i
}

// cloneTrace deep-copies a trace so the store never hands out or accepts
// aliases to mutable state.
func cloneTrace(t *Trace) *Trace {
	if t == nil {
		return nil
	}
	cp := *t
	if t.ComponentIDs != nil {
		cp.ComponentIDs = append([]string(nil), t.ComponentIDs...)
	}
	if t.Spans != nil {
		cp.Spans = make([]Span, len(t.Spans))
		for i, sp := range t.Spans {
			cp.Spans[i] = cloneSpan(sp)
		}
	}
	return &cp
}

func cloneSpan(sp Span) Span {
	cp := sp
	if sp.Attributes != nil {
		cp.Attributes = make(map[string]any, len(sp.Attributes))
		for k, v := range sp.Attributes {
			cp.Attributes[k] = v
		}
	}
	if sp.Events != nil {
		cp.Events = make([]SpanEvent, len(sp.Events))
		for i, ev := range sp.Events {
			cp.Events[i] = cloneSpanEvent(ev)
		}
	}
	return cp
}

func cloneSpanEvent(ev SpanEvent) SpanEvent {
	cp := ev
	if ev.Attributes != nil {
		cp.Attributes = make(map[string]any, len(ev.Attributes))
		for k, v := range ev.Attributes {
			cp.Attributes[k] = v
		}
	}
	return cp
}
