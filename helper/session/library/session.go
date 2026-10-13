// Package library provides the asynchronous, in-process Tesla BLE boundary.
// Application policy (presence, retry schedules and file persistence) belongs
// to Rust. Backend methods execute one explicitly requested protocol operation.
package library

import (
	"context"
	"fmt"
	"runtime/debug"
	"sync"
	"time"
)

type Status uint32

const (
	OK Status = iota
	Invalid
	Busy
	Stopping
	InvalidHandle
	Failed
	Cancelled
	TimedOut
	InternalBug
	Uncertain
	NotConnected
	BluetoothOff
	AuthenticationFailed
)

type Operation uint32

const (
	Connect Operation = iota + 1
	Disconnect
	BodyControllerState
	NavigateGPS
	NavigateAddress
	WatchStart
	WatchStop
	PassiveEntry
	EnrollmentStatus
	VehicleCommand
	PublicKey
)

type Request struct {
	ID        uint64
	Operation Operation
	Domain    uint32 // 1 = VCSEC, 2 = infotainment (Connect only).
	Timeout   time.Duration
	Latitude  float64
	Longitude float64
	Text      string
	Target    uint64
	Command   uint32
	Values    []Value
}

type Value struct {
	Kind    uint32 // 0 omitted, 1 integer/enum/bool, 2 floating point, 3 bytes
	Integer uint64
	Number  float64
	Bytes   []byte
}

type Observation struct {
	Kind   uint32 // 1 advertisement, 2 disconnected, 3 authenticated, 4 auth failure, 5 watch failure
	Target uint64
	RSSI   int32
	Value  uint32
	Status Status
	Error  string
}

type observationSink interface{ Observe(Observation) }

type Result struct {
	Status  Status
	Payload []byte
	Error   string
}

type Backend interface {
	Execute(context.Context, Request) Result
	Close()
}

// Sink methods are serialized, may run on Go-managed threads, and must not
// block or call back into this session. Rust copies callback data immediately.
type Sink interface {
	Complete(uint64, Result)
	Diagnostic(string)
	Stopped()
}

type Session struct {
	mu       sync.Mutex
	backend  Backend
	sink     Sink
	ctx      context.Context
	cancel   context.CancelFunc
	active   uint64
	lastID   uint64
	opCancel context.CancelFunc
	stopping bool
	workers  sync.WaitGroup
	done     chan struct{}
	delivery sync.Mutex
}

func New(backend Backend, sink Sink) *Session {
	ctx, cancel := context.WithCancel(context.Background())
	s := &Session{backend: backend, sink: sink, ctx: ctx, cancel: cancel, done: make(chan struct{})}
	if backend, ok := backend.(interface {
		Bind(context.Context, func(Observation), func(string))
	}); ok {
		backend.Bind(ctx, func(event Observation) {
			s.delivery.Lock()
			defer s.delivery.Unlock()
			if sink, ok := sink.(observationSink); ok {
				sink.Observe(event)
			}
		}, func(message string) {
			s.delivery.Lock()
			s.sink.Diagnostic(message)
			s.delivery.Unlock()
			s.Stop()
		})
	}
	return s
}

// Submit copies the request by value. String storage belongs to Go by the time
// this method is called. One accepted operation is allowed at a time; Rust owns
// higher-level queuing. IDs must increase, so cancelled/late work cannot alias.
func (s *Session) Submit(req Request) Status {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.stopping {
		return Stopping
	}
	if req.ID == 0 || req.ID <= s.lastID || req.Timeout <= 0 || req.Operation < Connect || req.Operation > PublicKey {
		return Invalid
	}
	if s.active != 0 {
		return Busy
	}
	ctx, cancel := context.WithTimeout(s.ctx, req.Timeout)
	s.active, s.lastID, s.opCancel = req.ID, req.ID, cancel
	s.workers.Add(1) // Add under the same lock used by Stop before Wait.
	go s.run(ctx, cancel, req)
	return OK
}

func (s *Session) execute(ctx context.Context, req Request) (result Result) {
	defer func() {
		if value := recover(); value != nil {
			result = Result{Status: InternalBug, Error: "Go protocol worker panicked"}
			s.delivery.Lock()
			s.sink.Diagnostic(fmt.Sprintf("operation=%d request=%d panic=%v\n%s", req.Operation, req.ID, value, debug.Stack()))
			s.delivery.Unlock()
		}
	}()
	return s.backend.Execute(ctx, req)
}

func (s *Session) run(ctx context.Context, cancel context.CancelFunc, req Request) {
	defer s.workers.Done()
	defer cancel()
	result := s.execute(ctx, req)
	if result.Status == InternalBug {
		s.Stop() // Faulted state is never reused; no automatic retry.
	}
	s.delivery.Lock()
	s.mu.Lock()
	s.active, s.opCancel = 0, nil
	s.mu.Unlock()
	s.sink.Complete(req.ID, result)
	s.delivery.Unlock()
}

func (s *Session) Cancel(id uint64) Status {
	s.mu.Lock()
	defer s.mu.Unlock()
	if id == 0 || s.active != id {
		return Invalid
	}
	s.opCancel()
	return OK
}

// Stop is asynchronous and idempotent. No new work is accepted after it returns.
func (s *Session) Stop() {
	s.mu.Lock()
	if s.stopping {
		s.mu.Unlock()
		return
	}
	s.stopping = true
	s.cancel()
	s.mu.Unlock()
	go func() {
		s.workers.Wait()
		closed := false
		func() {
			defer func() {
				if value := recover(); value != nil {
					s.delivery.Lock()
					s.sink.Diagnostic(fmt.Sprintf("shutdown panic=%v\n%s", value, debug.Stack()))
					s.delivery.Unlock()
				}
			}()
			s.backend.Close()
			closed = true
		}()
		if !closed {
			// A teardown panic cannot prove resources/workers are gone. Keep
			// callback ownership alive instead of fabricating a stopped barrier.
			return
		}
		s.delivery.Lock()
		s.sink.Stopped()
		s.delivery.Unlock()
		close(s.done) // Barrier includes return from the terminal callback.
	}()
}

// Join is only used for final destruction, never from a callback or UI thread.
// A stuck backend intentionally keeps the context alive instead of reporting a
// false shutdown barrier and allowing use-after-free or conflicting sessions.
func (s *Session) Join() { <-s.done }

// Fault reports a bridge bug and retires this session. It never attempts to
// treat the panic as a recoverable vehicle/protocol result.
func (s *Session) Fault(message string) {
	s.delivery.Lock()
	s.sink.Diagnostic(message)
	s.delivery.Unlock()
	s.Stop()
}
