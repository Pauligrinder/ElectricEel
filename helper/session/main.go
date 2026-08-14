// tesla-session is a persistent companion to tesla-control: instead of
// connecting, running one command, and exiting (paying a full BLE
// connect+StartSession handshake every time), it holds a live
// *vehicle.Vehicle across many commands and only reconnects after a period
// of inactivity. It's spoken to over stdin/stdout by the in-process Rust
// control core, which falls back to spawning tesla-control directly
// (today's behavior) if this process is unreachable or misbehaves - see
// helper/src/session_client.rs.
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

	"electric-eel-session/bluez"
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
// serialized by mu - the caller (the in-process Rust core, via its own
// ble_sem) never issues concurrent commands, and this process only ever has
// one goroutine
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

	// presenceCancel is non-nil while the presence-maintenance loop (see
	// presenceLoop) is running; presence-start/presence-stop set/clear it.
	presenceCancel context.CancelFunc

	// writeMu serializes stdout writes: dispatch's replies and the presence
	// loop's unsolicited events both encode onto enc from different
	// goroutines, and json.Encoder is not safe for concurrent use.
	writeMu sync.Mutex
	enc     *json.Encoder
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

// commandsWithoutSession lists commands that must connect *without* calling
// StartSession at all. add-key-request is the one command that must work
// before this key is paired with the vehicle, and per the vehicle-command
// source (internal/dispatcher/dispatcher.go's tryStartSession), StartSession
// works by requesting SessionInfo and failing on
// SESSION_INFO_STATUS_KEY_NOT_ON_WHITELIST - which an unrecognized key gets
// for *every* domain once the vehicle already has other keys enrolled, VCSEC
// included. (An earlier fix here tried scoping the request to VCSEC only,
// on the theory VCSEC alone would tolerate an unpaired key; confirmed live
// against a real vehicle that it does not - same rejection.) The reason
// add-key-request works at all is that its handler (SendAddKeyRequestWithRole)
// never goes through the session/dispatcher machinery in the first place -
// it sends a self-identifying, unauthenticated message directly via the raw
// connector. So the fix is to skip StartSession for it entirely, not to
// narrow its domain.
var commandsWithoutSession = map[string]bool{
	"add-key-request": true,
}

// addKeyRequestGracePeriod is how long dispatch holds the BLE connection
// open after a successful add-key-request before disconnecting - see the
// call site in dispatch.
const addKeyRequestGracePeriod = 90 * time.Second

// sessionDomains picks which domains StartSession should handshake with for
// cmd, mirroring upstream's configureFlags (vendored into commands_vendor.go
// but, unlike here, never actually called - see ensureConnectedLocked).
// Requesting every domain (nil) is right for ordinary commands; only
// commands_vendor.go's own domain field (used by body-controller-state)
// narrows it - commandsWithoutSession commands never reach this at all.
func sessionDomains(cmd string) []protocol.Domain {
	if info, ok := commands[cmd]; ok && info.domain != protocol.DomainNone {
		return []protocol.Domain{info.domain}
	}
	return nil
}

// ensureConnectedLocked reuses the live session if there is one; otherwise
// performs the same connect+StartSession sequence
// cli.Config.Connect/ConnectLocal does in upstream tesla-control - except
// for commandsWithoutSession commands, which connect but skip StartSession
// (see that map's doc). cmd selects the StartSession domains via
// sessionDomains; pass "" for a general-purpose (all-domain) session, as
// presenceLoop does.
//
// A connection established for a commandsWithoutSession command is
// deliberately never cached into s.car/s.conn (dispatch tears it down right
// after the command runs, win or lose) - caching it would let a later,
// ordinary command silently reuse a connection that never authenticated,
// instead of the failure that command needs to see.
//
// target, if non-nil, is passed straight through to bluez.Conn.Connect
// instead of letting it scan for the vehicle itself. presenceLoop passes
// its own Watcher's last Peek() result here: without it, an arrival-
// triggered connect would call bluez's own scan()/startDiscovery() while
// presenceLoop's Watcher already has discovery open for RSSI polling,
// colliding with itself - confirmed live ("bluez: start discovery:
// Operation already in progress", repeatedly, since each failed attempt
// reset presenceStep back to "away" and immediately re-triggered arrival).
// Every other caller passes nil (nothing to reuse). Caller holds mu.
func (s *session) ensureConnectedLocked(ctx context.Context, cmd string, target *bluez.ScanResult) error {
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
		if err := s.ensureBluezLocked(); err != nil {
			return err
		}
		conn, err = s.bluez.Connect(connCtx, s.adapterID, s.vin, target)
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
	if !commandsWithoutSession[cmd] {
		if err := car.StartSession(connCtx, sessionDomains(cmd)); err != nil {
			car.Disconnect()
			conn.Close()
			return err
		}
	}

	s.car = car
	s.conn = conn
	return nil
}

// ensureBluezLocked opens the system-bus connection used for the bluez
// backend, if not already open. It's shared by ensureConnectedLocked (one
// GATT connect) and presenceLoop (discovery only, no GATT connect) - both
// need the same lazily-opened, process-lifetime bus handle. Caller holds mu.
func (s *session) ensureBluezLocked() error {
	if s.bluez != nil {
		return nil
	}
	bz, err := bluez.Open()
	if err != nil {
		return fmt.Errorf("open bluez connection: %w", err)
	}
	s.bluez = bz
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

// presenceConfig tunes the presence-maintenance loop's proximity hysteresis.
type presenceConfig struct {
	nearRSSI     int16         // beacon must be at/above this to count as nearby
	nearConfirm  int           // consecutive near samples required before treating the vehicle as present (debounce false triggers on a single strong sample)
	farTimeout   time.Duration // how long the beacon may go unseen/weak before departure is declared
	scanInterval time.Duration
}

func defaultPresenceConfig() presenceConfig {
	return presenceConfig{
		nearRSSI:     -70,
		nearConfirm:  2,
		farTimeout:   30 * time.Second,
		scanInterval: 2 * time.Second,
	}
}

// parsePresenceArgs parses presence-start's optional flags out of the
// request's Args. Never a real CLI (this is driven over stdin by the Rust
// core), but flag.FlagSet gives free -name value/-name=value parsing and
// error text for free.
func parsePresenceArgs(args []string) (presenceConfig, error) {
	cfg := defaultPresenceConfig()
	fs := flag.NewFlagSet("presence-start", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	nearRSSI := int(cfg.nearRSSI)
	fs.IntVar(&nearRSSI, "near-rssi", nearRSSI, "RSSI (dBm) at/above which the vehicle counts as nearby")
	fs.IntVar(&cfg.nearConfirm, "near-confirm", cfg.nearConfirm, "consecutive near samples required before treating the vehicle as present")
	fs.DurationVar(&cfg.farTimeout, "away-timeout", cfg.farTimeout, "how long the beacon may go unseen/weak before departure is declared")
	fs.DurationVar(&cfg.scanInterval, "scan-interval", cfg.scanInterval, "interval between beacon snapshots")
	if err := fs.Parse(args); err != nil {
		return presenceConfig{}, err
	}
	cfg.nearRSSI = int16(nearRSSI)
	return cfg, nil
}

// presenceAction is presenceStep's verdict for the current tick.
type presenceAction int

const (
	presenceActionNone presenceAction = iota
	presenceActionArrive
	presenceActionStayNear
	presenceActionDepart
)

// presenceStep is the pure decision core of the presence loop: given the
// current near/away state and this tick's beacon snapshot, it decides what
// to do and returns the updated state. Kept separate from presenceLoop's I/O
// (BLE scanning, connecting, locking) so the hysteresis logic is
// unit-testable without a real adapter.
//
// Deliberately does not decide to unlock: the vehicle's own passive-entry
// check (run locally when the door handle is pulled) is what authorizes
// unlock, using whatever session is live at that moment - this state
// machine's job on arrival is only to make sure one *is* live. Departure is
// the one point it acts on its own initiative, sending the same "lock" an
// explicit walk-away lock would, mirroring the official app.
func presenceStep(cfg presenceConfig, near bool, consecNear int, lastSeen, now time.Time, seen bool, rssi int16) (nextNear bool, nextConsecNear int, nextLastSeen time.Time, action presenceAction) {
	if seen && rssi >= cfg.nearRSSI {
		lastSeen = now
		consecNear++
	} else {
		consecNear = 0
	}

	switch {
	case !near && consecNear >= cfg.nearConfirm:
		return true, 0, lastSeen, presenceActionArrive
	case near && seen:
		return true, consecNear, lastSeen, presenceActionStayNear
	case near && now.Sub(lastSeen) > cfg.farTimeout:
		return false, 0, lastSeen, presenceActionDepart
	default:
		return near, consecNear, lastSeen, presenceActionNone
	}
}

// event is an unsolicited JSON line the presence loop pushes to the Rust
// core outside the request/response cycle - distinguished from response by
// its "kind" field (response never sets one) so the reader can tell the two
// apart on the same stdout stream. Time is RFC 3339 with sub-second
// precision - added after a live test where a plain log of bare kinds left
// no way to tell whether a handle-pull happened before or after the
// preceding presence_near, which mattered a great deal to what the result
// could be taken to mean.
type event struct {
	Kind  string    `json:"kind"`
	VIN   string    `json:"vin"`
	Time  time.Time `json:"time"`
	Error string    `json:"error,omitempty"`
}

func (s *session) emitEvent(kind string, err error) {
	e := event{Kind: kind, VIN: s.vin, Time: time.Now()}
	if err != nil {
		e.Error = err.Error()
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	if s.enc != nil {
		_ = s.enc.Encode(e)
	}
}

// writeResponse serializes resp to stdout, synchronized with emitEvent so
// the two goroutines that write to the process's single JSON-lines stdout
// (the request loop and the presence loop) never interleave partial writes.
func (s *session) writeResponse(resp response) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	if s.enc != nil {
		_ = s.enc.Encode(resp)
	}
}

// dispatchPresenceStart begins (or no-ops if already running) the presence
// loop. Requires the bluez backend: the raw-HCI backend's InitAdapterWithID
// takes exclusive control of the controller for GATT connects, which is
// incompatible with also running continuous org.bluez discovery for
// proximity polling (see KNOWN_ISSUES.md).
func (s *session) dispatchPresenceStart(req request) response {
	cfg, err := parsePresenceArgs(req.Args)
	if err != nil {
		return response{ID: req.ID, OK: false, Stderr: err.Error() + "\n", ExitCode: 2}
	}
	if s.bleBackend != "bluez" {
		return response{ID: req.ID, OK: false, Stderr: "presence mode requires -ble-backend=bluez\n", ExitCode: 1}
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.presenceCancel != nil {
		return response{ID: req.ID, OK: true, Stdout: "presence mode already running\n", ExitCode: 0}
	}
	ctx, cancel := context.WithCancel(context.Background())
	s.presenceCancel = cancel
	go s.presenceLoop(ctx, cfg)
	return response{ID: req.ID, OK: true, Stdout: "presence mode started\n", ExitCode: 0}
}

func (s *session) dispatchPresenceStop(req request) response {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.stopPresenceLocked()
	return response{ID: req.ID, OK: true, Stdout: "presence mode stopped\n", ExitCode: 0}
}

// stopPresenceLocked cancels the presence loop, if running. Safe to call
// when it isn't. Caller holds mu.
func (s *session) stopPresenceLocked() {
	if s.presenceCancel != nil {
		s.presenceCancel()
		s.presenceCancel = nil
	}
}

// triggerCommand runs cmd against the (lazily connected) session, the same
// connect+execute+reset-idle-timer sequence dispatch uses (captureOutput
// included - a handler's stdout text must not reach the process's real
// stdout here any more than it may from dispatch, since both share that
// JSON-lines stream), for callers that aren't answering a stdin request
// (i.e. presenceLoop's departure action). The command's own stdout/stderr
// text is discarded; callers report success/failure via the returned error.
func (s *session) triggerCommand(ctx context.Context, cmd string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.ensureConnectedLocked(ctx, cmd, nil); err != nil {
		return err
	}
	var execErr error
	captureOutput(func() {
		execErr = execute(ctx, nil, s.car, []string{cmd})
	})
	s.resetIdleTimerLocked()
	return execErr
}

// presenceLoop polls the vehicle's beacon on cfg.scanInterval and drives
// presenceStep, translating its verdicts into the one BLE side effect that
// matters: keeping a live authenticated session up while the vehicle is
// judged nearby (arrival, and every "still near" tick, since idle teardown
// would otherwise reclaim it out from under a stationary phone), and
// releasing it with an explicit lock on departure. Runs until ctx is
// cancelled by stopPresenceLocked.
func (s *session) presenceLoop(ctx context.Context, cfg presenceConfig) {
	defer s.emitEvent("presence_stopped", nil)

	s.mu.Lock()
	err := s.ensureBluezLocked()
	s.mu.Unlock()
	if err != nil {
		s.emitEvent("presence_error", err)
		return
	}

	watchCtx, watchCancel := context.WithTimeout(ctx, s.connectTimeout)
	watcher, err := s.bluez.Watch(watchCtx, s.adapterID, s.vin)
	watchCancel()
	if err != nil {
		s.emitEvent("presence_error", err)
		return
	}
	defer watcher.Stop(context.Background())

	var (
		near       bool
		consecNear int
		lastSeen   time.Time
	)
	ticker := time.NewTicker(cfg.scanInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}

		peekCtx, cancel := context.WithTimeout(ctx, cfg.scanInterval)
		result, err := watcher.Peek(peekCtx)
		cancel()
		if err != nil {
			continue // transient D-Bus hiccup; try again next tick
		}
		var seen bool
		var rssi int16
		if result != nil {
			seen, rssi = true, result.RSSI
		}

		var action presenceAction
		near, consecNear, lastSeen, action = presenceStep(cfg, near, consecNear, lastSeen, time.Now(), seen, rssi)

		switch action {
		case presenceActionArrive:
			connCtx, cancel := context.WithTimeout(ctx, s.connectTimeout)
			s.mu.Lock()
			// result is guaranteed non-nil here: presenceStep only returns
			// Arrive on a tick where seen was true. Passing it as the
			// connect target reuses the Watcher's own discovery instead of
			// triggering a second, colliding one (see ensureConnectedLocked's
			// target doc).
			connErr := s.ensureConnectedLocked(connCtx, "", result) // general-purpose session, not for one specific command
			s.mu.Unlock()
			cancel()
			if connErr != nil {
				// Didn't actually arrive; let the next strong sample retry.
				near, consecNear = false, 0
				s.emitEvent("presence_error", connErr)
				continue
			}
			s.emitEvent("presence_near", nil)
		case presenceActionStayNear:
			s.mu.Lock()
			s.resetIdleTimerLocked()
			s.mu.Unlock()
		case presenceActionDepart:
			cmdCtx, cancel := context.WithTimeout(ctx, s.commandTimeout)
			lockErr := s.triggerCommand(cmdCtx, "lock")
			cancel()
			s.mu.Lock()
			s.teardownLocked()
			s.mu.Unlock()
			if lockErr != nil {
				s.emitEvent("presence_lock_failed", lockErr)
			} else {
				s.emitEvent("presence_far", nil)
			}
		}
	}
}

// dispatch runs one command against the (lazily connected) session and
// captures its output. Mirrors cmd/tesla-control/main.go's runCommand()
// error-text formatting (not vendored - main.go isn't reused wholesale,
// only commands.go is) so switching between this path and the core's
// one-shot subprocess fallback doesn't change what error text reaches the
// app.
func (s *session) dispatch(req request) response {
	// keygen is pure crypto, no BLE session required (or wanted - it must
	// work on first run, before any connection exists). presence-start/-stop
	// manage their own goroutine and locking, independent of the
	// connect+execute path below.
	switch req.Cmd {
	case "keygen":
		s.mu.Lock()
		defer s.mu.Unlock()
		return s.dispatchKeygen(req)
	case "presence-start":
		return s.dispatchPresenceStart(req)
	case "presence-stop":
		return s.dispatchPresenceStop(req)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	connectCtx, cancel := context.WithTimeout(context.Background(), s.connectTimeout)
	connectErr := s.ensureConnectedLocked(connectCtx, req.Cmd, nil)
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

	if commandsWithoutSession[req.Cmd] {
		if execErr == nil {
			// Hold the connection open rather than disconnecting the
			// instant the write succeeds: SendAddKeyRequestWithRole's own
			// doc comment warns a nil return only means the request was
			// transmitted, not that the vehicle did anything with it -
			// confirmed live, a request sent by a process that then
			// immediately disconnected produced no visible NFC-confirmation
			// prompt on the car's screen at all. This also gives the human
			// operator time to physically walk up and tap the card.
			time.Sleep(addKeyRequestGracePeriod)
		}
		// This connection never called StartSession (see
		// commandsWithoutSession) - it must not survive to be reused by a
		// later command that expects an authenticated one.
		s.teardownLocked()
	} else {
		s.resetIdleTimerLocked()
	}

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
// this process's own stdout, which is reserved for the JSON response/event
// stream. Safe only because its two callers - dispatch() and
// triggerCommand() - both hold session.mu for their entire call, so no two
// invocations of captureOutput (which swaps process-wide globals) can ever
// run concurrently; a future caller into execute() that doesn't hold mu
// would silently break this.
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

	// Run the wrapped handler, but capture any panic and let the cleanup
	// below run regardless, so a panicking handler can't leave the
	// process-global os.Stdout/os.Stderr swapped to a pipe (which would
	// silently swallow every later response/event) or strand the reader
	// goroutines.
	var panicValue interface{}
	func() {
		defer func() { panicValue = recover() }()
		f()
	}()

	// Closing the write ends lets the readers hit EOF and finish; the pipe
	// is drained only after f returns so a handler that fills the OS buffer
	// mid-call can't deadlock against us.
	outW.Close()
	errW.Close()
	os.Stdout, os.Stderr = origOut, origErr

	out := <-outCh
	errOut := <-errCh
	_ = outR.Close()
	_ = errR.Close()

	if panicValue != nil {
		panic(panicValue)
	}
	return out, errOut
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
	s.enc = json.NewEncoder(os.Stdout)

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)
	go func() {
		<-sigCh
		s.mu.Lock()
		s.stopPresenceLocked()
		s.teardownLocked()
		s.closeBluezLocked()
		s.mu.Unlock()
		os.Exit(0)
	}()

	scanner := bufio.NewScanner(os.Stdin)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var req request
		if err := json.Unmarshal(line, &req); err != nil {
			s.writeResponse(response{OK: false, Stderr: fmt.Sprintf("tesla-session: malformed request: %s", err), ExitCode: 1})
			continue
		}
		s.writeResponse(s.dispatch(req))
	}

	s.mu.Lock()
	s.stopPresenceLocked()
	s.teardownLocked()
	s.closeBluezLocked()
	s.mu.Unlock()
}
