package bluez

import (
	"bytes"
	"sync"
	"testing"
	"time"
)

func TestInboundObserverSeesCopyWithoutStealingFromReceive(t *testing.T) {
	c := newTestConnection(newFakeBluez())

	var (
		mu       sync.Mutex
		observed [][]byte
	)
	c.SetInboundObserver(func(dg []byte) {
		mu.Lock()
		observed = append(observed, append([]byte(nil), dg...))
		mu.Unlock()
		// Mutate the observer's slice: must not affect Receive().
		for i := range dg {
			dg[i] = 0xff
		}
	})

	payload := []byte("auth-tap")
	frame := append([]byte{0x00, byte(len(payload))}, payload...)
	c.rx(frame)

	select {
	case msg := <-c.Receive():
		if !bytes.Equal(msg, payload) {
			t.Fatalf("Receive() = %q, want %q (observer must not steal/corrupt)", msg, payload)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for Receive()")
	}

	mu.Lock()
	defer mu.Unlock()
	if len(observed) != 1 {
		t.Fatalf("observer calls = %d, want 1", len(observed))
	}
	if !bytes.Equal(observed[0], payload) {
		t.Fatalf("observer saw %q, want %q", observed[0], payload)
	}
}

func TestInboundObserverNilClearsTap(t *testing.T) {
	c := newTestConnection(newFakeBluez())
	called := false
	c.SetInboundObserver(func([]byte) { called = true })
	c.SetInboundObserver(nil)

	c.rx(append([]byte{0x00, 0x01}, 'x'))
	select {
	case <-c.Receive():
	default:
		t.Fatal("expected Receive() delivery after clearing observer")
	}
	if called {
		t.Fatal("cleared observer must not be invoked")
	}
}
