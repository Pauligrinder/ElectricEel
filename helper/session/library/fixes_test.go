package library

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/teslamotors/vehicle-command/pkg/vehicle"
)

// DRIVE AuthenticationRequest as a bare FromVCSECMessage (see
// internal/phoneprotocol/auth_test.go for the wire layout).
var driveRequest = []byte{0x1a, 0x0a, 0x12, 0x06, 0x0a, 0x04, 0x00, 0x01, 0x0f, 0x2c, 0x18, 0x02}

// The RX-loop tap must not queue ordinary command responses (they used to
// fill the inbox and fault the session as an "auth failure"), must keep only
// the newest request, and must never block GATT reassembly.
func TestAuthTapQueuesOnlyTheNewestRealRequest(t *testing.T) {
	var passive atomic.Bool
	passive.Store(true)
	requests := make(chan int, 1)
	var logged int
	tap := authTap(context.Background(), &passive, requests, func(string, ...interface{}) { logged++ })

	for i := 0; i < 100; i++ {
		tap([]byte{0x0a, 0x00}) // not an AuthenticationRequest
	}
	if len(requests) != 0 {
		t.Fatal("ordinary inbound datagram queued as an authentication request")
	}
	done := make(chan struct{})
	go func() {
		tap(driveRequest)
		tap(driveRequest) // must replace, not block
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("tap blocked the RX loop on a full queue")
	}
	if len(requests) != 1 || logged != 1 {
		t.Fatalf("queued=%d replaced-logs=%d, want 1/1", len(requests), logged)
	}
	passive.Store(false)
	<-requests
	tap(driveRequest)
	if len(requests) != 0 {
		t.Fatal("request queued while passive entry is disabled")
	}
}

// A command on a link the observer saw drop reports NotConnected and never
// opens a new connection behind Rust's back.
func TestCommandOnDroppedLinkDoesNotReconnect(t *testing.T) {
	scalar := make([]byte, 32)
	scalar[31] = 1
	backend, err := NewTesla("5YJ3E1EA7KF000001", "", scalar)
	if err != nil {
		t.Fatal(err)
	}
	link := &replyingConnector{inbox: make(chan []byte, 8)}
	car, err := vehicle.NewVehicle(link, backend.key, nil)
	if err != nil {
		t.Fatal(err)
	}
	backend.conn, backend.car = link, car
	backend.linkActive.Store(false) // dropped
	honk := Request{ID: 1, Operation: VehicleCommand, Command: commandID(t, "honk")}
	result := backend.Execute(context.Background(), honk)
	if result.Status != NotConnected {
		t.Fatalf("status %d (%s), want NotConnected", result.Status, result.Error)
	}
	if backend.bus != nil || backend.car != nil || !link.isClosed() {
		t.Fatal("dropped link was reused or a reconnect was attempted")
	}
}

// A watcher goroutine that exited after a failure is released so WatchStart
// can install a working watcher instead of reporting OK for a dead one.
func TestFailedWatcherIsReaped(t *testing.T) {
	backend := &Tesla{}
	done := make(chan struct{})
	close(done) // goroutine already exited
	backend.watchDone = done
	backend.watchCancel = func() {}
	backend.watcher = nil // the dead watcher already released its BlueZ state
	backend.reapWatch()
	if backend.watchCancel != nil || backend.watchDone != nil {
		t.Fatal("dead watcher left installed")
	}
}

func commandID(t *testing.T, name string) uint32 {
	t.Helper()
	for i, candidate := range commandNames {
		if candidate == name {
			return uint32(i + 1)
		}
	}
	t.Fatalf("unknown command %s", name)
	return 0
}
