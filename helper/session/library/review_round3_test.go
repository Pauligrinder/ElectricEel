package library

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/teslamotors/vehicle-command/pkg/connector"
	"github.com/teslamotors/vehicle-command/pkg/protocol"
	universal "github.com/teslamotors/vehicle-command/pkg/protocol/protobuf/universalmessage"
	"github.com/teslamotors/vehicle-command/pkg/vehicle"
	"google.golang.org/protobuf/proto"
)

// Review round 3. These tests FAIL on the current code and PASS once fixed.

// TestReviewRound3KeyNotPairedIsAuthenticationFailure:
//
// Bug: classify maps protocol.ErrNoSession (a local "you forgot to call
// StartSession" programming state) to AuthenticationFailed, but the error the
// vehicle actually produces when it rejects our key - protocol.ErrKeyNotPaired,
// returned by the handshake for UNKNOWN_KEY_ID / KEY_NOT_ON_WHITELIST - falls
// through to the generic Failed. EE_AUTHENTICATION_FAILED is therefore
// unreachable for the real "key removed from the car" case, and Rust cannot
// tell "re-pair needed" from an arbitrary failure.
//
// Desired: a vehicle rejection of the key reports AuthenticationFailed.
func TestReviewRound3KeyNotPairedIsAuthenticationFailure(t *testing.T) {
	if got := classify(protocol.ErrKeyNotPaired).Status; got != AuthenticationFailed {
		t.Fatalf("classify(ErrKeyNotPaired) = %d, want AuthenticationFailed (%d)", got, AuthenticationFailed)
	}
}

// TestReviewRound3SharedLibraryDoesNotLinkRawHCIStack:
//
// Bug: docs/architecture.md says the legacy raw-HCI code is excluded from the
// Harbour build, but bluez/scan.go imports vehicle-command's
// pkg/connector/ble solely for the three-line VehicleLocalName helper. That
// drags go-ble's whole raw HCI socket stack (linux/hci/socket, att, gatt) and
// mgutz/logxi into libteslable.so; logxi's init() runs inside the Qt app
// process when the library is loaded (isatty/env probing, stdout logger).
//
// Desired: cmd/teslable's dependency closure contains no go-ble package
// (compute the beacon name locally: "S" + hex(sha1(vin)[:8]) + "C", keeping
// TestVehicleBeaconNameMatchesUpstream as the drift guard in tests only).
func TestReviewRound3SharedLibraryDoesNotLinkRawHCIStack(t *testing.T) {
	goTool, err := exec.LookPath("go")
	if err != nil {
		t.Skip("go tool not available")
	}
	command := exec.Command(goTool, "list", "-deps", "electric-eel-session/cmd/teslable")
	command.Env = append(os.Environ(), "CGO_ENABLED=1")
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("go list: %v\n%s", err, output)
	}
	var linked []string
	for _, pkg := range strings.Fields(string(output)) {
		if strings.HasPrefix(pkg, "github.com/go-ble/") || strings.HasPrefix(pkg, "github.com/mgutz/logxi") {
			linked = append(linked, pkg)
		}
	}
	if len(linked) > 0 {
		t.Fatalf("libteslable.so links the raw-HCI BLE stack: %s", strings.Join(linked, ", "))
	}
}

// replyingConnector is an in-memory vehicle link. Every request is answered
// immediately with a RoutableMessage carrying fault, so the link is
// demonstrably alive and responsive.
type replyingConnector struct {
	fault  universal.MessageFault_E
	inbox  chan []byte
	mu     sync.Mutex
	closed bool
}

func (c *replyingConnector) Receive() <-chan []byte { return c.inbox }
func (c *replyingConnector) VIN() string            { return "5YJ3E1EA7KF000001" }
func (c *replyingConnector) PreferredAuthMethod() connector.AuthMethod {
	return connector.AuthMethodGCM
}
func (c *replyingConnector) RetryInterval() time.Duration  { return 50 * time.Millisecond }
func (c *replyingConnector) AllowedLatency() time.Duration { return 4 * time.Second }
func (c *replyingConnector) Close() {
	c.mu.Lock()
	c.closed = true
	c.mu.Unlock()
}
func (c *replyingConnector) isClosed() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.closed
}
func (c *replyingConnector) Send(_ context.Context, buffer []byte) error {
	var request universal.RoutableMessage
	if err := proto.Unmarshal(buffer, &request); err != nil {
		return err
	}
	reply, err := proto.Marshal(&universal.RoutableMessage{
		FromDestination: &universal.Destination{SubDestination: &universal.Destination_Domain{Domain: request.GetToDestination().GetDomain()}},
		ToDestination:   &universal.Destination{SubDestination: &universal.Destination_RoutingAddress{RoutingAddress: request.GetFromDestination().GetRoutingAddress()}},
		RequestUuid:     request.GetUuid(),
		SignedMessageStatus: &universal.MessageStatus{
			OperationStatus:    universal.OperationStatus_E_OPERATIONSTATUS_ERROR,
			SignedMessageFault: c.fault,
		},
	})
	if err != nil {
		return err
	}
	select {
	case c.inbox <- reply:
	default:
	}
	return nil
}

// TestReviewRound3RejectedInfotainmentHandshakeKeepsLivePresenceLink:
//
// Bug: Tesla.connect tears the whole GATT link down (t.disconnect) whenever
// StartSession fails on an already-connected, reused link. Phone-key presence
// keeps that link up with VCSEC authenticated so the car's
// AuthenticationRequests (handle-pull unlock) are answered. Any foreground
// infotainment command or dashboard state query issued while the
// infotainment domain refuses or does not answer the handshake (asleep, older
// firmware, INVALID_DOMAINS ...) therefore kills the passive-entry link,
// without even emitting EE_DISCONNECTED; presence must then rescan/reconnect,
// and repeated dashboard refreshes make it flap.
//
// The vehicle answered the handshake, so the transport is healthy and nothing
// was left half-written. Desired: a handshake rejected by the vehicle on a
// reused link reports the failure but keeps the link and its VCSEC session
// (only transport-level loss/uncertainty retires the link; the link observer
// already detects real drops).
func TestReviewRound3RejectedInfotainmentHandshakeKeepsLivePresenceLink(t *testing.T) {
	scalar := make([]byte, 32)
	scalar[31] = 1
	backend, err := NewTesla("5YJ3E1EA7KF000001", "", scalar)
	if err != nil {
		t.Fatal(err)
	}
	link := &replyingConnector{fault: universal.MessageFault_E_MESSAGEFAULT_ERROR_INVALID_DOMAINS, inbox: make(chan []byte, 8)}
	car, err := vehicle.NewVehicle(link, backend.key, nil)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := car.Connect(ctx); err != nil {
		t.Fatal(err)
	}
	// State of a live presence link (as established by Connect(VCSEC)).
	backend.conn, backend.car = link, car
	backend.linkActive.Store(true)
	defer backend.disconnect()

	result := backend.Execute(ctx, Request{ID: 1, Operation: Connect, Domain: 2, Timeout: time.Second})
	if result.Status == OK {
		t.Fatal("rejected infotainment handshake reported success")
	}
	if backend.car == nil || link.isClosed() || !backend.linkActive.Load() {
		t.Fatalf("infotainment handshake rejection (%d: %s) tore down the live VCSEC/passive-entry link", result.Status, result.Error)
	}
}
