package bluez

import (
	"context"
	"testing"
	"time"

	"github.com/godbus/dbus"
)

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
		if err := w.ensureDiscovering(ctx); err != nil {
			t.Fatalf("ensureDiscovering #%d: %v", i, err)
		}
	}
	if n := countCalls(bus.calls, adapterIface+".StartDiscovery"); n != 1 {
		t.Errorf("StartDiscovery called %d times across repeated checks, want 1 (a live discovery must not be restarted)", n)
	}

	w.Stop(ctx)
	if bus.discovering {
		t.Error("expected Stop to stop discovery")
	}
}

func TestWatcherIgnoresCachedRSSIAndDeliversAdvertisementSignals(t *testing.T) {
	bus := newFakeBluez()
	vin := "5YJ3E1EA0PF000000"
	bus.dev = &fakeDevice{path: bus.devPath(), name: vehicleBeaconName(vin), rssi: -55}
	bus.deviceVisible = true
	w, err := newWatcher(context.Background(), bus, "", vin)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Stop(context.Background())
	// A cached RSSI property seeds identity only; it is not an advertisement.
	quiet, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	res, err := w.Wait(quiet)
	cancel()
	if err != nil || res != nil {
		t.Fatalf("cached snapshot delivered as advertisement: %+v, %v", res, err)
	}
	// Separate channel: watching RSSI must never consume the GATT rxLoop's
	// copy of a signal, even while both subscribers are registered.
	gattSignals := make(chan *dbus.Signal, 16)
	bus.subscribeSignals(gattSignals)
	defer bus.unsubscribeSignals(gattSignals)
	sig := &dbus.Signal{
		Name: propsIface + ".PropertiesChanged",
		Path: bus.devPath(),
		Body: []interface{}{deviceIface, map[string]dbus.Variant{"RSSI": dbus.MakeVariant(int16(-54))}, []string{}},
	}
	bus.emitSignal(sig)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	res, err = w.Wait(ctx)
	if err != nil || res == nil || res.RSSI != -54 || !res.HasRSSI {
		t.Fatalf("RSSI PropertiesChanged not delivered: %+v, %v", res, err)
	}
	select {
	case got := <-gattSignals:
		if got != sig {
			t.Fatal("shared GATT signal was replaced")
		}
	default:
		t.Fatal("watcher stole GATT signal")
	}
}

// An advertisement whose signal omits RSSI is completed by a property read
// that must not run on the signal loop: a blocked read would stall (and,
// with godbus, reorder) every later signal.
func TestWatcherReadsMissingRSSIOffTheSignalLoop(t *testing.T) {
	bus := newFakeBluez()
	vin := "5YJ3E1EA0PF000000"
	bus.dev = &fakeDevice{path: bus.devPath(), name: vehicleBeaconName(vin), rssi: -61}
	bus.deviceVisible = true
	w, err := newWatcher(context.Background(), bus, "", vin)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Stop(context.Background())
	bus.emitSignal(&dbus.Signal{
		Name: propsIface + ".PropertiesChanged",
		Path: bus.devPath(),
		Body: []interface{}{deviceIface, map[string]dbus.Variant{"ManufacturerData": dbus.MakeVariant(map[uint16]dbus.Variant{})}, []string{}},
	})
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	res, err := w.Wait(ctx)
	if err != nil || res == nil || res.RSSI != -61 {
		t.Fatalf("advertisement without RSSI not completed by a property read: %+v, %v", res, err)
	}
}

func TestWatcherRestartsDiscoveryWhenDropped(t *testing.T) {
	bus := newFakeBluez()
	vin := "5YJ3E1EA0PF000000"
	bus.dev = &fakeDevice{path: bus.devPath(), name: vehicleBeaconName(vin)}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	w, err := newWatcher(ctx, bus, "", vin)
	if err != nil {
		t.Fatalf("newWatcher: %v", err)
	}
	defer w.Stop(ctx)

	bus.discovering = false
	if err := w.ensureDiscovering(ctx); err != nil {
		t.Fatalf("ensureDiscovering after discovery dropped: %v", err)
	}
	if !bus.discovering {
		t.Error("expected discovery to restart after bluetoothd dropped it")
	}
	if n := countCalls(bus.calls, adapterIface+".StartDiscovery"); n != 2 {
		t.Errorf("StartDiscovery called %d times, want 2 (once at Watch, once after drop)", n)
	}
}

func TestWatcherRepowersAdapter(t *testing.T) {
	bus := newFakeBluez()
	vin := "5YJ3E1EA0PF000000"
	bus.dev = &fakeDevice{path: bus.devPath(), name: vehicleBeaconName(vin)}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	w, err := newWatcher(ctx, bus, "", vin)
	if err != nil {
		t.Fatalf("newWatcher: %v", err)
	}
	defer w.Stop(ctx)

	bus.powered = false
	bus.discovering = false
	if err := w.ensureDiscovering(ctx); err != nil {
		t.Fatalf("ensureDiscovering after adapter powered off: %v", err)
	}
	if !bus.powered {
		t.Error("expected the adapter to be powered back on")
	}
	if !bus.discovering {
		t.Error("expected discovery to restart after powering the adapter")
	}
}

func TestWatcherWaitFindsDelayedBeacon(t *testing.T) {
	bus := newFakeBluez()
	vin := "5YJ3E1EA0PF000000"
	bus.dev = &fakeDevice{path: bus.devPath(), name: vehicleBeaconName(vin), rssi: -50}
	bus.deviceVisible = false

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	w, err := newWatcher(ctx, bus, "", vin)
	if err != nil {
		t.Fatalf("newWatcher: %v", err)
	}
	defer w.Stop(ctx)

	bus.emitSignal(&dbus.Signal{
		Name: objMgrIface + ".InterfacesAdded", Path: "/",
		Body: []interface{}{bus.devPath(), map[string]map[string]dbus.Variant{
			deviceIface: {"Name": dbus.MakeVariant(bus.dev.name), "RSSI": dbus.MakeVariant(int16(-50))},
		}},
	})
	res, err := w.Wait(ctx)
	if err != nil {
		t.Fatalf("Wait: %v", err)
	}
	if res == nil {
		t.Fatal("Wait returned nil after the beacon appeared")
	}
	if res.RSSI != -50 {
		t.Errorf("res.RSSI = %d, want -50", res.RSSI)
	}
}

func TestWatcherWaitTimeoutIsNotAnError(t *testing.T) {
	bus := newFakeBluez()
	vin := "5YJ3E1EA0PF000000"
	bus.dev = &fakeDevice{path: bus.devPath(), name: vehicleBeaconName(vin)}
	bus.deviceVisible = false

	wctx, wcancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer wcancel()
	w, err := newWatcher(wctx, bus, "", vin)
	if err != nil {
		t.Fatalf("newWatcher: %v", err)
	}
	defer w.Stop(wctx)

	waitCtx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	res, err := w.Wait(waitCtx)
	if err != nil {
		t.Fatalf("Wait on timeout: %v", err)
	}
	if res != nil {
		t.Fatalf("Wait should return nil when no beacon appears, got %+v", res)
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
