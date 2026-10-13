package bluez

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/godbus/dbus"
)

// Production findings: desired behavior. FAILS now, PASSES after fix.

func TestProductionFindingsVariantInt16RejectsOutOfRange(t *testing.T) {
	// variantInt16 silently narrows int32/int to int16 (70000 -> 4464),
	// corrupting RSSI gating. Out-of-range values must report !ok.
	for _, v := range []dbus.Variant{
		dbus.MakeVariant(int32(70000)),
		dbus.MakeVariant(int32(-70000)),
		dbus.MakeVariant(int(1 << 20)),
		dbus.MakeVariant(int(-1 << 20)),
	} {
		if got, ok := variantInt16(v); ok {
			t.Errorf("variantInt16(%v) = (%d, true); want (0, false) — truncation corrupts RSSI gating", v.Value(), got)
		}
	}
	// In-range values must keep working.
	for _, tc := range []struct {
		v    dbus.Variant
		want int16
	}{
		{dbus.MakeVariant(int16(-90)), -90},
		{dbus.MakeVariant(int32(-60)), -60},
		{dbus.MakeVariant(int(0)), 0},
	} {
		if got, ok := variantInt16(tc.v); !ok || got != tc.want {
			t.Errorf("variantInt16(%v) = (%d, %v); want (%d, true)", tc.v.Value(), got, ok, tc.want)
		}
	}
}

func TestProductionFindingsConnectPreservesContextDeadline(t *testing.T) {
	// gatt.go connect(): on ctx.Done() with a prior lastErr it returns
	// lastErr instead of ctx.Err(), so classify() reports Failed instead of
	// TimedOut and Rust loses the timeout signal. Desired: the returned
	// error reports the deadline.
	bus := newFakeBluez()
	vin := "5YJ3E1EA0PF000000"
	bus.dev = &fakeDevice{path: bus.devPath(), name: vehicleBeaconName(vin)}
	bus.deviceVisible = true
	bus.connectErr = errors.New("org.bluez.Error.Failed: transient link failure")

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	_, err := connect(ctx, bus, "hci0", vin, &ScanResult{Path: bus.dev.path})
	if err == nil {
		t.Fatal("expected connect to fail")
	}
	if !errors.Is(err, context.DeadlineExceeded) && !errors.Is(err, context.Canceled) {
		t.Fatalf("connect error = %v; want context deadline/cancel so Rust classifies TimedOut, not Failed", err)
	}
}
