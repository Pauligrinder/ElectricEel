package library

import (
	"context"
	"crypto/ecdh"
	"errors"
	"fmt"
	"runtime/debug"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode"

	"electric-eel-session/bluez"
	"electric-eel-session/internal/phoneprotocol"
	"github.com/godbus/dbus"
	"github.com/teslamotors/vehicle-command/pkg/connector"
	"github.com/teslamotors/vehicle-command/pkg/protocol"
	"github.com/teslamotors/vehicle-command/pkg/vehicle"
	"google.golang.org/protobuf/encoding/protojson"
)

// Tesla is accessed exclusively by Session's operation worker. It contains no
// proximity controller, idle timer, key files, subprocesses or retry schedule.
type Tesla struct {
	vin                string
	adapter            string
	key                protocol.ECDHPrivateKey
	bus                *bluez.Conn
	conn               connector.Connector
	car                *vehicle.Vehicle
	root               context.Context
	emit               func(Observation)
	fault              func(string)
	watcher            *bluez.Watcher
	watchCancel        context.CancelFunc
	watchDone          chan struct{}
	linkCancel         context.CancelFunc
	linkDone           chan struct{}
	linkActive         atomic.Bool
	passive            atomic.Bool
	targetMu           sync.Mutex
	watchControl       sync.Mutex
	target             uint64
	lastTarget         *bluez.ScanResult
	targetAt           time.Time
	targets            map[uint64]discoveryTarget
	diagnosticAttached bool
}

type discoveryTarget struct {
	result *bluez.ScanResult
	at     time.Time
}

func (t *Tesla) Bind(ctx context.Context, emit func(Observation), fault func(string)) {
	t.root, t.emit, t.fault = ctx, emit, fault
}

func (t *Tesla) backgroundGuard() {
	if value := recover(); value != nil && t.fault != nil {
		t.fault(fmt.Sprintf("Tesla background panic=%v\n%s", value, debug.Stack()))
	}
}

func NewTesla(vin, adapter string, scalar []byte) (*Tesla, error) {
	if len(vin) != 17 || strings.ContainsFunc(vin, func(r rune) bool { return r < '0' || r > 'Z' || (r > '9' && r < 'A') }) {
		return nil, errors.New("VIN must contain 17 uppercase ASCII letters/digits")
	}
	if len(scalar) != 32 {
		return nil, errors.New("P-256 private scalar must contain 32 bytes")
	}
	if _, err := ecdh.P256().NewPrivateKey(scalar); err != nil {
		return nil, err
	}
	key := protocol.UnmarshalECDHPrivateKey(scalar)
	if key == nil {
		return nil, errors.New("invalid P-256 private scalar")
	}
	return &Tesla{vin: vin, adapter: adapter, key: key}, nil
}

func classify(err error) Result {
	if err == nil {
		return Result{Status: OK}
	}
	status := Failed
	var dbError dbus.Error
	var dbErrorPointer *dbus.Error
	disconnected := (errors.As(err, &dbError) && (dbError.Name == "org.bluez.Error.NotConnected" || dbError.Name == "org.freedesktop.DBus.Error.UnknownObject")) || (errors.As(err, &dbErrorPointer) && dbErrorPointer != nil && (dbErrorPointer.Name == "org.bluez.Error.NotConnected" || dbErrorPointer.Name == "org.freedesktop.DBus.Error.UnknownObject"))
	switch {
	case protocol.MayHaveSucceeded(err):
		status = Uncertain
	case errors.Is(err, context.Canceled):
		status = Cancelled
	case errors.Is(err, context.DeadlineExceeded):
		status = TimedOut
	case errors.Is(err, protocol.ErrNotConnected) || disconnected:
		status = NotConnected
	case bluez.IsBluetoothOff(err):
		status = BluetoothOff
	case errors.Is(err, protocol.ErrNoSession):
		status = AuthenticationFailed
	}
	return Result{Status: status, Error: err.Error()}
}

func (t *Tesla) disconnect() {
	if t.linkCancel != nil {
		t.linkCancel()
		<-t.linkDone
		t.linkCancel = nil
	}
	if t.car != nil {
		t.car.Disconnect()
		t.car = nil
	}
	if t.conn != nil {
		t.conn.Close()
		t.conn = nil
	}
	t.linkActive.Store(false)
}

func (t *Tesla) Close() {
	t.detachDiagnostics()
	t.stopWatch()
	t.disconnect()
	if t.bus != nil {
		_ = t.bus.Close()
		t.bus = nil
	}
	t.key = nil
}

func (t *Tesla) connect(ctx context.Context, domain uint32, token uint64) error {
	if t.car != nil && !t.linkActive.Load() {
		t.disconnect()
	}
	var domains []protocol.Domain
	switch domain {
	case 0: // Explicit unauthenticated enrollment connection.
	case 1:
		domains = []protocol.Domain{protocol.DomainVCSEC}
	case 2:
		domains = []protocol.Domain{protocol.DomainInfotainment}
	default:
		return errors.New("invalid authentication domain")
	}
	if t.car == nil {
		if t.bus == nil {
			bus, err := bluez.Open()
			if err != nil {
				return err
			}
			t.bus = bus
		}
		var target *bluez.ScanResult
		if t.watcher != nil {
			t.targetMu.Lock()
			if token != 0 {
				record, ok := t.targets[token]
				if ok && time.Since(record.at) <= 5*time.Second {
					target = record.result
				}
			} else if t.lastTarget != nil && time.Since(t.targetAt) <= 5*time.Second {
				target = t.lastTarget
			}
			t.targetMu.Unlock()
			if target == nil {
				return errors.New("no fresh advertisement target")
			}
		}
		t.watchControl.Lock()
		t.linkActive.Store(true)
		if t.watcher != nil {
			t.watcher.Pause(ctx)
		}
		t.watchControl.Unlock()
		conn, err := t.bus.Connect(ctx, t.adapter, t.vin, target)
		if err != nil {
			t.linkActive.Store(false)
			return err
		}
		t.conn = conn
		car, err := vehicle.NewVehicle(conn, t.key, nil)
		if err != nil {
			t.disconnect()
			return err
		}
		t.car = car
		if err := car.Connect(ctx); err != nil {
			t.disconnect()
			return err
		}
		t.startLinkObserver()
		if bz, ok := conn.(*bluez.Connection); ok {
			_ = bz.SetTrusted(true)
			_ = bz.SetAutoConnect(false)
		}
	}
	if len(domains) > 0 {
		if err := t.car.StartSession(ctx, domains); err != nil {
			t.disconnect()
			return err
		}
	}
	return nil
}

func (t *Tesla) Execute(ctx context.Context, req Request) Result {
	if !t.diagnosticAttached {
		t.attachDiagnostics()
		t.diagnosticAttached = true
	}
	if err := ctx.Err(); err != nil {
		return classify(err)
	}
	switch req.Operation {
	case PublicKey:
		return Result{Status: OK, Payload: append([]byte(nil), t.key.PublicBytes()...)}
	case WatchStart:
		return classify(t.startWatch(ctx))
	case WatchStop:
		t.stopWatch()
		return Result{Status: OK}
	case PassiveEntry:
		t.passive.Store(req.Domain != 0)
		return Result{Status: OK}
	case EnrollmentStatus:
		return t.enrollment(ctx, req)
	case VehicleCommand:
		return t.command(ctx, req)
	case Disconnect:
		t.disconnect()
		return Result{Status: OK}
	case Connect:
		if req.Domain > 2 {
			return Result{Status: Invalid, Error: "invalid authentication domain"}
		}
		if req.Target != 0 {
			t.targetMu.Lock()
			record, exists := t.targets[req.Target]
			valid := exists && time.Since(record.at) <= 5*time.Second
			t.targetMu.Unlock()
			if !valid {
				return Result{Status: Invalid, Error: "stale discovery target"}
			}
		}
		return classify(t.connect(ctx, req.Domain, req.Target))
	case NavigateGPS:
		if !phoneprotocol.ValidLatLon(req.Latitude, req.Longitude) {
			return Result{Status: Invalid, Error: "invalid coordinates"}
		}
	case NavigateAddress:
		if strings.TrimSpace(req.Text) == "" || len([]rune(req.Text)) > 2000 || strings.ContainsFunc(req.Text, unicode.IsControl) {
			return Result{Status: Invalid, Error: "invalid destination text"}
		}
	case BodyControllerState:
	default:
		return Result{Status: Invalid, Error: "unsupported operation"}
	}
	if t.car == nil {
		return Result{Status: NotConnected, Error: "connect before executing a vehicle operation"}
	}
	var result Result
	switch req.Operation {
	case BodyControllerState:
		if err := t.car.StartSession(ctx, []protocol.Domain{protocol.DomainVCSEC}); err != nil {
			result = classify(err)
			break
		}
		state, err := t.car.BodyControllerState(ctx)
		result = classify(err)
		if err == nil {
			payload, err := (protojson.MarshalOptions{EmitUnpopulated: true}).Marshal(state)
			result = classify(err)
			result.Payload = payload
		}
	case NavigateGPS, NavigateAddress:
		if err := t.car.StartSession(ctx, []protocol.Domain{protocol.DomainInfotainment}); err != nil {
			result = classify(err)
			break
		}
		if req.Operation == NavigateGPS {
			result = classify(t.car.NavigateToGPS(ctx, req.Latitude, req.Longitude, 0))
		} else {
			result = classify(t.car.NavigateToDestination(ctx, req.Text))
		}
	}
	if result.Status == Uncertain || result.Status == Cancelled || result.Status == TimedOut || result.Status == NotConnected {
		t.disconnect() // Do not reuse an uncertain/cancelled operation's link.
	}
	return result
}
