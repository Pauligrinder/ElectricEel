package library

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"
)

type fakeBackend struct {
	execute func(context.Context, Request) Result
	closed  chan struct{}
}

func (b *fakeBackend) Execute(ctx context.Context, req Request) Result { return b.execute(ctx, req) }
func (b *fakeBackend) Close()                                          { close(b.closed) }

type completion struct {
	id     uint64
	result Result
}
type testSink struct {
	completions     chan completion
	diagnostics     chan string
	stopped         chan struct{}
	callbackRelease <-chan struct{}
}

func newSink() *testSink {
	return &testSink{completions: make(chan completion, 8), diagnostics: make(chan string, 8), stopped: make(chan struct{})}
}
func (s *testSink) Complete(id uint64, result Result) { s.completions <- completion{id, result} }
func (s *testSink) Diagnostic(message string)         { s.diagnostics <- message }
func (s *testSink) Stopped() {
	close(s.stopped)
	if s.callbackRelease != nil {
		<-s.callbackRelease
	}
}
func receive[T any](t *testing.T, channel <-chan T) T {
	t.Helper()
	select {
	case value := <-channel:
		return value
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for worker")
		var zero T
		return zero
	}
}

func TestAdmissionCancellationAndShutdown(t *testing.T) {
	started := make(chan struct{})
	backend := &fakeBackend{closed: make(chan struct{}), execute: func(ctx context.Context, _ Request) Result {
		close(started)
		<-ctx.Done()
		return classify(ctx.Err())
	}}
	sink := newSink()
	session := New(backend, sink)
	if status := session.Submit(Request{ID: 1, Operation: Connect, Timeout: time.Minute}); status != OK {
		t.Fatal(status)
	}
	receive(t, started)
	if status := session.Submit(Request{ID: 2, Operation: Disconnect, Timeout: time.Second}); status != Busy {
		t.Fatalf("busy=%d", status)
	}
	if session.Cancel(2) != Invalid {
		t.Fatal("cancel accepted wrong request")
	}
	if session.Cancel(1) != OK {
		t.Fatal("cancel failed")
	}
	result := receive(t, sink.completions)
	if result.id != 1 || result.result.Status != Cancelled {
		t.Fatalf("completion=%+v", result)
	}
	if session.Submit(Request{ID: 1, Operation: Disconnect, Timeout: time.Second}) != Invalid {
		t.Fatal("reused ID accepted")
	}
	session.Stop()
	session.Stop()
	if session.Submit(Request{ID: 3, Operation: Disconnect, Timeout: time.Second}) != Stopping {
		t.Fatal("work accepted after stop")
	}
	receive(t, sink.stopped)
	session.Join()
	receive(t, backend.closed)
	select {
	case extra := <-sink.completions:
		t.Fatalf("extra completion=%+v", extra)
	default:
	}
}

func TestStopCancelsWorkAndWaitsForTerminalCallback(t *testing.T) {
	started := make(chan struct{})
	backend := &fakeBackend{closed: make(chan struct{}), execute: func(ctx context.Context, _ Request) Result {
		close(started)
		<-ctx.Done()
		return classify(ctx.Err())
	}}
	release := make(chan struct{})
	sink := newSink()
	sink.callbackRelease = release
	session := New(backend, sink)
	if session.Submit(Request{ID: 1, Operation: Connect, Timeout: time.Minute}) != OK {
		t.Fatal("submit")
	}
	receive(t, started)
	session.Stop()
	if receive(t, sink.completions).result.Status != Cancelled {
		t.Fatal("work not cancelled")
	}
	receive(t, sink.stopped)
	select {
	case <-session.done:
		t.Fatal("barrier crossed before callback returned")
	default:
	}
	close(release)
	receive(t, session.done)
}

func TestPanicIsLoggedAndFaultsSession(t *testing.T) {
	backend := &fakeBackend{closed: make(chan struct{}), execute: func(context.Context, Request) Result { panic("injected protocol bug") }}
	sink := newSink()
	session := New(backend, sink)
	if session.Submit(Request{ID: 41, Operation: Connect, Timeout: time.Second}) != OK {
		t.Fatal("submit")
	}
	message := receive(t, sink.diagnostics)
	if !strings.Contains(message, "request=41") || !strings.Contains(message, "injected protocol bug") || !strings.Contains(message, "goroutine") {
		t.Fatal(message)
	}
	if receive(t, sink.completions).result.Status != InternalBug {
		t.Fatal("panic hidden")
	}
	receive(t, session.done)
	if session.Submit(Request{ID: 42, Operation: Connect, Timeout: time.Second}) != Stopping {
		t.Fatal("faulted backend reused")
	}
}

func TestStopAndSubmitRace(t *testing.T) {
	for iteration := 0; iteration < 100; iteration++ {
		backend := &fakeBackend{closed: make(chan struct{}), execute: func(ctx context.Context, _ Request) Result { <-ctx.Done(); return classify(ctx.Err()) }}
		sink := newSink()
		session := New(backend, sink)
		var wg sync.WaitGroup
		wg.Add(2)
		var status Status
		go func() {
			defer wg.Done()
			status = session.Submit(Request{ID: 1, Operation: Connect, Timeout: time.Second})
		}()
		go func() { defer wg.Done(); session.Stop() }()
		wg.Wait()
		receive(t, session.done)
		if status == OK {
			if receive(t, sink.completions).result.Status != Cancelled {
				t.Fatal("completion lost")
			}
		} else if status != Stopping {
			t.Fatal(status)
		}
	}
}

func TestDeadlineCancelsBackendWork(t *testing.T) {
	backend := &fakeBackend{closed: make(chan struct{}), execute: func(ctx context.Context, _ Request) Result {
		<-ctx.Done()
		return classify(ctx.Err())
	}}
	sink := newSink()
	session := New(backend, sink)
	if session.Submit(Request{ID: 1, Operation: Connect, Timeout: 10 * time.Millisecond}) != OK {
		t.Fatal("submit")
	}
	if receive(t, sink.completions).result.Status != TimedOut {
		t.Fatal("deadline did not reach backend")
	}
	session.Stop()
	receive(t, session.done)
}

type brokenClose struct{}

func (brokenClose) Execute(context.Context, Request) Result { return Result{Status: OK} }
func (brokenClose) Close()                                  { panic("injected teardown bug") }

func TestTeardownPanicDoesNotFabricateStoppedBarrier(t *testing.T) {
	sink := newSink()
	session := New(brokenClose{}, sink)
	session.Stop()
	message := receive(t, sink.diagnostics)
	if !strings.Contains(message, "shutdown panic=injected teardown bug") {
		t.Fatal(message)
	}
	select {
	case <-sink.stopped:
		t.Fatal("reported stopped despite incomplete teardown")
	case <-session.done:
		t.Fatal("released callback ownership despite incomplete teardown")
	default:
	}
}

func TestTypedOperationsValidateBeforeConnecting(t *testing.T) {
	key := make([]byte, 32)
	key[31] = 1
	backend, err := NewTesla("5YJ3E1EA7KF000001", "", key)
	if err != nil {
		t.Fatal(err)
	}
	clear(key)
	if len(backend.key.PublicBytes()) != 65 {
		t.Fatal("key depends on caller storage")
	}
	if result := backend.Execute(context.Background(), Request{Operation: NavigateGPS, Latitude: 100}); result.Status != Invalid {
		t.Fatal(result)
	}
	if result := backend.Execute(context.Background(), Request{Operation: NavigateAddress, Text: ""}); result.Status != Invalid {
		t.Fatal(result)
	}
	if result := backend.Execute(context.Background(), Request{Operation: BodyControllerState}); result.Status != NotConnected {
		t.Fatal(result)
	}
	if result := backend.Execute(context.Background(), Request{Operation: Disconnect}); result.Status != OK {
		t.Fatal(result)
	}
	if backend.bus != nil {
		t.Fatal("validation touched BlueZ")
	}
	backend.Close()
}
