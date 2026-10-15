// Package bluez provides a connector.Connector for Tesla vehicles that
// talks to the vehicle over BlueZ's org.bluez D-Bus interface instead of
// go-ble's raw HCI channel.
//
// The raw HCI approach requires exclusive, whole-adapter control: go-ble's
// socket setup brings the controller DOWN and binds an HCI user channel
// (HCI_CHANNEL_USER), which forcibly drops every other Bluetooth connection
// on the phone - e.g. an A2DP audio stream to a soundbar. Routing through
// org.bluez keeps the radio owned by the OS Bluetooth stack, so existing
// connections are never disturbed.
//
// This package reimplements only the transport, mirroring the behavior of
// upstream pkg/connector/ble (same 2-byte length-prefixed message framing,
// same blockLength chunking, same error/latency semantics). Everything above
// the transport (session crypto, command dispatch) is untouched and comes
// from the vendored upstream code.
package bluez

import (
	"context"
	"errors"
	"time"

	"github.com/godbus/dbus"
	"github.com/teslamotors/vehicle-command/pkg/connector"
)

// D-Bus interface/object names for the org.bluez service. Collected here so
// the transport logic and the tests reference a single set of names.
const (
	bluezService = "org.bluez"

	objMgrIface = "org.freedesktop.DBus.ObjectManager"
	propsIface  = "org.freedesktop.DBus.Properties"

	adapterIface = "org.bluez.Adapter1"
	deviceIface  = "org.bluez.Device1"
	gattSvcIface = "org.bluez.GattService1"
	gattChrIface = "org.bluez.GattCharacteristic1"
)

// Tesla GATT service/characteristic UUIDs in BlueZ's on-the-wire lowercase,
// dash-stripped form.
const (
	vehicleServiceUUID = "00000211b2d143f09b88960cebf8b91e"
	toVehicleUUID      = "00000212b2d143f09b88960cebf8b91e"
	fromVehicleUUID    = "00000213b2d143f09b88960cebf8b91e"
)

// Transport constants mirroring upstream pkg/connector/ble:
//   - maxMessageSize is the cap on a single command message (upstream
//     maxBLEMessageSize).
//   - maxExpectedMTU is go-ble's MaxMTU (512 + 3 bytes of ATT header); Linux
//     BlueZ negotiates at least this, so writes are chunked at MTU-3.
//   - defaultMTU is go-ble's DefaultMTU; 23 is the minimum ATT MTU every
//     device supports, so MTU-3 = 20 bytes is the guaranteed-safe fallback
//     chunk size when the assumed MTU turns out to be too large.
const (
	maxMessageSize = 1024
	maxExpectedMTU = 512 + 3
	defaultMTU     = 23
)

// rxTimeout is the gap between received chunks that resets the RX
// reassembly buffer (upstream ble.rxTimeout).
const rxTimeout = time.Second

// dbusBus is the slice of the D-Bus connection this package uses. It exists
// so tests can substitute a fake that answers method calls and delivers
// signals without a real system bus.
type dbusBus interface {
	// object returns a handle for method calls/property access on dest/path.
	object(dest string, path dbus.ObjectPath) dbusCaller
	// subscribeSignals registers an independent, lifetime-scoped channel.
	subscribeSignals(chan *dbus.Signal)
	unsubscribeSignals(chan *dbus.Signal)
	// addMatch/removeMatch register/unregister a signal match rule.
	addMatch(options ...dbus.MatchOption) error
	removeMatch(options ...dbus.MatchOption) error
	// logf records a diagnostic for the session that owns this bus.
	logf(format string, args ...interface{})
}

// dbusCaller is the slice of a D-Bus object this package uses.
type dbusCaller interface {
	// call invokes a method and returns the response body.
	call(ctx context.Context, method string, args ...interface{}) ([]interface{}, error)
	// getProp/setProp read/write a property via org.freedesktop.DBus.Properties.
	getProp(ctx context.Context, iface, prop string) (dbus.Variant, error)
	setProp(ctx context.Context, iface, prop string, value interface{}) error
}

// variant decoders. This repo's pinned godbus version predates
// Variant.Store, so property values are decoded from Variant.Value directly.
// The ok-returning helpers keep each decode site a one-liner.
func variantBool(v dbus.Variant) (bool, bool) {
	b, ok := v.Value().(bool)
	return b, ok
}

func variantString(v dbus.Variant) (string, bool) {
	s, ok := v.Value().(string)
	return s, ok
}

func variantBytes(v dbus.Variant) ([]byte, bool) {
	b, ok := v.Value().([]byte)
	return b, ok
}

// variantInt16 tolerates the handful of int widths a D-Bus int16 may decode
// to across implementations.
func variantInt16(v dbus.Variant) (int16, bool) {
	switch n := v.Value().(type) {
	case int16:
		return n, true
	case int32:
		if n >= -32768 && n <= 32767 {
			return int16(n), true
		}
	case int:
		if n >= -32768 && n <= 32767 {
			return int16(n), true
		}
	}
	return 0, false
}

// godbusConn adapts a *dbus.Conn to dbusBus.
type godbusConn struct {
	c   *dbus.Conn
	log Logger
}

func adaptConn(conn *dbus.Conn, log Logger) *godbusConn {
	return &godbusConn{c: conn, log: log}
}

func (g *godbusConn) logf(format string, args ...interface{}) { g.log.logf(format, args...) }

func (g *godbusConn) object(dest string, path dbus.ObjectPath) dbusCaller {
	return &godbusObject{obj: g.c.Object(dest, path)}
}

func (g *godbusConn) subscribeSignals(ch chan *dbus.Signal)   { g.c.Signal(ch) }
func (g *godbusConn) unsubscribeSignals(ch chan *dbus.Signal) { g.c.RemoveSignal(ch) }

func (g *godbusConn) addMatch(options ...dbus.MatchOption) error {
	return g.c.AddMatchSignal(options...)
}

func (g *godbusConn) removeMatch(options ...dbus.MatchOption) error {
	return g.c.RemoveMatchSignal(options...)
}

// godbusObject adapts a *dbus.Object to dbusCaller.
type godbusObject struct {
	obj dbus.BusObject
}

func (o *godbusObject) call(ctx context.Context, method string, args ...interface{}) ([]interface{}, error) {
	call := o.obj.CallWithContext(ctx, method, 0, args...)
	if call.Err != nil {
		return nil, call.Err
	}
	return call.Body, nil
}

func (o *godbusObject) getProp(ctx context.Context, iface, prop string) (dbus.Variant, error) {
	body, err := o.call(ctx, propsIface+".Get", iface, prop)
	if err != nil {
		return dbus.Variant{}, err
	}
	if len(body) != 1 {
		return dbus.Variant{}, &dbus.Error{Name: "bluez", Body: []interface{}{"unexpected Properties.Get reply"}}
	}
	v, ok := body[0].(dbus.Variant)
	if !ok {
		return dbus.Variant{}, &dbus.Error{Name: "bluez", Body: []interface{}{"unexpected Properties.Get reply type"}}
	}
	return v, nil
}

func (o *godbusObject) setProp(ctx context.Context, iface, prop string, value interface{}) error {
	_, err := o.call(ctx, propsIface+".Set", iface, prop, dbus.MakeVariant(value))
	return err
}

// Conn is a handle to a system-bus connection prepared for org.bluez calls.
// Watchers and GATT links own independent signal subscriptions; callers
// reuse the bus connection for the lifetime of the owning protocol session.
type Conn struct {
	raw *dbus.Conn
	bus dbusBus
}

// Open establishes an owned system-bus connection for BlueZ access. Transport
// diagnostics go to log (which may be nil), never to other sessions.
func Open(log Logger) (*Conn, error) {
	// SystemBus caches a process-wide connection, even after Close. Session
	// teardown must not close that shared handle and poison every later retry.
	raw, err := dbus.SystemBusPrivate()
	if err != nil {
		return nil, err
	}
	if err := raw.Auth(nil); err != nil {
		_ = raw.Close()
		return nil, err
	}
	if err := raw.Hello(); err != nil {
		_ = raw.Close()
		return nil, err
	}
	return &Conn{raw: raw, bus: adaptConn(raw, log)}, nil
}

// Watch starts continuous BLE discovery and returns a Watcher that delivers
// fresh vehicle advertisements.
func (c *Conn) Watch(ctx context.Context, adapterID, vin string) (*Watcher, error) {
	return newWatcher(ctx, c.bus, adapterID, vin)
}

// Connect connects to the vehicle. If target is nil, the vehicle's beacon is
// scanned for first. It returns a live connector.Connector.
func (c *Conn) Connect(ctx context.Context, adapterID, vin string, target *ScanResult) (connector.Connector, error) {
	return connect(ctx, c.bus, adapterID, vin, target)
}

// Close releases the system-bus connection.
func (c *Conn) Close() error {
	return c.raw.Close()
}

// ErrNoAdapter means BlueZ has no usable Bluetooth controller.
var ErrNoAdapter = errors.New("bluez: no Bluetooth adapter found")

// IsAdapterError reports whether err is a permanent adapter/radio-level
// failure rather than a transient link or scan condition: a missing
// controller, a radio that is off (and that we may not power on), or
// org.bluez itself being unavailable. Retrying will not fix these, so callers
// surface them immediately instead of burning the remaining deadline.
func IsAdapterError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, ErrNoAdapter) || IsBluetoothOff(err) {
		return true
	}
	switch DBusErrorName(err) {
	case "org.bluez.Error.NotReady", "org.bluez.Error.NotPowered", "org.freedesktop.DBus.Error.ServiceUnknown":
		return true
	}
	return false
}
