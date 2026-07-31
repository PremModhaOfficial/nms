package monitorFailure

import (
	"context"
	"testing"
	"time"

	"nms/pkg/models"
)

// fakeEntity is a minimal EntityService stand-in that replies to every request
// and forwards each request to captured for assertions.
func fakeEntity(ctx context.Context, reqCh <-chan models.Request, captured chan<- models.Request) {
	for {
		select {
		case <-ctx.Done():
			return
		case req := <-reqCh:
			captured <- req
			req.ReplyCh <- models.Response{}
		}
	}
}

func TestHandleFailureDeactivatesAtThreshold(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	reqCh := make(chan models.Request, 4)
	captured := make(chan models.Request, 4)
	go fakeEntity(ctx, reqCh, captured)

	svc := NewHealthMonitor(nil, reqCh, 5, 3) // 5 min window, threshold 3
	now := time.Now()
	ev := &models.DeviceFailureEvent{DeviceID: 1, Timestamp: now, Reason: "ping"}

	// Two failures within window: count rises but stays below threshold.
	svc.handleFailure(ctx, ev)
	svc.handleFailure(ctx, ev)
	if got := svc.failures[1].Count; got != 2 {
		t.Fatalf("count after 2 failures = %d, want 2", got)
	}
	// No deactivation request should have been sent yet.
	select {
	case req := <-captured:
		t.Fatalf("unexpected request before threshold: %+v", req)
	default:
	}

	// Third failure: deactivation request must reach the entity service.
	svc.handleFailure(ctx, ev)
	select {
	case req := <-captured:
		if req.Operation != models.OpDeactivateDevice || req.ID != 1 {
			t.Fatalf("unexpected request: op=%v id=%d", req.Operation, req.ID)
		}
	case <-time.After(time.Second):
		t.Fatal("no deactivation request sent")
	}

	// Record cleaned up after deactivation.
	if _, exists := svc.failures[1]; exists {
		t.Fatal("failure record should be deleted after deactivation")
	}
}

func TestDeactivateDeviceTimeoutDoesNotHang(t *testing.T) {
	// No responder: the RPC must give up when the deadline expires.
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	reqCh := make(chan models.Request, 1)
	svc := NewHealthMonitor(nil, reqCh, 5, 3)

	done := make(chan struct{})
	go func() {
		svc.deactivateDevice(ctx, 1)
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("deactivateDevice hung past the deadline")
	}
}

func TestRunStopsOnClosedChannel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	failureChan := make(chan models.Event)
	close(failureChan)

	svc := NewHealthMonitor(failureChan, nil, 5, 3)
	done := make(chan struct{})
	go func() {
		svc.Run(ctx)
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Run did not return after failure channel closed")
	}
}
