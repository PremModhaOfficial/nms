package pluginWorker

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type testTask struct {
	ID int `json:"id"`
}

type testResult struct {
	ID int `json:"id"`
}

const fakePlugin = `#!/bin/sh
cat >/dev/null
echo '[{"id":42}]'
`

func writePlugin(t *testing.T, dir, name, body string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
		t.Fatalf("write plugin: %v", err)
	}
	return path
}

func waitForClose(t *testing.T, pool *PluginWorkerPool[testTask, testResult]) {
	t.Helper()
	select {
	case _, ok := <-pool.Results():
		if ok {
			t.Fatal("result channel still open after shutdown")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("result channel never closed after shutdown")
	}
}

func TestPoolRoundTrip(t *testing.T) {
	dir := t.TempDir()
	bin := writePlugin(t, dir, "fakeplug", fakePlugin)

	pool := NewPool[testTask, testResult](2, "test", 4)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	pool.Start(ctx)

	if !pool.Submit(bin, []testTask{{ID: 1}, {ID: 2}}) {
		t.Fatal("Submit returned false on a running pool")
	}

	select {
	case results := <-pool.Results():
		if len(results) != 1 || results[0].ID != 42 {
			t.Fatalf("unexpected results: %+v", results)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for results")
	}

	cancel()
	waitForClose(t, pool)
}

func TestPoolShutdownRejectsSubmit(t *testing.T) {
	dir := t.TempDir()
	bin := writePlugin(t, dir, "fakeplug", fakePlugin)

	pool := NewPool[testTask, testResult](2, "test", 4)
	ctx, cancel := context.WithCancel(context.Background())
	pool.Start(ctx)
	cancel()
	waitForClose(t, pool)

	if pool.Submit(bin, []testTask{{ID: 1}}) {
		t.Fatal("Submit returned true on a cancelled pool")
	}
}

// TestPoolShutdownCancelsInFlightPlugin proves that cancelling the lifecycle
// ctx kills a plugin process still running (sleep 30) instead of hanging the
// pool until the plugin finishes on its own.
func TestPoolShutdownCancelsInFlightPlugin(t *testing.T) {
	dir := t.TempDir()
	bin := writePlugin(t, dir, "slowplug", "#!/bin/sh\ncat >/dev/null\nsleep 30\necho '[]'\n")

	pool := NewPool[testTask, testResult](1, "test", 1)
	ctx, cancel := context.WithCancel(context.Background())
	pool.Start(ctx)

	if !pool.Submit(bin, []testTask{{ID: 1}}) {
		t.Fatal("Submit failed on running pool")
	}

	time.Sleep(200 * time.Millisecond) // let the plugin start sleeping
	cancel()

	// In-flight sleep(30) must be killed via process-group kill, so the pool
	// closes its results channel well before the plugin would have finished.
	// Drain any empty batches delivered during shutdown, then require close.
	deadline := time.After(5 * time.Second)
	for {
		select {
		case _, ok := <-pool.Results():
			if !ok {
				return
			}
		case <-deadline:
			t.Fatal("result channel never closed after shutdown")
		}
	}
}

// TestPoolWorkerSurvivesBadPluginOutput proves a worker keeps processing jobs
// after a plugin returns invalid JSON (the error path logs and drops).
func TestPoolWorkerSurvivesBadPluginOutput(t *testing.T) {
	dir := t.TempDir()
	badBin := writePlugin(t, dir, "badplug", "#!/bin/sh\ncat >/dev/null\necho 'NOT JSON'\n")
	goodBin := writePlugin(t, dir, "goodplug", fakePlugin)

	pool := NewPool[testTask, testResult](1, "test", 4)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	pool.Start(ctx)

	if !pool.Submit(badBin, []testTask{{ID: 1}}) {
		t.Fatal("submit failed")
	}
	if !pool.Submit(goodBin, []testTask{{ID: 2}}) {
		t.Fatal("submit failed")
	}

	deadline := time.After(5 * time.Second)
	for {
		select {
		case results := <-pool.Results():
			if len(results) == 0 {
				continue // bad plugin's empty batch
			}
			if len(results) != 1 || results[0].ID != 42 {
				t.Fatalf("unexpected results: %+v", results)
			}
			cancel()
			waitForClose(t, pool)
			return
		case <-deadline:
			t.Fatal("timed out waiting for good results after bad output")
		}
	}
}

func TestNewPoolValidatesArgs(t *testing.T) {
	for _, tc := range []struct {
		name        string
		workers     int
		buffer      int
		wantPanic   bool
		panicPrefix string
	}{
		{name: "valid", workers: 1, buffer: 1},
		{name: "zero workers", workers: 0, buffer: 1, wantPanic: true, panicPrefix: "workerCount"},
		{name: "negative workers", workers: -1, buffer: 1, wantPanic: true, panicPrefix: "workerCount"},
		{name: "negative buffer", workers: 1, buffer: -1, wantPanic: true, panicPrefix: "bufferSize"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			defer func() {
				r := recover()
				if tc.wantPanic {
					if r == nil {
						t.Fatal("expected panic, got none")
					}
					if msg, ok := r.(string); !ok || !strings.Contains(msg, tc.panicPrefix) {
						t.Fatalf("panic %v missing prefix %q", r, tc.panicPrefix)
					}
				} else if r != nil {
					t.Fatalf("unexpected panic: %v", r)
				}
			}()
			NewPool[testTask, testResult](tc.workers, "test", tc.buffer)
		})
	}
}

func TestPoolDoubleStartPanics(t *testing.T) {
	pool := NewPool[testTask, testResult](1, "test", 1)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	pool.Start(ctx)

	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected panic on double Start, got none")
		}
	}()
	pool.Start(ctx)
}
