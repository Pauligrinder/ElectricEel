// tesla-session is a persistent companion to tesla-control: instead of
// connecting, running one command, and exiting (paying a full BLE
// connect+StartSession handshake every time), it holds a live
// *vehicle.Vehicle across many commands and only reconnects after a period
// of inactivity. It's spoken to over stdin/stdout by teslacontrold, which
// falls back to spawning tesla-control directly (today's behavior) if this
// process is unreachable or misbehaves - see helper/src/session_client.rs.
//
// Every command still goes through commands_vendor.go's execute() and
// commands map, unmodified - this file only changes when the BLE
// connect/handshake happens, not what any individual command does.
package main

import (
	"bufio"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/teslamotors/vehicle-command/pkg/connector"
	"github.com/teslamotors/vehicle-command/pkg/connector/ble"
	"github.com/teslamotors/vehicle-command/pkg/protocol"
	"github.com/teslamotors/vehicle-command/pkg/vehicle"

	"teslacontrol-session/bluez"
)

// writeErr is referenced by a couple of commands_vendor.go's handlers
// (list-keys, add-key-request) - identical to upstream cmd/tesla-control/
// main.go's own definition, needed here because main.go itself wasn't
// vendored (only commands.go was; see commands_vendor.go's header).
func writeErr(format string, a ...interface{}) {
	fmt.Fprintf(os.Stderr, format, a...)
	fmt.Fprintf(os.Stderr, "\n")
}

type request struct {
	ID   string   `json:"id"`
	Cmd  string   `json:"cmd"`
	Args []string `json:"args"`
}

type response struct {
	ID       string `json:"id"`
	OK       bool   `json:"ok"`
	Stdout   string `json:"stdout"`
	Stderr   string `json:"stderr"`
	ExitCode int    `json:"exit_code"`
}

// session owns the (possibly absent) live vehicle connection. All access is
// serialized by mu - the caller (teslacontrold, via its own ble_sem) never
// issues concurrent commands, and this process only ever has one goroutine
// (the stdin loop) calling dispatch, but idleTimer's callback runs on its
// own goroutine and must take mu too.
type session struct {
	mu sync.Mutex

	vin            string
	keyFile        string
	adapterID      string
	bleBackend     string
	connectTimeout time.Duration
	commandTimeout time.Duration
	idleTimeout    time.Duration

	car   *vehicle.Vehicle
	conn  connector.Connector
	bluez *bluez.Conn

	idleTimer *time.Timer
}

// teardownLocked closes the live BLE session, if any, so the vehicle (and
// the phone's BLE radio) can go back to sleep. Safe to call when already
// torn down. Caller holds mu.
func (s *session) teardownLocked() {
	if s.idleTimer != nil {
		s.idleTimer.Stop()
		s.idleTimer = nil
	}
	if s.car != nil {
		s.car.Disconnect()
		s.car = nil
	}
	if s.conn != nil {
		s.conn.Close()
		s.conn = nil
	}
}

// closeBluezLocked releases the system-bus connection held for the bluez
// backend (it registers a D-Bus signal channel, so it must be closed exactly
// once for the life of the process, not per connection). Caller holds mu.
func (s *session) closeBluezLocked() {
	if s.bluez != nil {
		_ = s.bluez.Close()
		s.bluez = nil
	}
}

// ensureConnectedLocked reuses the live session if there is one; otherwise
// performs the same connect+StartSession sequence
// cli.Config.Connect/ConnectLocal does in upstream tesla-control, with
// domains left nil (== "all domains") to match every existing command's
// default behavior (only body-controller-state narrows this, and that's
// handled inside execute() via commands_vendor.go, not here). Caller holds
// mu.
func (s *session) ensureConnectedLocked(ctx context.Context) error {
	if s.car != nil {
		return nil
	}

	skey, err := protocol.LoadPrivateKey(s.keyFile)
	if err != nil {
		return fmt.Errorf("load private key: %w", err)
	}

	connCtx, cancel := context.WithTimeout(ctx, s.connectTimeout)
	defer cancel()

	var conn connector.Connector
	if s.bleBackend == "bluez" {
		// org.bluez D-Bus transport: the radio stays owned by the OS
		// Bluetooth stack, so other connections (e.g. a soundbar) survive.
		// The one system-bus connection is opened lazily and reused across
		// reconnects until the process exits (closeBluezLocked).
		if s.bluez == nil {
			bz, err := bluez.Open()
			if err != nil {
				return fmt.Errorf("open bluez connection: %w", err)
			}
			s.bluez = bz
		}
		conn, err = s.bluez.Connect(connCtx, s.adapterID, s.vin, nil)
		if err != nil {
			return err
		}
	} else {
		// Default: upstream go-ble raw HCI. The only path that calls
		// InitAdapterWithID - which brings the controller down and binds an
		// exclusive HCI user channel (see KNOWN_ISSUES.md). This is exactly
		// the behavior the bluez backend exists to avoid.
		if err := ble.InitAdapterWithID(s.adapterID); err != nil {
			return err
		}
		conn, err = ble.NewConnection(connCtx, s.vin)
		if err != nil {
			return err
		}
	}
	car, err := vehicle.NewVehicle(conn, skey, nil)
	if err != nil {
		conn.Close()
		return err
	}
	if err := car.Connect(connCtx); err != nil {
		conn.Close()
		return err
	}
	if err := car.StartSession(connCtx, nil); err != nil {
		car.Disconnect()
		conn.Close()
		return err
	}

	s.car = car
	s.conn = conn
	return nil
}

// resetIdleTimerLocked (re)starts the countdown to teardownLocked. Called
// after every command, successful or not - a command that fails still
// proves the link is alive right now, and a wedged link will surface on
// the *next* attempt regardless of whether idle teardown ran in between.
// Caller holds mu.
func (s *session) resetIdleTimerLocked() {
	if s.idleTimer != nil {
		s.idleTimer.Stop()
	}
	s.idleTimer = time.AfterFunc(s.idleTimeout, func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		s.teardownLocked()
	})
}

// keygen generates (or, when an existing key exists and force is unset,
// re-prints the existing key's) P256 private/public key pair and saves the
// private key to s.keyFile - the same work upstream tesla-keygen's `create`
// subcommand does, so the Rust helper no longer needs to exec a privileged
// binary for key management. The PEM-encoded public key is returned in the
// response's Stdout, matching what tesla-keygen writes to stdout.
//
// No BLE connection is involved; this runs even when the session has never
// connected (and does not need to).
func (s *session) dispatchKeygen(req request) response {
	force := false
	for _, a := range req.Args {
		if a == "-f" {
			force = true
		}
	}

	if !force {
		if skey, err := protocol.LoadPrivateKey(s.keyFile); err == nil {
			if pub, ok := publicKeyPEM(skey); ok {
				return response{ID: req.ID, OK: true, Stdout: pub, ExitCode: 0}
			}
		}
	}

	newKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return response{ID: req.ID, OK: false, Stderr: fmt.Sprintf("Failed to generate private key: %s\n", err), ExitCode: 1}
	}
	scalar := make([]byte, 32)
	newKey.D.FillBytes(scalar)
	skey := protocol.UnmarshalECDHPrivateKey(scalar)
	if skey == nil {
		return response{ID: req.ID, OK: false, Stderr: "failed to build private key from generated scalar\n", ExitCode: 1}
	}
	if err := protocol.SavePrivateKey(skey, s.keyFile); err != nil {
		return response{ID: req.ID, OK: false, Stderr: fmt.Sprintf("Failed to save private key: %s\n", err), ExitCode: 1}
	}
	pub, ok := publicKeyPEM(skey)
	if !ok {
		return response{ID: req.ID, OK: false, Stderr: "Failed to parse key. The keyring may be corrupted. Run with -f to generate new key.\n", ExitCode: 1}
	}
	return response{ID: req.ID, OK: true, Stdout: pub, ExitCode: 0}
}

// publicKeyPEM encodes skey's public half as a PEM "PUBLIC KEY" block,
// mirroring tesla-keygen's printPublicKey.
func publicKeyPEM(skey protocol.ECDHPrivateKey) (string, bool) {
	pkey := ecdsa.PublicKey{Curve: elliptic.P256()}
	pkey.X, pkey.Y = elliptic.Unmarshal(elliptic.P256(), skey.PublicBytes())
	if pkey.X == nil {
		return "", false
	}
	derPublicKey, err := x509.MarshalPKIXPublicKey(&pkey)
	if err != nil {
		return "", false
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: derPublicKey})), true
}

// dispatch runs one command against the (lazily connected) session and
// captures its output. Mirrors cmd/tesla-control/main.go's runCommand()
// error-text formatting (not vendored - main.go isn't reused wholesale,
// only commands.go is) so switching between this path and teslacontrold's
// one-shot subprocess fallback doesn't change what error text reaches the
// app.
func (s *session) dispatch(req request) response {
	// keygen is pure crypto, no BLE session required (or wanted - it must
	// work on first run, before any connection exists).
	if req.Cmd == "keygen" {
		s.mu.Lock()
		defer s.mu.Unlock()
		return s.dispatchKeygen(req)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	connectCtx, cancel := context.WithTimeout(context.Background(), s.connectTimeout)
	connectErr := s.ensureConnectedLocked(connectCtx)
	cancel()
	if connectErr != nil {
		return response{ID: req.ID, OK: false, Stderr: connectErr.Error(), ExitCode: 1}
	}

	cmdCtx, cancel := context.WithTimeout(context.Background(), s.commandTimeout)
	defer cancel()

	args := append([]string{req.Cmd}, req.Args...)
	var execErr error
	stdout, stderr := captureOutput(func() {
		execErr = execute(cmdCtx, nil, s.car, args)
	})

	s.resetIdleTimerLocked()

	if execErr == nil {
		return response{ID: req.ID, OK: true, Stdout: stdout, Stderr: stderr, ExitCode: 0}
	}
	switch {
	case protocol.MayHaveSucceeded(execErr):
		stderr += fmt.Sprintf("Couldn't verify success: %s\n", execErr)
	case errors.Is(execErr, protocol.ErrNoSession):
		stderr += "You must provide a private key with -key-name or -key-file to execute this command\n"
	default:
		stderr += fmt.Sprintf("Failed to execute command: %s\n", execErr)
	}
	return response{ID: req.ID, OK: false, Stdout: stdout, Stderr: stderr, ExitCode: 1}
}

// captureOutput redirects the process-wide os.Stdout/os.Stderr for the
// duration of f, so commands_vendor.go's handlers (which fmt.Println
// straight to them, same as upstream tesla-control) don't collide with
// this process's own stdout, which is reserved for the JSON response
// stream. Safe only because dispatch() is never called concurrently with
// itself (session.mu) or with the main loop's own response write (same
// goroutine, strictly after dispatch returns) - both preconditions hold by
// construction, not by luck, but are worth stating since a future change
// that parallelizes dispatch would silently break this.
func captureOutput(f func()) (stdout, stderr string) {
	origOut, origErr := os.Stdout, os.Stderr
	outR, outW, err := os.Pipe()
	if err != nil {
		f()
		return "", ""
	}
	errR, errW, err := os.Pipe()
	if err != nil {
		outR.Close()
		outW.Close()
		f()
		return "", ""
	}
	os.Stdout, os.Stderr = outW, errW

	outCh := make(chan string, 1)
	errCh := make(chan string, 1)
	go func() { b, _ := io.ReadAll(outR); outCh <- string(b) }()
	go func() { b, _ := io.ReadAll(errR); errCh <- string(b) }()

	f()

	outW.Close()
	errW.Close()
	os.Stdout, os.Stderr = origOut, origErr

	return <-outCh, <-errCh
}

func main() {
	var (
		vin            string
		keyFile        string
		adapterID      string
		bleBackend     string
		connectTimeout time.Duration
		commandTimeout time.Duration
		idleTimeout    time.Duration
	)
	flag.StringVar(&vin, "vin", "", "Vehicle Identification Number (required)")
	flag.StringVar(&keyFile, "key-file", "", "Private key file (required)")
	flag.StringVar(&adapterID, "bt-adapter-id", "", "Bluetooth adapter ID (Linux only)")
	flag.StringVar(&bleBackend, "ble-backend", "hci", "BLE transport backend: hci (go-ble raw HCI) or bluez (org.bluez D-Bus)")
	flag.DurationVar(&connectTimeout, "connect-timeout", 20*time.Second, "Timeout for establishing a connection")
	flag.DurationVar(&commandTimeout, "command-timeout", 5*time.Second, "Timeout for each command sent to the vehicle")
	flag.DurationVar(&idleTimeout, "idle-timeout", 90*time.Second, "Tear down the BLE session after this much inactivity")
	flag.Parse()

	if vin == "" || keyFile == "" {
		fmt.Fprintln(os.Stderr, "tesla-session: -vin and -key-file are required")
		os.Exit(2)
	}
	if bleBackend != "hci" && bleBackend != "bluez" {
		fmt.Fprintf(os.Stderr, "tesla-session: invalid -ble-backend %q (want hci or bluez)\n", bleBackend)
		os.Exit(2)
	}

	s := &session{
		vin:            vin,
		keyFile:        keyFile,
		adapterID:      adapterID,
		bleBackend:     bleBackend,
		connectTimeout: connectTimeout,
		commandTimeout: commandTimeout,
		idleTimeout:    idleTimeout,
	}

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)
	go func() {
		<-sigCh
		s.mu.Lock()
		s.teardownLocked()
		s.closeBluezLocked()
		s.mu.Unlock()
		os.Exit(0)
	}()

	realStdout := os.Stdout
	enc := json.NewEncoder(realStdout)

	scanner := bufio.NewScanner(os.Stdin)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var req request
		if err := json.Unmarshal(line, &req); err != nil {
			enc.Encode(response{OK: false, Stderr: fmt.Sprintf("tesla-session: malformed request: %s", err), ExitCode: 1})
			continue
		}
		enc.Encode(s.dispatch(req))
	}

	s.mu.Lock()
	s.teardownLocked()
	s.closeBluezLocked()
	s.mu.Unlock()
}
