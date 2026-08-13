package bluez

import (
	"bytes"
	"context"
	"testing"
	"time"

	"github.com/teslamotors/vehicle-command/pkg/connector"
)

// newTestConnection returns a Connection wired to the fake, primed for
// Send/RX unit tests (no rxLoop goroutine; rx/framing is exercised directly).
func newTestConnection(bus *fakeBluez) *Connection {
	return &Connection{
		vin:         "5YJ3E1EA0PF000000",
		bus:         bus,
		devPath:     bus.devPath(),
		svcPath:     bus.svcPath(),
		txPath:      bus.txPath(),
		rxPath:      bus.rxPath(),
		inbox:       make(chan []byte, connector.BufferSize),
		blockLength: maxExpectedMTU - 3,
		done:        make(chan struct{}),
	}
}

func TestConnectFromScanResult(t *testing.T) {
	bus := newFakeBluez()
	vin := "5YJ3E1EA0PF000000"
	bus.dev = &fakeDevice{path: bus.devPath(), name: vehicleBeaconName(vin)}
	bus.deviceVisible = true
	bus.servicesResolved = true
	bus.gattReady = true

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	cc, err := connect(ctx, bus, "hci0", vin, &ScanResult{Path: bus.dev.path})
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	c, ok := cc.(*Connection)
	if !ok {
		t.Fatalf("connect returned %T, want *Connection", cc)
	}
	if !bus.connected {
		t.Error("expected Device1.Connect to have been called")
	}
	if !bus.startedNotify {
		t.Error("expected StartNotify on the RX characteristic")
	}
	if c.blockLength != maxExpectedMTU-3 {
		t.Errorf("blockLength = %d, want %d", c.blockLength, maxExpectedMTU-3)
	}
	if c.VIN() != vin {
		t.Errorf("VIN() = %q, want %q", c.VIN(), vin)
	}
	if bus.matches != 1 {
		t.Errorf("match rules registered = %d, want 1", bus.matches)
	}
}

func TestConnectScansWhenNoTarget(t *testing.T) {
	bus := newFakeBluez()
	vin := "5YJ3E1EA0PF000000"
	bus.dev = &fakeDevice{path: bus.devPath(), name: vehicleBeaconName(vin)}
	bus.deviceVisible = true
	bus.servicesResolved = true
	bus.gattReady = true

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	if _, err := connect(ctx, bus, "", vin, nil); err != nil {
		t.Fatalf("connect(no target): %v", err)
	}
	if !hasCall(bus.calls, adapterIface+".StartDiscovery") {
		t.Error("expected a scan to run when no target was given")
	}
}

func TestConnectFailsWhenDeviceNeverShowsUp(t *testing.T) {
	bus := newFakeBluez()
	vin := "5YJ3E1EA0PF000000"
	bus.dev = &fakeDevice{path: bus.devPath(), name: vehicleBeaconName(vin)}
	bus.deviceVisible = false

	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()

	if _, err := connect(ctx, bus, "hci0", vin, &ScanResult{Path: bus.devPath()}); err == nil {
		t.Fatal("expected connect to fail when the device is not in the object tree")
	}
}

func TestConnectDeliversNotifications(t *testing.T) {
	bus := newFakeBluez()
	vin := "5YJ3E1EA0PF000000"
	bus.dev = &fakeDevice{path: bus.devPath(), name: vehicleBeaconName(vin)}
	bus.deviceVisible = true
	bus.servicesResolved = true
	bus.gattReady = true

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	cc, err := connect(ctx, bus, "hci0", vin, &ScanResult{Path: bus.dev.path})
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	c := cc.(*Connection)

	// A single notification carrying one length-prefixed message.
	framed := []byte{0x00, 0x03, 'f', 'o', 'o'}
	bus.notify(c.rxPath, framed)

	select {
	case msg := <-c.Receive():
		if string(msg) != "foo" {
			t.Errorf("received %q, want %q", msg, "foo")
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for RX datagram")
	}
}

func TestRXFramingAcrossChunks(t *testing.T) {
	c := newTestConnection(newFakeBluez())

	// Message "hello world" (11 bytes) framed as [0x00,0x0b,...], delivered
	// in awkward split chunks across multiple rx() calls.
	frame := append([]byte{0x00, 0x0b}, []byte("hello world")...)
	split1, split2, split3 := frame[:3], frame[3:8], frame[8:]

	c.rx(split1)
	c.rx(split2)
	c.rx(split3)

	select {
	case msg := <-c.Receive():
		if string(msg) != "hello world" {
			t.Errorf("received %q, want %q", msg, "hello world")
		}
	default:
		t.Fatal("expected a reassembled datagram after all chunks arrived")
	}
}

func TestRXRejectsOversizedLength(t *testing.T) {
	c := newTestConnection(newFakeBluez())

	// Length prefix claims 0xFFFF bytes (> maxMessageSize); must reset the
	// buffer rather than allocate.
	c.rx([]byte{0xff, 0xff})
	c.rx([]byte("some trailing bytes"))

	select {
	case msg := <-c.Receive():
		t.Errorf("received %q after oversized length prefix, want nothing", msg)
	default:
	}
}

func TestSendFramesAndChunks(t *testing.T) {
	bus := newFakeBluez()
	c := newTestConnection(bus)

	payload := bytes.Repeat([]byte{0xAB}, 1000)
	if err := c.Send(context.Background(), payload); err != nil {
		t.Fatalf("Send: %v", err)
	}

	// Reassemble the captured chunks and verify framing matches upstream.
	var got []byte
	for _, w := range bus.writes {
		got = append(got, w...)
	}
	want := append([]byte{0x03, 0xe8}, payload...) // 1000 = 0x03e8
	if !bytes.Equal(got, want) {
		t.Errorf("framed output mismatch: got %d bytes, want %d", len(got), len(want))
	}
	// Chunks other than the final one must be exactly blockLength.
	maxChunk := maxExpectedMTU - 3
	for i, w := range bus.writes {
		if i < len(bus.writes)-1 && len(w) != maxChunk {
			t.Errorf("chunk %d is %d bytes, want %d", i, len(w), maxChunk)
		}
	}
}

func TestSendShrinksBlockLengthOnMtuError(t *testing.T) {
	bus := newFakeBluez()
	bus.failLargeWrites = true // remote only supports ATT MTU 23
	c := newTestConnection(bus)

	payload := bytes.Repeat([]byte{0x42}, 500)
	if err := c.Send(context.Background(), payload); err != nil {
		t.Fatalf("Send: %v", err)
	}

	// After the shrink, every captured write must fit the guaranteed size.
	for i, w := range bus.writes {
		if len(w) > defaultMTU-3 {
			t.Errorf("write %d is %d bytes, want <= %d (must shrink on MTU error)", i, len(w), defaultMTU-3)
		}
	}
	if c.blockLength != defaultMTU-3 {
		t.Errorf("blockLength after shrink = %d, want %d", c.blockLength, defaultMTU-3)
	}

	// Reassembled payload must still match framing + content.
	var got []byte
	for _, w := range bus.writes {
		got = append(got, w...)
	}
	want := append([]byte{0x01, 0xf4}, payload...) // 500 = 0x01f4
	if !bytes.Equal(got, want) {
		t.Errorf("framed output after shrink mismatch: got %d bytes, want %d", len(got), len(want))
	}
}

func TestCloseIsIdempotent(t *testing.T) {
	bus := newFakeBluez()
	c := newTestConnection(bus)
	bus.connected = true

	c.Close()
	c.Close() // must be a no-op, not a panic

	if !bus.stoppedNotify {
		t.Error("expected StopNotify on Close")
	}
	if bus.connected {
		t.Error("expected Disconnect on Close")
	}
	if bus.matches != 0 {
		t.Errorf("match rule not removed: matches = %d", bus.matches)
	}
	if !bus.removedMatch {
		t.Error("expected RemoveMatch on Close")
	}
	// The closed channel must have stopped the RX loop; rx() on a closed
	// connection is an internal invariant (Close is terminal).
	select {
	case _, ok := <-c.done:
		if ok {
			t.Error("done channel should be closed")
		}
	case <-time.After(100 * time.Millisecond):
		t.Error("done channel should be closed after Close")
	}
}
