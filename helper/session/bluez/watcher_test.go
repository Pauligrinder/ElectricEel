package bluez

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/godbus/dbus"
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
	if bus.matches != 2 {
		t.Errorf("signal matches = %d, want 2", bus.matches)
	}

	for i := 0; i < 5; i++ {
		if _, err := w.Peek(ctx); err != nil {
			t.Fatalf("Peek #%d: %v", i, err)
		}
	}
	if n := countCalls(bus.calls, adapterIface+".StartDiscovery"); n != 1 {
		t.Errorf("StartDiscovery called %d times across repeated Peek, want 1 (Peek must not restart a live discovery)", n)
	}

	w.Stop(ctx)
	if bus.discovering {
		t.Error("expected Stop to stop discovery")
	}
	if bus.removedMatches != 2 {
		t.Errorf("removed signal matches = %d, want 2", bus.removedMatches)
	}
}

func TestWatcherCleansUpFirstMatchWhenSecondMatchFails(t *testing.T) {
	bus := newFakeBluez()
	bus.addMatchErrAt = 2
	vin := "5YJ3E1EA0PF000000"

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if _, err := newWatcher(ctx, bus, "", vin); err == nil {
		t.Fatal("newWatcher succeeded despite signal-match failure")
	}
	if bus.removedMatches != 1 {
		t.Errorf("removed signal matches = %d, want 1", bus.removedMatches)
	}
	if bus.discovering {
		t.Fatal("discovery started after signal-match failure")
	}
}

func TestWatcherCleansUpMatchesWhenDiscoveryStartFails(t *testing.T) {
	bus := newFakeBluez()
	bus.startDiscoveryErr = context.DeadlineExceeded
	vin := "5YJ3E1EA0PF000000"

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if _, err := newWatcher(ctx, bus, "", vin); err == nil {
		t.Fatal("newWatcher succeeded despite discovery-start failure")
	}
	if bus.removedMatches != 2 {
		t.Errorf("removed signal matches = %d, want 2", bus.removedMatches)
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
	if _, err := w.Peek(ctx); err != nil {
		t.Fatalf("Peek after discovery dropped: %v", err)
	}
	if !bus.discovering {
		t.Error("expected Peek to start discovery again after bluetoothd dropped it")
	}
	if n := countCalls(bus.calls, adapterIface+".StartDiscovery"); n != 2 {
		t.Errorf("StartDiscovery called %d times, want 2 (once at Watch, once after drop)", n)
	}
}

func TestWatcherPeekFailsWhenAdapterOff(t *testing.T) {
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
	if _, err := w.Peek(ctx); err == nil {
		t.Fatal("Peek must fail when the adapter is off")
	} else if !strings.Contains(err.Error(), "adapter not powered") {
		t.Fatalf("Peek error %q, want adapter not powered", err)
	}
	if bus.powered {
		t.Error("Peek must not Set Powered (Sailfish ConnMan AuthFailed)")
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

	go func() {
		time.Sleep(20 * time.Millisecond)
		bus.advertiseAdded()
	}()
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

func TestWatcherWaitUsesRSSIUpdateForCachedTarget(t *testing.T) {
	bus := newFakeBluez()
	vin := "5YJ3E1EA0PF000000"
	bus.dev = &fakeDevice{path: bus.devPath(), name: vehicleBeaconName(vin), omitRSSI: true}
	bus.deviceVisible = true

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	w, err := newWatcher(ctx, bus, "", vin)
	if err != nil {
		t.Fatalf("newWatcher: %v", err)
	}
	defer w.Stop(ctx)

	bus.advertiseRSSI(-62)
	res, err := w.Wait(ctx)
	if err != nil {
		t.Fatalf("Wait: %v", err)
	}
	if res == nil || res.Path != bus.dev.path || res.RSSI != -62 || !res.HasRSSI {
		t.Fatalf("Wait returned %+v, want fresh RSSI update for cached target", res)
	}
}

func TestWatcherWaitIgnoresCachedDeviceWithoutRSSI(t *testing.T) {
	bus := newFakeBluez()
	vin := "5YJ3E1EA0PF000000"
	bus.dev = &fakeDevice{path: bus.devPath(), name: vehicleBeaconName(vin), omitRSSI: true}
	bus.deviceVisible = true

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
		t.Fatalf("Wait: %v", err)
	}
	if res != nil {
		t.Fatalf("Wait should not return a cached Device1 without RSSI, got %+v", res)
	}
}

func TestWatcherForgetStaleRemovesLeftoverWithoutRSSI(t *testing.T) {
	bus := newFakeBluez()
	vin := "5YJ3E1EA0PF000000"
	bus.dev = &fakeDevice{path: bus.devPath(), name: vehicleBeaconName(vin), omitRSSI: true}
	bus.deviceVisible = true

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	w, err := newWatcher(ctx, bus, "", vin)
	if err != nil {
		t.Fatalf("newWatcher: %v", err)
	}
	defer w.Stop(ctx)

	if !w.ForgetStale(ctx) {
		t.Fatal("expected ForgetStale to RemoveDevice a leftover without RSSI")
	}
	if bus.removeDeviceN == 0 {
		t.Fatal("ForgetStale must call RemoveDevice")
	}
	if w.devicePath != "" {
		t.Fatalf("devicePath = %q after ForgetStale, want empty", w.devicePath)
	}
}

func TestWatcherForgetStaleLeavesLiveAdvertisement(t *testing.T) {
	bus := newFakeBluez()
	vin := "5YJ3E1EA0PF000000"
	bus.dev = &fakeDevice{path: bus.devPath(), name: vehicleBeaconName(vin), rssi: -52}
	bus.deviceVisible = true

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	w, err := newWatcher(ctx, bus, "", vin)
	if err != nil {
		t.Fatalf("newWatcher: %v", err)
	}
	defer w.Stop(ctx)

	if w.ForgetStale(ctx) {
		t.Fatal("ForgetStale must not RemoveDevice a live RSSI beacon")
	}
	if bus.removeDeviceN != 0 {
		t.Fatal("live advertisement must stay paired")
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

func TestWatcherWaitDoesNotPollManagedObjects(t *testing.T) {
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
	initialCalls := bus.managedCalls

	waitCtx, cancel := context.WithTimeout(context.Background(), 250*time.Millisecond)
	defer cancel()
	if res, err := w.Wait(waitCtx); err != nil || res != nil {
		t.Fatalf("Wait = (%+v, %v), want (nil, nil)", res, err)
	}
	if extra := bus.managedCalls - initialCalls; extra != 0 {
		t.Errorf("GetManagedObjects calls during idle Wait = %d, want 0", extra)
	}
}

func TestWatcherWaitRestartsDroppedDiscoverySignal(t *testing.T) {
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

	bus.discoveryChanged(false)
	bus.advertiseAdded()
	res, err := w.Wait(ctx)
	if err != nil {
		t.Fatalf("Wait: %v", err)
	}
	if res == nil {
		t.Fatal("Wait did not return the advertisement after restarting discovery")
	}
	if !bus.discovering {
		t.Fatal("Wait did not restart dropped discovery")
	}
	if n := countCalls(bus.calls, adapterIface+".StartDiscovery"); n != 2 {
		t.Errorf("StartDiscovery called %d times, want 2", n)
	}
}

func TestWatcherWaitIgnoresCachedRSSIUntilFreshSignal(t *testing.T) {
	bus := newFakeBluez()
	vin := "5YJ3E1EA0PF000000"
	bus.dev = &fakeDevice{path: bus.devPath(), name: vehicleBeaconName(vin), rssi: -55}
	bus.deviceVisible = true

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
		t.Fatalf("Wait: %v", err)
	}
	if res != nil {
		t.Fatalf("Wait returned cached RSSI %+v; that triggers a GATT connect to a stale Device1", res)
	}
}

func TestWatcherWaitIgnoresNonAdvertisementPropertyChange(t *testing.T) {
	bus := newFakeBluez()
	vin := "5YJ3E1EA0PF000000"
	bus.dev = &fakeDevice{path: bus.devPath(), name: vehicleBeaconName(vin), rssi: -55}
	bus.deviceVisible = true

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	w, err := newWatcher(ctx, bus, "", vin)
	if err != nil {
		t.Fatalf("newWatcher: %v", err)
	}
	defer w.Stop(ctx)

	bus.advertiseProps(map[string]dbus.Variant{"Connected": dbus.MakeVariant(false)})
	waitCtx, waitCancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer waitCancel()
	res, err := w.Wait(waitCtx)
	if err != nil {
		t.Fatalf("Wait: %v", err)
	}
	if res != nil {
		t.Fatalf("Wait treated Connected change as a live beacon: %+v", res)
	}
}

func TestWatcherPauseStopsDiscovery(t *testing.T) {
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
	if !bus.discovering {
		t.Fatal("expected discovery after newWatcher")
	}

	w.Pause()
	if bus.discovering {
		t.Fatal("Pause must stop discovery before Device.Connect")
	}
	bus.discovering = false
	if _, err := w.Peek(ctx); err != nil {
		t.Fatalf("Peek while paused: %v", err)
	}
	if bus.discovering {
		t.Fatal("Peek must not restart discovery while paused")
	}

	w.Resume()
	if _, err := w.Peek(ctx); err != nil {
		t.Fatalf("Peek after Resume: %v", err)
	}
	if !bus.discovering {
		t.Fatal("Resume+Peek must restart discovery")
	}
}

func TestWatcherRecycleDiscoveryStopsAndStarts(t *testing.T) {
	bus := newFakeBluez()
	vin := "5YJ3E1EA0PF000000"
	bus.dev = &fakeDevice{path: bus.devPath(), name: vehicleBeaconName(vin), rssi: -94}
	bus.deviceVisible = true

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	w, err := newWatcher(ctx, bus, "", vin)
	if err != nil {
		t.Fatalf("newWatcher: %v", err)
	}
	defer w.Stop(ctx)
	if !bus.discovering {
		t.Fatal("expected discovery after newWatcher")
	}

	bus.advertiseRSSI(-94)
	if err := w.RecycleDiscovery(ctx); err != nil {
		t.Fatalf("RecycleDiscovery: %v", err)
	}
	if !bus.discovering {
		t.Fatal("RecycleDiscovery must leave discovery running")
	}
	if n := countCalls(bus.calls, adapterIface+".StopDiscovery"); n == 0 {
		t.Fatal("RecycleDiscovery must StopDiscovery")
	}
	if n := countCalls(bus.calls, adapterIface+".StartDiscovery"); n < 2 {
		t.Fatalf("StartDiscovery calls=%d, want newWatcher + recycle", n)
	}

	waitCtx, waitCancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer waitCancel()
	res, err := w.Wait(waitCtx)
	if err != nil {
		t.Fatalf("Wait: %v", err)
	}
	if res != nil {
		t.Fatalf("RecycleDiscovery must drain leftover RSSI, got %+v", res)
	}
}

func TestWatcherRecycleDiscoveryNoopWhilePaused(t *testing.T) {
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

	w.Pause()
	stops := countCalls(bus.calls, adapterIface+".StopDiscovery")
	if err := w.RecycleDiscovery(ctx); err != nil {
		t.Fatalf("RecycleDiscovery while paused: %v", err)
	}
	if countCalls(bus.calls, adapterIface+".StopDiscovery") != stops {
		t.Fatal("RecycleDiscovery must not touch discovery while paused")
	}
	if bus.discovering {
		t.Fatal("paused watcher must stay not-discovering")
	}
}

func TestWatcherForgetCachedRemovesLeftoverWithRSSI(t *testing.T) {
	bus := newFakeBluez()
	vin := "5YJ3E1EA0PF000000"
	bus.dev = &fakeDevice{path: bus.devPath(), name: vehicleBeaconName(vin), rssi: -94}
	bus.deviceVisible = true

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	w, err := newWatcher(ctx, bus, "", vin)
	if err != nil {
		t.Fatalf("newWatcher: %v", err)
	}
	defer w.Stop(ctx)

	if !w.ForgetCached(ctx) {
		t.Fatal("frozen leftover with RSSI must be RemoveDevice'd")
	}
	if bus.removeDeviceN == 0 {
		t.Fatal("ForgetCached must call RemoveDevice")
	}
	if bus.deviceVisible {
		t.Fatal("ForgetCached must drop the leftover Device1")
	}
	if w.devicePath != "" {
		t.Fatalf("devicePath = %q after ForgetCached, want empty", w.devicePath)
	}
}

func TestWatcherForgetCachedNoopWhilePaused(t *testing.T) {
	bus := newFakeBluez()
	vin := "5YJ3E1EA0PF000000"
	bus.dev = &fakeDevice{path: bus.devPath(), name: vehicleBeaconName(vin), rssi: -94}
	bus.deviceVisible = true

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	w, err := newWatcher(ctx, bus, "", vin)
	if err != nil {
		t.Fatalf("newWatcher: %v", err)
	}
	defer w.Stop(ctx)

	w.Pause()
	if w.ForgetCached(ctx) {
		t.Fatal("ForgetCached must not RemoveDevice while connecting")
	}
	if bus.removeDeviceN != 0 {
		t.Fatal("paused ForgetCached must leave Device1")
	}
}

func TestWatcherReleaseOrphanLinkDisconnectsLeftover(t *testing.T) {
	bus := newFakeBluez()
	vin := "5YJ3E1EA0PF000000"
	bus.dev = &fakeDevice{path: bus.devPath(), name: vehicleBeaconName(vin), omitRSSI: true}
	bus.deviceVisible = true
	bus.connected = true

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	w, err := newWatcher(ctx, bus, "", vin)
	if err != nil {
		t.Fatalf("newWatcher: %v", err)
	}
	defer w.Stop(ctx)

	if !w.ReleaseOrphanLink(ctx) {
		t.Fatal("leftover Connected Device1 must be released")
	}
	if bus.connected {
		t.Fatal("ReleaseOrphanLink must Disconnect")
	}
	if w.ReleaseOrphanLink(ctx) {
		t.Fatal("second ReleaseOrphanLink must be a no-op")
	}
}

func TestWatcherResumeDrainsStaleSignals(t *testing.T) {
	bus := newFakeBluez()
	vin := "5YJ3E1EA0PF000000"
	bus.dev = &fakeDevice{path: bus.devPath(), name: vehicleBeaconName(vin), rssi: -55}
	bus.deviceVisible = true

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	w, err := newWatcher(ctx, bus, "", vin)
	if err != nil {
		t.Fatalf("newWatcher: %v", err)
	}
	defer w.Stop(ctx)

	w.Pause()
	bus.advertiseRSSI(-40) // buffered while paused; must not count as a fresh arrival
	w.Resume()

	waitCtx, waitCancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer waitCancel()
	res, err := w.Wait(waitCtx)
	if err != nil {
		t.Fatalf("Wait: %v", err)
	}
	if res != nil {
		t.Fatalf("Resume must drain pre-Resume RSSI signals, got %+v", res)
	}
}

func TestWatcherWaitMatchesAliasWhenNameIsAddress(t *testing.T) {
	bus := newFakeBluez()
	vin := "5YJ3E1EA0PF000000"
	bus.dev = &fakeDevice{
		path:  bus.devPath(),
		name:  "AA:BB:CC:DD:EE:FF",
		alias: vehicleBeaconName(vin),
		rssi:  -48,
	}
	bus.deviceVisible = false

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	w, err := newWatcher(ctx, bus, "", vin)
	if err != nil {
		t.Fatalf("newWatcher: %v", err)
	}
	defer w.Stop(ctx)

	go func() {
		time.Sleep(20 * time.Millisecond)
		bus.advertiseAdded()
	}()
	res, err := w.Wait(ctx)
	if err != nil {
		t.Fatalf("Wait: %v", err)
	}
	if res == nil || res.RSSI != -48 || res.LocalName != vehicleBeaconName(vin) {
		t.Fatalf("Wait = %+v, want Alias-matched Tesla beacon", res)
	}
}

func TestWatcherWaitFetchesRSSIWhenAdvertisementOmitsIt(t *testing.T) {
	bus := newFakeBluez()
	vin := "5YJ3E1EA0PF000000"
	bus.dev = &fakeDevice{path: bus.devPath(), name: vehicleBeaconName(vin), omitRSSI: true}
	bus.deviceVisible = true

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	w, err := newWatcher(ctx, bus, "", vin)
	if err != nil {
		t.Fatalf("newWatcher: %v", err)
	}
	defer w.Stop(ctx)

	bus.dev.omitRSSI = false
	bus.dev.rssi = -64
	bus.advertiseProps(map[string]dbus.Variant{
		"ManufacturerData": dbus.MakeVariant("ad"),
	})
	res, err := w.Wait(ctx)
	if err != nil {
		t.Fatalf("Wait: %v", err)
	}
	if res == nil || res.RSSI != -64 || !res.HasRSSI {
		t.Fatalf("Wait = %+v, want RSSI fetched after ManufacturerData-only signal", res)
	}
}

func TestWatcherWaitDoesNotShareConnectionSignalChannel(t *testing.T) {
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

	// Consume the process-lifetime channel the way Connection.rxLoop does.
	// Wait must still see the advertisement on its own listener.
	thiefDone := make(chan struct{})
	go func() {
		for {
			select {
			case <-thiefDone:
				return
			case <-bus.signals():
			}
		}
	}()
	defer close(thiefDone)

	go func() {
		time.Sleep(20 * time.Millisecond)
		bus.advertiseAdded()
	}()
	res, err := w.Wait(ctx)
	if err != nil {
		t.Fatalf("Wait: %v", err)
	}
	if res == nil || res.RSSI != -50 {
		t.Fatalf("Wait = %+v, want advertisement despite rxLoop stealing from signals()", res)
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
