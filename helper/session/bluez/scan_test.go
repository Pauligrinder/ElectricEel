package bluez

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/godbus/dbus"
	"github.com/teslamotors/vehicle-command/pkg/connector/ble"
)

func TestVehicleBeaconNameMatchesUpstream(t *testing.T) {
	// Guard against accidentally re-implementing the name format: the value
	// must exactly match upstream's ble.VehicleLocalName.
	for _, vin := range []string{
		"5YJ3E1EA0PF000000",
		"LRW3E7FA9NC123456",
		"SAYHCBAFXGC123456",
	} {
		if got, want := vehicleBeaconName(vin), ble.VehicleLocalName(vin); got != want {
			t.Errorf("vehicleBeaconName(%q) = %q, want %q (must match upstream)", vin, got, want)
		}
		if !strings.HasPrefix(vehicleBeaconName(vin), "S") || !strings.HasSuffix(vehicleBeaconName(vin), "C") {
			t.Errorf("vehicleBeaconName(%q) = %q, expected S...C beacon format", vin, vehicleBeaconName(vin))
		}
	}
}

func TestScanFindsVehicleBeacon(t *testing.T) {
	bus := newFakeBluez()
	vin := "5YJ3E1EA0PF000000"
	bus.dev = &fakeDevice{
		path: dbus.ObjectPath("/org/bluez/hci0/dev_DDEADBEEF001"),
		name: vehicleBeaconName(vin),
		rssi: -55,
	}
	bus.deviceVisible = true

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	res, err := scan(ctx, bus, "", vin)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if res.Path != bus.dev.path {
		t.Errorf("res.Path = %s, want %s", res.Path, bus.dev.path)
	}
	if res.LocalName != bus.dev.name {
		t.Errorf("res.LocalName = %q, want %q", res.LocalName, bus.dev.name)
	}
	if res.RSSI != -55 {
		t.Errorf("res.RSSI = %d, want -55", res.RSSI)
	}
	if !res.HasRSSI {
		t.Error("expected HasRSSI=true when fake reports RSSI")
	}
	if bus.discovering {
		t.Error("expected discovery to be stopped after scan returned")
	}
	// The scan must have started (and therefore stopped) discovery.
	if !hasCall(bus.calls, adapterIface+".StartDiscovery") {
		t.Error("expected StartDiscovery to have been called")
	}
}

func TestScanLeavesSharedDiscoveryRunning(t *testing.T) {
	bus := newFakeBluez()
	vin := "5YJ3E1EA0PF000000"
	bus.dev = &fakeDevice{path: bus.devPath(), name: vehicleBeaconName(vin)}
	bus.deviceVisible = true
	bus.discovering = true

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	if _, err := scan(ctx, bus, "", vin); err != nil {
		t.Fatalf("scan: %v", err)
	}
	if !bus.discovering {
		t.Error("scan must not StopDiscovery when presence already holds it")
	}
}

func TestFindAdapterPrefersPowered(t *testing.T) {
	bus := newFakeBluez()
	bus.powered = true
	bus.extraAdapters = map[string]bool{
		"hci1": false,
		"zzz":  false,
	}

	path, err := findAdapter(context.Background(), bus, "")
	if err != nil {
		t.Fatalf("findAdapter: %v", err)
	}
	if path != dbus.ObjectPath("/org/bluez/hci0") {
		t.Fatalf("findAdapter picked %s, want powered hci0", path)
	}
}

func TestFindAdapterForNamePrefersVehicleAdapter(t *testing.T) {
	bus := newFakeBluez()
	vin := "5YJ3E1EA0PF000000"
	bus.powered = true
	bus.extraAdapters = map[string]bool{"hci1": true}
	hci1Path := dbus.ObjectPath("/org/bluez/hci1/dev_98_04_ED_D7_EE_5E")
	bus.dev = &fakeDevice{path: hci1Path, name: vehicleBeaconName(vin), omitRSSI: true}
	bus.deviceVisible = true

	path, err := findAdapterForName(context.Background(), bus, "", vehicleBeaconName(vin))
	if err != nil {
		t.Fatalf("findAdapterForName: %v", err)
	}
	if path != "/org/bluez/hci1" {
		t.Fatalf("findAdapterForName picked %s, want hci1 (where the Tesla Device1 lives)", path)
	}
	plain, err := findAdapter(context.Background(), bus, "")
	if err != nil {
		t.Fatalf("findAdapter: %v", err)
	}
	if plain != "/org/bluez/hci0" {
		t.Fatalf("findAdapter picked %s, want lexicographic hci0 so the vehicle preference is doing work", plain)
	}
}

func TestWaitPoweredAlreadyOn(t *testing.T) {
	bus := newFakeBluez()
	bus.powered = true
	if err := waitPowered(context.Background(), bus, ""); err != nil {
		t.Fatalf("waitPowered: %v", err)
	}
	if bus.matches != 0 {
		t.Fatalf("already-powered wait subscribed matches=%d, want 0", bus.matches)
	}
}

func TestWaitPoweredWakesOnToggle(t *testing.T) {
	bus := newFakeBluez()
	bus.powered = false
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- waitPowered(ctx, bus, "") }()
	waitForFakeMatches(t, bus, 2)
	bus.adapterPoweredChanged("hci0", true)
	if err := <-done; err != nil {
		t.Fatalf("waitPowered after Powered=true: %v", err)
	}
	if bus.removedMatches < 2 {
		t.Fatalf("removedMatches=%d, want signal matches cleaned up", bus.removedMatches)
	}
}

func TestWaitPoweredWakesOnHci1(t *testing.T) {
	bus := newFakeBluez()
	bus.powered = false
	bus.extraAdapters = map[string]bool{"hci1": false}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- waitPowered(ctx, bus, "") }()
	waitForFakeMatches(t, bus, 2)
	bus.adapterPoweredChanged("hci1", true)
	if err := <-done; err != nil {
		t.Fatalf("waitPowered after hci1 Powered=true: %v", err)
	}
}

func TestWaitPoweredWakesOnAdapterAdded(t *testing.T) {
	bus := newFakeBluez()
	bus.powered = false
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- waitPowered(ctx, bus, "") }()
	waitForFakeMatches(t, bus, 2)
	bus.adapterAdded("hci1", true)
	if err := <-done; err != nil {
		t.Fatalf("waitPowered after hci1 appear: %v", err)
	}
}

func TestWaitPoweredIgnoresPowerOffAndDevices(t *testing.T) {
	bus := newFakeBluez()
	bus.powered = false
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- waitPowered(ctx, bus, "") }()
	waitForFakeMatches(t, bus, 2)
	bus.adapterPoweredChanged("hci0", false)
	bus.advertiseAdded()
	err := <-done
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("waitPowered err=%v, want deadline (Powered=false / Device1 must not wake)", err)
	}
}

func TestWaitPoweredHonorsAdapterID(t *testing.T) {
	bus := newFakeBluez()
	bus.powered = false
	bus.extraAdapters = map[string]bool{"hci1": false}
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- waitPowered(ctx, bus, "hci0") }()
	waitForFakeMatches(t, bus, 2)
	bus.adapterPoweredChanged("hci1", true)
	err := <-done
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("waitPowered(hci0) woke on hci1: %v", err)
	}
}

func TestAdapterPowerOnSignal(t *testing.T) {
	if adapterPowerOnSignal(&dbus.Signal{
		Name: propsIface + ".PropertiesChanged",
		Path: "/org/bluez/hci1",
		Body: []interface{}{
			adapterIface,
			map[string]dbus.Variant{"Powered": dbus.MakeVariant(true)},
		},
	}, "") != true {
		t.Fatal("hci1 Powered=true must wake")
	}
	if adapterPowerOnSignal(&dbus.Signal{
		Name: propsIface + ".PropertiesChanged",
		Path: "/org/bluez/hci1",
		Body: []interface{}{
			adapterIface,
			map[string]dbus.Variant{"Powered": dbus.MakeVariant(false)},
		},
	}, "") {
		t.Fatal("Powered=false must not wake")
	}
	if adapterPowerOnSignal(&dbus.Signal{
		Name: propsIface + ".PropertiesChanged",
		Path: "/org/bluez/hci1/dev_AA_BB_CC_DD_EE_FF",
		Body: []interface{}{
			deviceIface,
			map[string]dbus.Variant{"RSSI": dbus.MakeVariant(int16(-70))},
		},
	}, "") {
		t.Fatal("Device1 RSSI must not look like adapter power")
	}
}

func waitForFakeMatches(t *testing.T, bus *fakeBluez, n int) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if bus.matches >= n {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("matches=%d, want >= %d", bus.matches, n)
}

func TestEnsurePoweredDoesNotSetWhenOff(t *testing.T) {
	bus := newFakeBluez()
	bus.powered = false
	bus.setPoweredErr = dbus.Error{Name: "org.freedesktop.DBus.Error.AuthFailed", Body: []interface{}{""}}

	err := ensurePowered(context.Background(), bus, dbus.ObjectPath("/org/bluez/hci0"))
	if err == nil {
		t.Fatal("expected ensurePowered to fail when the adapter is off")
	}
	if !strings.Contains(err.Error(), "adapter not powered") {
		t.Fatalf("error %q should say the adapter is off without attempting Set", err)
	}
	if hasCall(bus.calls, propsIface+".Set") {
		t.Fatal("ensurePowered must not Set Powered (Sailfish ConnMan AuthFailed)")
	}
	if bus.powered {
		t.Fatal("ensurePowered must not flip the fake Powered flag")
	}
}

func TestScanFailsWhenAdapterOff(t *testing.T) {
	bus := newFakeBluez()
	bus.powered = false
	vin := "5YJ3E1EA0PF000000"
	bus.dev = &fakeDevice{path: bus.devPath(), name: vehicleBeaconName(vin)}
	bus.deviceVisible = true

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	if _, err := scan(ctx, bus, "", vin); err == nil {
		t.Fatal("scan must fail when the adapter is off")
	} else if !strings.Contains(err.Error(), "adapter not powered") {
		t.Fatalf("scan error %q, want adapter not powered", err)
	}
	if bus.powered {
		t.Error("scan must not power the adapter on")
	}
}

func TestScanHonorsSpecificAdapter(t *testing.T) {
	bus := newFakeBluez()
	vin := "5YJ3E1EA0PF000000"
	bus.dev = &fakeDevice{path: bus.devPath(), name: vehicleBeaconName(vin)}
	bus.deviceVisible = true

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	if _, err := scan(ctx, bus, "hci0", vin); err != nil {
		t.Fatalf("scan with matching adapter: %v", err)
	}
	if _, err := scan(ctx, bus, "hci9", vin); err == nil {
		t.Fatal("scan with nonexistent adapter should fail")
	}
}

func TestScanWaitsForBeaconToAppear(t *testing.T) {
	bus := newFakeBluez()
	vin := "5YJ3E1EA0PF000000"
	bus.dev = &fakeDevice{path: bus.devPath(), name: vehicleBeaconName(vin)}
	bus.deviceVisible = false
	bus.deviceAppearCall = 3 // beacon only shows up after a few polls
	bus.gattReady = false

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	res, err := scan(ctx, bus, "", vin)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if res.LocalName != bus.dev.name {
		t.Errorf("scan returned %q after delayed appearance", res.LocalName)
	}
}

func TestScanTimesOutWithoutBeacon(t *testing.T) {
	bus := newFakeBluez()
	vin := "5YJ3E1EA0PF000000"
	bus.dev = &fakeDevice{path: bus.devPath(), name: vehicleBeaconName(vin)}
	bus.deviceVisible = false

	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()

	if _, err := scan(ctx, bus, "", vin); err == nil {
		t.Fatal("expected scan to time out when no beacon ever appears")
	}
}

func TestTimedOutScanReleasesDiscoveryWithFreshContext(t *testing.T) {
	bus := newFakeBluez()
	bus.rejectCancelledStop = true
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	if _, err := scan(ctx, bus, "", "5YJ3E1EA0PF000000"); err == nil {
		t.Fatal("expected scan deadline")
	}
	if bus.discovering {
		t.Fatal("timed-out scan left discovery running")
	}
}

func TestScanIgnoresOtherDevices(t *testing.T) {
	bus := newFakeBluez()
	vin := "5YJ3E1EA0PF000000"
	// A different device is present and advertising a different name; the
	// vehicle is never seen.
	bus.dev = &fakeDevice{path: bus.devPath(), name: "SOME OTHER SOUNDBAR"}
	bus.deviceVisible = true

	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()

	if _, err := scan(ctx, bus, "", vin); err == nil {
		t.Fatal("expected scan not to match a non-vehicle device name")
	}
}

func TestScanIgnoresCachedDeviceWithoutRSSI(t *testing.T) {
	bus := newFakeBluez()
	vin := "5YJ3E1EA0PF000000"
	bus.dev = &fakeDevice{path: bus.devPath(), name: vehicleBeaconName(vin), omitRSSI: true}
	bus.deviceVisible = true

	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()

	if _, err := scan(ctx, bus, "", vin); err == nil {
		t.Fatal("scan must not return a leftover Device1 that has no live RSSI")
	}
}

func TestFindBeaconReportsMissingRSSI(t *testing.T) {
	bus := newFakeBluez()
	vin := "5YJ3E1EA0PF000000"
	bus.dev = &fakeDevice{path: bus.devPath(), name: vehicleBeaconName(vin), omitRSSI: true}
	bus.deviceVisible = true

	res, err := findBeacon(context.Background(), bus, dbus.ObjectPath("/org/bluez/hci0"), vehicleBeaconName(vin))
	if err != nil {
		t.Fatalf("findBeacon: %v", err)
	}
	if res == nil {
		t.Fatal("expected cached device to still be found")
	}
	if res.HasRSSI {
		t.Fatal("expected HasRSSI=false when BlueZ omits RSSI")
	}
}

func TestStartDiscoveryToleratesAlreadyInProgress(t *testing.T) {
	if !isDiscoveryInProgress(errors.New("org.bluez.Error.InProgress")) {
		t.Fatal("expected InProgress error to be recognized")
	}
	bus := newFakeBluez()
	bus.discovering = true
	ctx := context.Background()
	adapterPath := dbus.ObjectPath("/org/bluez/hci0")
	if err := startDiscovery(ctx, bus, adapterPath); err != nil {
		t.Fatalf("startDiscovery with already discovering should succeed: %v", err)
	}
}

func hasCall(calls []string, want string) bool {
	for _, c := range calls {
		if c == want {
			return true
		}
	}
	return false
}

func countCalls(calls []string, want string) int {
	n := 0
	for _, c := range calls {
		if c == want {
			n++
		}
	}
	return n
}
