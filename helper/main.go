// teslacontrold is a privileged system D-Bus service that runs a bundled,
// setcap'd copy of Tesla's tesla-control/tesla-keygen binaries on behalf of
// the sandboxed harbour-teslacontrol Silica app, which has no CAP_NET_ADMIN
// of its own under Sailjail.
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/godbus/dbus/v5"
	"github.com/godbus/dbus/v5/introspect"
)

const (
	busName    = "org.teslacontrol.Helper"
	objectPath = "/org/teslacontrol/Helper"
	ifaceName  = "org.teslacontrol.Helper1"
)

// binDir holds the bundled, setcap'd tesla-control/tesla-keygen binaries.
// stateDir holds the private key and persisted config; both are created by
// the RPM with restrictive permissions for the service's own system user.
const maxTimeoutSec int32 = 300

var vinRe = regexp.MustCompile(`^[A-HJ-NPR-Z0-9]{17}$`)

var (
	binDir   = envOr("TESLACONTROLD_BIN_DIR", "/opt/teslacontrold/bin")
	stateDir = envOr("TESLACONTROLD_STATE_DIR", "/var/lib/teslacontrold")
)

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

// commandCatalog is every subcommand tesla-control accepts (cmd/tesla-control/commands.go
// upstream). Run() only ever execs one of these - never an arbitrary string - so a
// compromised or buggy caller on the D-Bus session can't smuggle in flags.
var commandCatalog = map[string]bool{}

func init() {
	for _, c := range []string{
		"valet-mode-on", "valet-mode-off", "unlock", "lock", "drive",
		"climate-on", "climate-off", "climate-set-temp",
		"add-key", "add-key-request", "remove-key", "rename-key",
		"list-keys", "honk", "ping", "flash-lights",
		"keep-accessory-power", "low-power-mode",
		"charging-set-limit", "charging-set-amps", "charging-start", "charging-stop",
		"charging-schedule", "charging-schedule-cancel", "charging-schedule-add", "charging-schedule-remove",
		"media-set-volume", "media-volume-up", "media-volume-down",
		"media-next-favorite", "media-next-track", "media-previous-track", "media-previous-favorite",
		"media-toggle-playback",
		"software-update-start", "software-update-cancel",
		"sentry-mode", "wake",
		"tonneau-open", "tonneau-close", "tonneau-stop",
		"trunk-open", "trunk-move", "trunk-close", "frunk-open",
		"charge-port-open", "charge-port-close",
		"autosecure-modelx", "session-info",
		"seat-heater", "steering-wheel-heater",
		"auto-seat-and-climate",
		"windows-vent", "windows-close",
		"body-controller-state",
		"guest-mode-on", "guest-mode-off", "erase-guest-data",
		"parental-controls-on", "parental-controls-off",
		"parental-controls-set-speed-limit", "parental-controls-enable-setting",
		"parental-controls-clear-pin-admin",
		"precondition-schedule-add", "precondition-schedule-remove",
		"product-info", "state",
	} {
		commandCatalog[c] = true
	}
}

type Config struct {
	VIN            string `json:"vin"`
	KeyName        string `json:"key_name"`
	ConnectTimeout int32  `json:"connect_timeout_sec"`
	CommandTimeout int32  `json:"command_timeout_sec"`
}

func defaultConfig() Config {
	return Config{KeyName: "harbour-teslacontrol", ConnectTimeout: 20, CommandTimeout: 5}
}

type Helper struct {
	mu   sync.Mutex
	cfg  Config
	conn *dbus.Conn
}

func newHelper(conn *dbus.Conn) *Helper {
	h := &Helper{cfg: defaultConfig(), conn: conn}
	h.loadConfig()
	return h
}

// allowedCallers is the set of caller binary paths (resolved via
// /proc/<pid>/exe, see authorize) permitted to invoke the privileged
// methods below. This exists because the D-Bus system policy
// (org.teslacontrol.Helper.conf) can only scope access to the
// "defaultuser" Unix account - Sailfish's single-user model - not to a
// specific application, so *any* process running as defaultuser could
// otherwise call these methods directly, bypassing the Sailjail
// permission that's meant to gate harbour-teslacontrol specifically.
//
// Confirmed on-device (Jolla Phone 2026, Sailfish 5.2.0.16): Sailjail
// routes a sandboxed app's *entire* system-bus traffic through a
// dedicated per-app xdg-dbus-proxy process, so GetConnectionCredentials
// for a legitimate sandboxed call resolves to that proxy's PID/exe
// (/usr/bin/xdg-dbus-proxy), never harbour-teslacontrol's own PID -
// confirmed via `busctl list --system` showing the app's connections
// owned by the proxy PID, and via `firejail --debug` showing the proxy's
// filter args correctly include org.teslacontrol.Helper. So the actual
// per-app gate is one layer up: only an app whose .desktop file declares
// the TeslaControlHelper permission gets this bus name added to its own
// proxy's filter at all (verified with `firejail --debug`). This
// allow-list's job is narrower than originally intended - it can't
// distinguish harbour-teslacontrol from any other TeslaControlHelper-
// permitted app, but it still blocks a rogue *unsandboxed* process
// running directly as defaultuser (which connects to the bus directly,
// not through a proxy, so its own exe won't match either entry below).
// A SMACK-label-based per-app check (org.freedesktop.DBus.Error's
// LinuxSecurityLabel field) was considered as a tighter alternative, but
// this device has no smackfs mounted and every process shares the same
// generic SELinux-style context, so no per-app label exists to check.
//
// Resolving the proxy's own exe also requires CAP_SYS_PTRACE, since
// teslacontrold runs as its own unprivileged "teslacontrol" user, and
// reading another UID's /proc/<pid>/exe symlink is denied by the kernel
// without it regardless of Yama ptrace_scope - see
// helper/systemd/teslacontrold.service's AmbientCapabilities.
var allowedCallers = resolveCallerPaths(splitAndTrim(envOr("TESLACONTROLD_ALLOWED_CALLERS", "/usr/bin/harbour-teslacontrol,/usr/bin/xdg-dbus-proxy")))

func splitAndTrim(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func resolveCallerPaths(paths []string) []string {
	resolved := make([]string, 0, len(paths))
	for _, p := range paths {
		if r, err := filepath.EvalSymlinks(p); err == nil {
			resolved = append(resolved, r)
		} else {
			resolved = append(resolved, p)
		}
	}
	return resolved
}

var bleSem = make(chan struct{}, 1)

// pinCommands are subcommands whose arguments carry a secret PIN. Their
// argument lists are redacted from the audit log rather than written to the
// system journal in cleartext.
var pinCommands = map[string]bool{
	"valet-mode-on":                     true,
	"parental-controls-on":              true,
	"parental-controls-off":             true,
	"parental-controls-clear-pin-admin": true,
}

// authorize resolves the D-Bus caller's PID/UID and binary path and checks
// it against allowedCallers, denying by default on any lookup failure.
//
// The PID and UID come from the D-Bus daemon atomically in one
// GetConnectionCredentials reply. The PID is then cross-checked against the
// still-running process's real UID read from /proc: if the caller exited and
// its PID was recycled between the credentials reply and the /proc read, the
// two UIDs will disagree and the call is denied. This closes the PID-reuse
// TOCTOU without comparing against an expected UID (the caller legitimately
// runs as the Sailfish user, not as the teslacontrol service account).
func (h *Helper) authorize(sender dbus.Sender) *dbus.Error {
	var creds map[string]dbus.Variant
	if err := h.conn.BusObject().Call("org.freedesktop.DBus.GetConnectionCredentials", 0, string(sender)).Store(&creds); err != nil {
		return dbus.NewError(ifaceName+".Forbidden", []interface{}{"cannot resolve caller credentials"})
	}
	pidV, pidOk := creds["ProcessID"]
	uidV, uidOk := creds["UnixUserID"]
	if !pidOk || !uidOk {
		return dbus.NewError(ifaceName+".Forbidden", []interface{}{"caller credentials incomplete"})
	}
	pid, pidOk := pidV.Value().(uint32)
	credUID, uidOk := uidV.Value().(uint32)
	if !pidOk || !uidOk {
		return dbus.NewError(ifaceName+".Forbidden", []interface{}{"caller credentials malformed"})
	}

	procUID, err := readProcUid(pid)
	if err != nil {
		return dbus.NewError(ifaceName+".Forbidden", []interface{}{"cannot resolve caller uid"})
	}
	if uint32(procUID) != credUID {
		log.Printf("teslacontrold: rejected call from pid %d: uid mismatch (proc=%d creds=%d)", pid, procUID, credUID)
		return dbus.NewError(ifaceName+".Forbidden", []interface{}{"caller uid mismatch"})
	}
	exe, err := os.Readlink(fmt.Sprintf("/proc/%d/exe", pid))
	if err != nil {
		// Cross-UID /proc/<pid>/exe reads need CAP_SYS_PTRACE (see
		// teslacontrold.service); if that capability grant is ever lost,
		// every caller silently gets rejected here. Log it - this branch
		// used to fail silently, which is exactly what made the original
		// "helper not found" bug invisible in the journal.
		log.Printf("teslacontrold: rejected call from pid %d: cannot resolve caller binary (%v)", pid, err)
		return dbus.NewError(ifaceName+".Forbidden", []interface{}{"cannot resolve caller binary"})
	}
	for _, allowed := range allowedCallers {
		if exe == allowed {
			log.Printf("teslacontrold: authorized call from pid %d (%s)", pid, exe)
			return nil
		}
	}
	log.Printf("teslacontrold: rejected call from pid %d (%s): not in TESLACONTROLD_ALLOWED_CALLERS", pid, exe)
	return dbus.NewError(ifaceName+".Forbidden", []interface{}{"caller not authorized: " + exe})
}

func readProcUid(pid uint32) (int, error) {
	f, err := os.Open(fmt.Sprintf("/proc/%d/status", pid))
	if err != nil {
		return 0, err
	}
	defer f.Close()
	s := bufio.NewScanner(f)
	for s.Scan() {
		line := s.Text()
		if strings.HasPrefix(line, "Uid:") {
			fields := strings.Fields(line)
			if len(fields) < 2 {
				return 0, fmt.Errorf("malformed Uid line")
			}
			return strconv.Atoi(fields[1])
		}
	}
	return 0, fmt.Errorf("Uid line not found")
}

func (h *Helper) configPath() string     { return filepath.Join(stateDir, "config.json") }
func (h *Helper) privateKeyPath() string { return filepath.Join(stateDir, "private_key.pem") }
func (h *Helper) publicKeyPath() string  { return filepath.Join(stateDir, "public_key.pem") }

func (h *Helper) loadConfig() {
	data, err := os.ReadFile(h.configPath())
	if err != nil {
		return
	}
	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		log.Printf("teslacontrold: ignoring unparseable config %s: %v", h.configPath(), err)
		return
	}
	h.cfg = cfg
}

func (h *Helper) saveConfigLocked() error {
	data, err := json.MarshalIndent(h.cfg, "", "  ")
	if err != nil {
		return err
	}
	tmp := h.configPath() + ".tmp"
	if err := os.WriteFile(tmp, data, 0600); err != nil {
		return err
	}
	return os.Rename(tmp, h.configPath())
}

// runBinary execs a bundled binary with a hard deadline, returning combined
// exit status. Never invoked with attacker-controlled binary names.
func runBinary(name string, args []string, timeout time.Duration) (ok bool, stdout string, stderr string, exitCode int32) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	path := filepath.Join(binDir, name)
	cmd := exec.CommandContext(ctx, path, args...)

	var outBuf, errBuf strings.Builder
	cmd.Stdout = &outBuf
	cmd.Stderr = &errBuf

	runErr := cmd.Run()
	stdout, stderr = outBuf.String(), errBuf.String()

	if runErr == nil {
		return true, stdout, stderr, 0
	}
	if exitErr, isExit := runErr.(*exec.ExitError); isExit {
		return false, stdout, stderr, int32(exitErr.ExitCode())
	}
	if ctx.Err() == context.DeadlineExceeded {
		stderr += "\nteslacontrold: timed out waiting for tesla-control"
	}
	return false, stdout, stderr, -1
}

// commonArgs builds the -ble/-vin/-key-file/... flags shared by every
// tesla-control invocation, from the persisted config.
func (h *Helper) commonArgsLocked() ([]string, *dbus.Error) {
	if h.cfg.VIN == "" {
		return nil, dbus.NewError(ifaceName+".NotConfigured", []interface{}{"VIN is not set; call SetConfig first"})
	}
	if _, err := os.Stat(h.privateKeyPath()); err != nil {
		return nil, dbus.NewError(ifaceName+".NoKey", []interface{}{"no private key; call GenerateKey first"})
	}
	return []string{
		"-ble",
		"-keyring-type", "file",
		"-key-file", h.privateKeyPath(),
		"-key-name", h.cfg.KeyName,
		"-vin", h.cfg.VIN,
		"-connect-timeout", fmt.Sprintf("%ds", h.cfg.ConnectTimeout),
		"-command-timeout", fmt.Sprintf("%ds", h.cfg.CommandTimeout),
	}, nil
}

// Run executes a single tesla-control subcommand. cmd must be one of the
// known tesla-control subcommands; args are passed through verbatim as
// positional arguments (never as additional flags).
func (h *Helper) Run(cmd string, args []string, sender dbus.Sender) (bool, string, string, int32, *dbus.Error) {
	if dErr := h.authorize(sender); dErr != nil {
		return false, "", "", -1, dErr
	}
	if !commandCatalog[cmd] {
		return false, "", "", -1, dbus.NewError(ifaceName+".UnknownCommand", []interface{}{cmd})
	}
	for _, a := range args {
		if strings.HasPrefix(a, "-") {
			return false, "", "", -1, dbus.NewError(ifaceName+".InvalidArgument", []interface{}{"arguments may not start with '-': " + a})
		}
	}

	h.mu.Lock()
	common, dErr := h.commonArgsLocked()
	timeout := time.Duration(int64(h.cfg.ConnectTimeout)+int64(h.cfg.CommandTimeout)+10) * time.Second
	h.mu.Unlock()
	if dErr != nil {
		return false, "", "", -1, dErr
	}

	select {
	case bleSem <- struct{}{}:
	default:
		return false, "", "", -1, dbus.NewError(ifaceName+".Busy", []interface{}{"another BLE command is in progress"})
	}
	defer func() { <-bleSem }()

	argv := append(append([]string{}, common...), cmd)
	argv = append(argv, args...)

	if pinCommands[cmd] {
		log.Printf("teslacontrold: Run(%s, [%d redacted args])", cmd, len(args))
	} else {
		log.Printf("teslacontrold: Run(%s, %v)", cmd, args)
	}
	ok, stdout, stderr, exitCode := runBinary("tesla-control", argv, timeout)
	return ok, stdout, stderr, exitCode, nil
}

// GenerateKey creates a new local private key (file-backed - there is no
// Sailfish OS keyring backend) and returns its PEM-encoded public key.
func (h *Helper) GenerateKey(force bool, sender dbus.Sender) (bool, string, string, *dbus.Error) {
	if dErr := h.authorize(sender); dErr != nil {
		return false, "", "", dErr
	}
	h.mu.Lock()
	defer h.mu.Unlock()

	args := []string{"-keyring-type", "file", "-key-file", h.privateKeyPath(), "-output", h.publicKeyPath()}
	if force {
		args = append(args, "-f")
	}
	args = append(args, "create")

	log.Printf("teslacontrold: GenerateKey(force=%v)", force)
	ok, _, stderr, _ := runBinary("tesla-keygen", args, 15*time.Second)
	if !ok {
		return false, "", strings.TrimSpace(stderr), nil
	}
	pub, err := os.ReadFile(h.publicKeyPath())
	if err != nil {
		return false, "", err.Error(), nil
	}
	return true, string(pub), "", nil
}

// Pair enrolls the current public key with the vehicle via BLE, requiring
// physical NFC-card approval at the center console (matches the official
// app's "add key" flow).
func (h *Helper) Pair(sender dbus.Sender) (bool, string, string, *dbus.Error) {
	if dErr := h.authorize(sender); dErr != nil {
		return false, "", "", dErr
	}
	h.mu.Lock()
	common, dErr := h.commonArgsLocked()
	pubkeyPath := h.publicKeyPath()
	h.mu.Unlock()
	if dErr != nil {
		return false, "", "", dErr
	}
	if _, err := os.Stat(pubkeyPath); err != nil {
		return false, "", "no public key on file; call GenerateKey first", nil
	}

	argv := append(append([]string{}, common...), "add-key-request", pubkeyPath, "owner", "cloud_key")
	log.Printf("teslacontrold: Pair()")
	select {
	case bleSem <- struct{}{}:
	default:
		return false, "", "another BLE command is in progress", nil
	}
	defer func() { <-bleSem }()
	ok, stdout, stderr, _ := runBinary("tesla-control", argv, 60*time.Second)
	if !ok {
		return false, stdout, strings.TrimSpace(stderr), nil
	}
	return true, stdout, "", nil
}

// validateConfig returns "" if the inputs are acceptable, else a human-readable
// error message. Shared by SetConfig and unit tests so the bounds can be
// verified without a live D-Bus connection.
func validateConfig(vin string, connectTimeout, commandTimeout int32) string {
	if connectTimeout <= 0 || commandTimeout <= 0 {
		return "timeouts must be positive"
	}
	if connectTimeout > maxTimeoutSec || commandTimeout > maxTimeoutSec {
		return fmt.Sprintf("timeouts must be <= %d", maxTimeoutSec)
	}
	vin = strings.TrimSpace(vin)
	if vin != "" && !vinRe.MatchString(vin) {
		return "invalid VIN format (17 alphanumeric chars, no I/O/Q)"
	}
	return ""
}

func (h *Helper) SetConfig(vin string, keyName string, connectTimeout int32, commandTimeout int32, sender dbus.Sender) (bool, string, *dbus.Error) {
	if dErr := h.authorize(sender); dErr != nil {
		return false, "", dErr
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if msg := validateConfig(vin, connectTimeout, commandTimeout); msg != "" {
		return false, msg, nil
	}
	vin = strings.TrimSpace(vin)
	h.cfg.VIN = vin
	h.cfg.KeyName = strings.TrimSpace(keyName)
	h.cfg.ConnectTimeout = connectTimeout
	h.cfg.CommandTimeout = commandTimeout
	if err := h.saveConfigLocked(); err != nil {
		return false, err.Error(), nil
	}
	log.Printf("teslacontrold: SetConfig(vin=%s, keyName=%q, connectTimeout=%ds, commandTimeout=%ds)", vin, keyName, connectTimeout, commandTimeout)
	return true, "", nil
}

func (h *Helper) GetConfig(sender dbus.Sender) (string, string, int32, int32, bool, string, *dbus.Error) {
	if dErr := h.authorize(sender); dErr != nil {
		return "", "", 0, 0, false, "", dErr
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	hasKey := false
	pub := ""
	if data, err := os.ReadFile(h.publicKeyPath()); err == nil {
		hasKey = true
		pub = string(data)
	}
	return h.cfg.VIN, h.cfg.KeyName, h.cfg.ConnectTimeout, h.cfg.CommandTimeout, hasKey, pub, nil
}

const introspectXML = `
<node>
	<interface name="` + ifaceName + `">
		<method name="Run">
			<arg direction="in" type="s" name="cmd"/>
			<arg direction="in" type="as" name="args"/>
			<arg direction="out" type="b" name="ok"/>
			<arg direction="out" type="s" name="stdout"/>
			<arg direction="out" type="s" name="stderr"/>
			<arg direction="out" type="i" name="exitCode"/>
		</method>
		<method name="GenerateKey">
			<arg direction="in" type="b" name="force"/>
			<arg direction="out" type="b" name="ok"/>
			<arg direction="out" type="s" name="publicKeyPem"/>
			<arg direction="out" type="s" name="errorMessage"/>
		</method>
		<method name="Pair">
			<arg direction="out" type="b" name="ok"/>
			<arg direction="out" type="s" name="output"/>
			<arg direction="out" type="s" name="errorMessage"/>
		</method>
		<method name="SetConfig">
			<arg direction="in" type="s" name="vin"/>
			<arg direction="in" type="s" name="keyName"/>
			<arg direction="in" type="i" name="connectTimeoutSec"/>
			<arg direction="in" type="i" name="commandTimeoutSec"/>
			<arg direction="out" type="b" name="ok"/>
			<arg direction="out" type="s" name="errorMessage"/>
		</method>
		<method name="GetConfig">
			<arg direction="out" type="s" name="vin"/>
			<arg direction="out" type="s" name="keyName"/>
			<arg direction="out" type="i" name="connectTimeoutSec"/>
			<arg direction="out" type="i" name="commandTimeoutSec"/>
			<arg direction="out" type="b" name="hasKey"/>
			<arg direction="out" type="s" name="publicKeyPem"/>
		</method>
	</interface>
</node>`

func main() {
	if err := os.MkdirAll(stateDir, 0700); err != nil {
		log.Fatalf("cannot create state dir %s: %v", stateDir, err)
	}

	conn, err := dbus.ConnectSystemBus()
	if err != nil {
		log.Fatalf("cannot connect to system bus: %v", err)
	}
	defer conn.Close()

	helper := newHelper(conn)
	if err := conn.Export(helper, objectPath, ifaceName); err != nil {
		log.Fatalf("cannot export object: %v", err)
	}
	if err := conn.Export(introspect.Introspectable(introspectXML), objectPath, "org.freedesktop.DBus.Introspectable"); err != nil {
		log.Fatalf("cannot export introspection: %v", err)
	}

	reply, err := conn.RequestName(busName, dbus.NameFlagDoNotQueue)
	if err != nil {
		log.Fatalf("cannot request bus name: %v", err)
	}
	if reply != dbus.RequestNameReplyPrimaryOwner {
		log.Fatalf("bus name %s already taken", busName)
	}

	log.Printf("teslacontrold listening on %s %s", busName, objectPath)
	select {}
}
