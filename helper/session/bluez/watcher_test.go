package bluez

import (
	"context"
	"testing"
	"time"
)

func TestWatcherPeekTracksBeaconVisibility(t *testing.T) {
	bus := newFakeBluez()
	vin := "5YJ3E1EA0PF000000"
	bus.dev = &fakeDevice{path: bus.devPath(), name: vehicleBeaconName(vin), rssi: -60}
	bus.deviceVisible = false

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	w, err := newWatcher(ctx, bus, "", vin)
	if err != nil {
		t.Fatalf("newWatcher: %v", err)
	}
	defer w.Stop(ctx)

	res, err := w.Peek(ctx)
	if err != nil {
		t.Fatalf("Peek (not yet visible): %v", err)
	}
	if res != nil {
		t.Fatalf("Peek returned a result before the beacon appeared: %+v", res)
	}

	bus.deviceVisible = true
	res, err = w.Peek(ctx)
	if err != nil {
		t.Fatalf("Peek (visible): %v", err)
	}
	if res == nil {
		t.Fatal("Peek returned nil after the beacon appeared")
	}
	if res.RSSI != -60 {
		t.Errorf("res.RSSI = %d, want -60", res.RSSI)
	}

	bus.deviceVisible = false
	res, err = w.Peek(ctx)
	if err != nil {
		t.Fatalf("Peek (departed): %v", err)
	}
	if res != nil {
		t.Fatalf("Peek returned a result after the beacon disappeared: %+v", res)
	}
}

func TestWatcherStartsDiscoveryOnceAndStopStopsIt(t *testing.T) {
	bus := newFakeBluez()
	vin := "5YJ3E1EA0PF000000"
	bus.dev = &fakeDevice{path: bus.devPath(), name: vehicleBeaconName(vin)}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	w, err := newWatcher(ctx, bus, "", vin)
	if err != nil {
		t.Fatalf("newWatcher: %v", err)
	}
	if !bus.discovering {
		t.Error("expected newWatcher to start discovery")
	}

	for i := 0; i < 5; i++ {
		if _, err := w.Peek(ctx); err != nil {
			t.Fatalf("Peek #%d: %v", i, err)
		}
	}
	if n := countCalls(bus.calls, adapterIface+".StartDiscovery"); n != 1 {
		t.Errorf("StartDiscovery called %d times across repeated Peek, want 1 (Peek must not restart discovery)", n)
	}

	w.Stop(ctx)
	if bus.discovering {
		t.Error("expected Stop to stop discovery")
	}
}

func TestWatcherHonorsSpecificAdapter(t *testing.T) {
	bus := newFakeBluez()
	vin := "5YJ3E1EA0PF000000"

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	if _, err := newWatcher(ctx, bus, "hci9", vin); err == nil {
		t.Fatal("newWatcher with nonexistent adapter should fail")
	}
}
