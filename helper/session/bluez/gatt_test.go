package bluez

import (
	"context"
	"testing"
	"time"

	"github.com/godbus/dbus"
)

func TestDiscoverGATTFindsTeslaCharacteristics(t *testing.T) {
	bus := newFakeBluez()
	vin := "5YJ3E1EA0PF000000"
	bus.dev = &fakeDevice{path: bus.devPath(), name: vehicleBeaconName(vin)}
	bus.deviceVisible = true
	bus.gattReady = true

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	svc, tx, rx, err := discoverGATT(ctx, bus, bus.devPath())
	if err != nil {
		t.Fatalf("discoverGATT: %v", err)
	}
	if svc != bus.svcPath() {
		t.Errorf("svc path = %s, want %s", svc, bus.svcPath())
	}
	if tx != bus.txPath() {
		t.Errorf("tx path = %s, want %s", tx, bus.txPath())
	}
	if rx != bus.rxPath() {
		t.Errorf("rx path = %s, want %s", rx, bus.rxPath())
	}
}

func TestDiscoverGATTFailsWhenServiceMissing(t *testing.T) {
	bus := newFakeBluez()
	vin := "5YJ3E1EA0PF000000"
	bus.dev = &fakeDevice{path: bus.devPath(), name: vehicleBeaconName(vin)}
	bus.deviceVisible = true
	bus.gattReady = false // services resolved but tree lacks GATT objects

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	if _, _, _, err := discoverGATT(ctx, bus, bus.devPath()); err == nil {
		t.Fatal("expected discoverGATT to fail when the Tesla service is absent")
	}
}

func TestIsUUIDNormalizesDashes(t *testing.T) {
	cases := []struct {
		value string
		want  bool
	}{
		{vehicleServiceUUID, true},
		{"00000211-b2d1-43f0-9b88-960cebf8b91e", true}, // BlueZ may report dashed form
		{"00000211-B2D1-43F0-9B88-960CEBF8B91E", true}, // ...or uppercase
		{"00000214b2d143f09b88960cebf8b91e", false},
	}
	for _, c := range cases {
		if got := isUUID(dbus.MakeVariant(c.value), vehicleServiceUUID); got != c.want {
			t.Errorf("isUUID(%q) = %v, want %v", c.value, got, c.want)
		}
	}
	if isUUID(dbus.MakeVariant(42), vehicleServiceUUID) {
		t.Error("isUUID should reject non-string UUID properties")
	}
}
