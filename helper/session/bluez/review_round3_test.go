package bluez

import (
	"context"
	"testing"
	"time"

	"github.com/godbus/dbus"
)

// Review round 3. These tests FAIL on the current code and PASS once fixed.

// TestReviewRound3ConnectReportsBluetoothOffWithoutBurningDeadline:
//
// Bug: with no presence watcher, library connects with target == nil, so
// tryConnect runs scan() -> ensurePowered(). When the radio is off and the
// Powered write is refused (the common Sailfish case: ConnMan owns radio
// power, see ensurePowered), ensurePowered returns *AdapterOffError. tryConnect
// marks every scan error retry=true and IsAdapterError only string-matches a
// few BlueZ names, so connect() keeps retrying with backoff until the
// operation deadline. It then returns fmt.Errorf("%v: %w", lastErr, ctx.Err()),
// which flattens the typed cause away: library.classify reports TimedOut
// after the full connect timeout instead of EE_BLUETOOTH_OFF immediately.
//
// Desired: an AdapterOffError is a permanent adapter condition. connect must
// return promptly and the returned error must still satisfy IsBluetoothOff.
func TestReviewRound3ConnectReportsBluetoothOffWithoutBurningDeadline(t *testing.T) {
	bus := newFakeBluez()
	vin := "5YJ3E1EA0PF000000"
	bus.dev = &fakeDevice{path: bus.devPath(), name: vehicleBeaconName(vin), rssi: -50}
	bus.deviceVisible = true
	bus.powered = false
	bus.setPoweredErr = &dbus.Error{Name: "org.freedesktop.DBus.Error.AccessDenied", Body: []interface{}{"Rejected send message"}}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	started := time.Now()
	cc, err := connect(ctx, bus, "hci0", vin, nil)
	elapsed := time.Since(started)
	if err == nil {
		cc.Close()
		t.Fatal("connect succeeded with the radio off")
	}
	if !IsBluetoothOff(err) {
		t.Errorf("connect error %q lost the AdapterOffError cause; library.classify cannot report BluetoothOff", err)
	}
	if ctx.Err() != nil || elapsed > time.Second {
		t.Errorf("connect retried a permanent radio-off condition for %s (deadline exhausted=%v)", elapsed.Round(time.Millisecond), ctx.Err() != nil)
	}
}

// TestReviewRound3ConnectDoesNotWaitForeverForForeignDiscovery:
//
// Bug: tryConnect calls StopDiscovery and then waitDiscoveryStopped polls
// Adapter1.Discovering until it is false, bounded only by the operation
// deadline. Adapter1.Discovering is adapter-wide: it stays true while ANY
// other D-Bus client (the Sailfish Bluetooth settings page, another BLE app)
// holds a discovery session, and our StopDiscovery cannot end someone else's
// session. While another client scans, every connect therefore spins until
// its deadline and fails without ever calling Device1.Connect - the phone
// key cannot connect at all.
//
// Desired: waiting for the scanner to settle is bounded (a short grace period
// well below the connect timeout), after which Device1.Connect is attempted
// anyway (or a distinct, classified error is returned early). Here the fake
// keeps Discovering=true regardless of our StopDiscovery, modelling a foreign
// discovery session, and Device1.Connect itself would succeed.
func TestReviewRound3ConnectDoesNotWaitForeverForForeignDiscovery(t *testing.T) {
	bus := newFakeBluez()
	vin := "5YJ3E1EA0PF000000"
	bus.dev = &fakeDevice{path: bus.devPath(), name: vehicleBeaconName(vin), rssi: -50}
	bus.deviceVisible, bus.servicesResolved, bus.gattReady = true, true, true
	bus.discovering = true
	// Non-zero stopDiscoveryPolls makes our StopDiscovery a no-op; Discovering
	// stays true for far longer than this test runs (another client's scan).
	bus.stopDiscoveryPolls = 1 << 30

	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	cc, err := connect(ctx, bus, "hci0", vin, &ScanResult{Path: bus.devPath(), HasRSSI: true})
	if err == nil {
		cc.Close()
	}
	if countCalls(bus.calls, deviceIface+".Connect") == 0 {
		t.Fatalf("Device1.Connect never attempted while another client held discovery; connect error: %v", err)
	}
	if err != nil {
		t.Fatalf("connect failed although Device1.Connect would succeed: %v", err)
	}
}
