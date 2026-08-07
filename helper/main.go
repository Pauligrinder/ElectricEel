// teslacontrold is a privileged system D-Bus service that runs a bundled,
// setcap'd copy of Tesla's tesla-control/tesla-keygen binaries on behalf of
// the sandboxed harbour-teslacontrol Silica app, which has no CAP_NET_ADMIN
// of its own under Sailjail.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
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
// This narrows that to processes whose own binary matches the allow-list;
// it does NOT fully close the gap if Sailjail proxies the sandboxed app's
// system-bus traffic through a shared process (unverified on real
// hardware - see KNOWN_ISSUES.md).
var allowedCallers = splitAndTrim(envOr("TESLACONTROLD_ALLOWED_CALLERS", "/usr/bin/harbour-teslacontrol"))

func splitAndTrim(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// authorize resolves the D-Bus caller's PID and binary path and checks it
// against allowedCallers, denying by default on any lookup failure.
func (h *Helper) authorize(sender dbus.Sender) *dbus.Error {
	var pid uint32
	if err := h.conn.BusObject().Call("org.freedesktop.DBus.GetConnectionUnixProcessID", 0, string(sender)).Store(&pid); err != nil {
		return dbus.NewError(ifaceName+".Forbidden", []interface{}{"cannot resolve caller pid"})
	}
	exe, err := os.Readlink(fmt.Sprintf("/proc/%d/exe", pid))
	if err != nil {
		return dbus.NewError(ifaceName+".Forbidden", []interface{}{"cannot resolve caller binary"})
	}
	for _, allowed := range allowedCallers {
		if exe == allowed {
			return nil
		}
	}
	log.Printf("teslacontrold: rejected call from pid %d (%s): not in TESLACONTROLD_ALLOWED_CALLERS", pid, exe)
	return dbus.NewError(ifaceName+".Forbidden", []interface{}{"caller not authorized: " + exe})
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
	if json.Unmarshal(data, &cfg) == nil {
		h.cfg = cfg
	}
}

func (h *Helper) saveConfigLocked() error {
	data, err := json.MarshalIndent(h.cfg, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(h.configPath(), data, 0600)
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
	timeout := time.Duration(h.cfg.ConnectTimeout+h.cfg.CommandTimeout+10) * time.Second
	h.mu.Unlock()
	if dErr != nil {
		return false, "", "", -1, dErr
	}

	argv := append(append([]string{}, common...), cmd)
	argv = append(argv, args...)

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
	ok, stdout, stderr, _ := runBinary("tesla-control", argv, 60*time.Second)
	if !ok {
		return false, stdout, strings.TrimSpace(stderr), nil
	}
	return true, stdout, "", nil
}

func (h *Helper) SetConfig(vin string, keyName string, connectTimeout int32, commandTimeout int32, sender dbus.Sender) (bool, string, *dbus.Error) {
	if dErr := h.authorize(sender); dErr != nil {
		return false, "", dErr
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if connectTimeout <= 0 || commandTimeout <= 0 {
		return false, "timeouts must be positive", nil
	}
	h.cfg.VIN = strings.TrimSpace(vin)
	h.cfg.KeyName = strings.TrimSpace(keyName)
	h.cfg.ConnectTimeout = connectTimeout
	h.cfg.CommandTimeout = commandTimeout
	if err := h.saveConfigLocked(); err != nil {
		return false, err.Error(), nil
	}
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
