package bluez

import (
	"context"
	"testing"
	"time"

	"github.com/godbus/dbus"
)

func eventWatcher(t *testing.T) (*fakeBluez, *Watcher) {
	t.Helper()
	bus := newFakeBluez()
	vin := "5YJ3E1EA0PF000000"
	bus.dev = &fakeDevice{path: bus.devPath(), name: vehicleBeaconName(vin), rssi: -55}
	bus.deviceVisible = true
	w, err := newWatcher(context.Background(), bus, "", vin)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { w.Stop(context.Background()) })
	return bus, w
}

func emitDeviceProperties(bus *fakeBluez, props map[string]dbus.Variant) {
	bus.emitSignal(&dbus.Signal{
		Name: propsIface + ".PropertiesChanged", Path: bus.devPath(),
		Body: []interface{}{deviceIface, props, []string{}},
	})
}

func TestEventWaitIgnoresCachedRSSIAndDoesNotPollObjects(t *testing.T) {
	bus, w := eventWatcher(t)
	before := bus.managedCalls
	emitDeviceProperties(bus, map[string]dbus.Variant{"Connected": dbus.MakeVariant(false)})
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	result, err := w.Wait(ctx)
	if err != nil || result != nil {
		t.Fatalf("cached RSSI/non-advertisement event triggered arrival: %v, %v", result, err)
	}
	if bus.managedCalls != before {
		t.Fatal("idle Wait polled the BlueZ object tree")
	}
	if !bus.discovering {
		t.Fatal("away scanning must remain continuous")
	}
}

func TestEventWaitAcceptsRSSIOnlyForKnownTarget(t *testing.T) {
	bus, w := eventWatcher(t)
	emitDeviceProperties(bus, map[string]dbus.Variant{"RSSI": dbus.MakeVariant(int16(-49))})
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	result, err := w.Wait(ctx)
	if err != nil || result == nil || result.RSSI != -49 {
		t.Fatalf("RSSI-only update: %v, %v", result, err)
	}
}

func TestEventWaitAcceptsAliasAndAdvertisementWithoutRSSI(t *testing.T) {
	bus, w := eventWatcher(t)
	emitDeviceProperties(bus, map[string]dbus.Variant{
		"Name": dbus.MakeVariant("address"), "Alias": dbus.MakeVariant(bus.dev.name),
		"ManufacturerData": dbus.MakeVariant(map[uint16]dbus.Variant{1: dbus.MakeVariant([]byte{1})}),
	})
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	result, err := w.Wait(ctx)
	if err != nil || result == nil || result.RSSI != -55 {
		t.Fatalf("Alias/advertisement update: %v, %v", result, err)
	}
}

func TestEventPauseDiscardsOldAdvertisementsAndResumeWaitsForNew(t *testing.T) {
	bus, w := eventWatcher(t)
	emitDeviceProperties(bus, map[string]dbus.Variant{"RSSI": dbus.MakeVariant(int16(-49))})
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if result, err := w.Wait(ctx); err != nil || result == nil {
		t.Fatalf("initial advertisement: %v, %v", result, err)
	}
	w.Pause(context.Background())
	if bus.discovering {
		t.Fatal("Pause left discovery on during GATT")
	}
	w.Resume()
	short, stop := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer stop()
	if result, err := w.Wait(short); err != nil || result != nil {
		t.Fatalf("Resume reused a prior advertisement: %v, %v", result, err)
	}
	if !bus.discovering {
		t.Fatal("Resume did not restart continuous discovery")
	}
	emitDeviceProperties(bus, map[string]dbus.Variant{"RSSI": dbus.MakeVariant(int16(-51))})
	if result, err := w.Wait(ctx); err != nil || result == nil || result.RSSI != -51 {
		t.Fatalf("fresh advertisement after resume: %v, %v", result, err)
	}
}

func TestEventWaitRejectsExpiredPendingAdvertisement(t *testing.T) {
	_, w := eventWatcher(t)
	w.signalMu.Lock()
	w.pending = &ScanResult{Path: w.devicePath, HasRSSI: true, RSSI: -50}
	w.pendingAt = time.Now().Add(-time.Minute)
	w.signalMu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	if result, err := w.Wait(ctx); err != nil || result != nil {
		t.Fatalf("expired advertisement triggered connection: %v, %v", result, err)
	}
}

func TestEventWaitStopsWhenSignalChannelCloses(t *testing.T) {
	_, w := eventWatcher(t)
	close(w.signalCh)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if _, err := w.Wait(ctx); err == nil {
		t.Fatal("closed signal channel must report failure rather than spin")
	}
	w.Stop(context.Background()) // cleanup is idempotent
}
