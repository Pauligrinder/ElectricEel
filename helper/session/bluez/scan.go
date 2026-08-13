package bluez

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/godbus/dbus"
	"github.com/teslamotors/vehicle-command/pkg/connector/ble"
)

// pollInterval is how often GetManagedObjects is re-read while waiting for a
// beacon. The car advertises every 20-150ms, so a 100ms poll notices it
// within a connect timeout. Polling (rather than subscribing to
// org.bluez signals) keeps the scan loop deterministic and trivially
// testable; the signal-based path is only required later, for GATT
// notifications, where events cannot be polled.
const pollInterval = 100 * time.Millisecond

// ScanResult describes a discovered vehicle beacon. Path is the org.bluez
// Device1 object path (the identifier Connect needs).
type ScanResult struct {
	Path      dbus.ObjectPath
	LocalName string
	RSSI      int16
}

// vehicleBeaconName returns the advertising local name the vehicle exposes
// for a given VIN. It reuses upstream's VehicleLocalName so the name format
// can never drift from the upstream implementation.
func vehicleBeaconName(vin string) string {
	return ble.VehicleLocalName(vin)
}

// scan finds the vehicle's beacon. It stops discovery on the way out
// regardless of how it returns, so an earlier stopDiscovery call is the
// normal path and the deferred one is a no-op in the success case... except
// that it would also be called on success; callers should not rely on
// discovery staying on after scan returns.
func scan(ctx context.Context, bus dbusBus, adapterID, vin string) (*ScanResult, error) {
	name := vehicleBeaconName(vin)

	adapterPath, err := findAdapter(ctx, bus, adapterID)
	if err != nil {
		return nil, err
	}
	if err := ensurePowered(ctx, bus, adapterPath); err != nil {
		return nil, err
	}
	// Best-effort filter: restricting discovery to LE keeps the scan on the
	// car's PHY and avoids classic (BR/EDR) traffic. Failure is tolerated
	// because an unfiltered scan still finds the car.
	_ = setDiscoveryFilter(ctx, bus, adapterPath)

	if err := startDiscovery(ctx, bus, adapterPath); err != nil {
		return nil, fmt.Errorf("bluez: start discovery: %w", err)
	}
	defer stopDiscovery(ctx, bus, adapterPath)

	for {
		result, err := findBeacon(ctx, bus, adapterPath, name)
		if err != nil {
			return nil, err
		}
		if result != nil {
			return result, nil
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(pollInterval):
		}
	}
}

// managedObjects returns BlueZ's full object tree keyed by object path.
func managedObjects(ctx context.Context, bus dbusBus) (map[dbus.ObjectPath]map[string]map[string]dbus.Variant, error) {
	body, err := bus.object(bluezService, "/").call(ctx, objMgrIface+".GetManagedObjects")
	if err != nil {
		return nil, fmt.Errorf("bluez: enumerate objects: %w", err)
	}
	if len(body) != 1 {
		return nil, errors.New("bluez: unexpected GetManagedObjects reply")
	}
	m, ok := body[0].(map[dbus.ObjectPath]map[string]map[string]dbus.Variant)
	if !ok {
		return nil, fmt.Errorf("bluez: unexpected GetManagedObjects reply type %T", body[0])
	}
	return m, nil
}

// findAdapter locates the org.bluez Adapter1 object. If adapterID names a
// specific controller ("hci0", ...), that one is required; otherwise the
// first available adapter is returned.
func findAdapter(ctx context.Context, bus dbusBus, adapterID string) (dbus.ObjectPath, error) {
	objects, err := managedObjects(ctx, bus)
	if err != nil {
		return "", err
	}
	var fallback dbus.ObjectPath
	for path, ifaces := range objects {
		if _, ok := ifaces[adapterIface]; !ok {
			continue
		}
		base := strings.TrimPrefix(string(path), "/org/bluez/")
		if adapterID == "" {
			if fallback == "" {
				fallback = path
			}
			continue
		}
		if base == adapterID {
			return path, nil
		}
	}
	if fallback != "" {
		return fallback, nil
	}
	return "", fmt.Errorf("bluez: no Bluetooth adapter found (wanted %q)", adapterID)
}

// ensurePowered turns the adapter on if it is off. Under a healthy
// bluetoothd it is already powered; this is a convenience that mirrors
// go-ble bringing the device up.
func ensurePowered(ctx context.Context, bus dbusBus, adapterPath dbus.ObjectPath) error {
	obj := bus.object(bluezService, adapterPath)
	v, err := obj.getProp(ctx, adapterIface, "Powered")
	if err != nil {
		return fmt.Errorf("bluez: read adapter Powered: %w", err)
	}
	powered, ok := variantBool(v)
	if !ok {
		return fmt.Errorf("bluez: decode adapter Powered: got %T", v.Value())
	}
	if powered {
		return nil
	}
	if err := obj.setProp(ctx, adapterIface, "Powered", true); err != nil {
		return fmt.Errorf("bluez: power on adapter: %w", err)
	}
	return nil
}

func setDiscoveryFilter(ctx context.Context, bus dbusBus, adapterPath dbus.ObjectPath) error {
	filter := map[string]dbus.Variant{
		"Transport":     dbus.MakeVariant("le"),
		"DuplicateData": dbus.MakeVariant(false),
	}
	_, err := bus.object(bluezService, adapterPath).call(ctx, adapterIface+".SetDiscoveryFilter", filter)
	return err
}

func startDiscovery(ctx context.Context, bus dbusBus, adapterPath dbus.ObjectPath) error {
	_, err := bus.object(bluezService, adapterPath).call(ctx, adapterIface+".StartDiscovery")
	return err
}

func stopDiscovery(ctx context.Context, bus dbusBus, adapterPath dbus.ObjectPath) {
	_, _ = bus.object(bluezService, adapterPath).call(ctx, adapterIface+".StopDiscovery")
}

// findBeacon inspects the current object tree for a device advertising the
// target local name. Returns (nil, nil) when no match is present yet.
func findBeacon(ctx context.Context, bus dbusBus, adapterPath dbus.ObjectPath, name string) (*ScanResult, error) {
	objects, err := managedObjects(ctx, bus)
	if err != nil {
		return nil, err
	}
	prefix := string(adapterPath) + "/dev_"
	for path, ifaces := range objects {
		dev, ok := ifaces[deviceIface]
		if !ok || !strings.HasPrefix(string(path), prefix) {
			continue
		}
		devName, ok := variantString(dev["Name"])
		if !ok || devName != name {
			continue
		}
		result := &ScanResult{Path: path, LocalName: devName}
		if rssi, ok := variantInt16(dev["RSSI"]); ok {
			result.RSSI = rssi
		}
		return result, nil
	}
	return nil, nil
}
