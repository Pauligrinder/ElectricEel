package main

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestValidateConfig(t *testing.T) {
	validVIN := "5YJ3E1EA0PF000000"
	cases := []struct {
		name            string
		vin             string
		connectTimeout  int32
		commandTimeout  int32
		wantErrContains string
	}{
		{"all valid", validVIN, 20, 5, ""},
		{"zero connect timeout", validVIN, 0, 5, "timeouts must be positive"},
		{"negative command timeout", validVIN, 20, -1, "timeouts must be positive"},
		{"connect timeout too large", validVIN, maxTimeoutSec + 1, 5, "timeouts must be <= 300"},
		{"command timeout too large", validVIN, 20, maxTimeoutSec + 1, "timeouts must be <= 300"},
		{"exactly at max allowed", validVIN, maxTimeoutSec, maxTimeoutSec, ""},
		{"empty VIN clears config", "", 20, 5, ""},
		{"VIN with whitespace trimmed", " " + validVIN + " ", 20, 5, ""},
		{"VIN too short", "5YJ3E1EA0PF00000", 20, 5, "invalid VIN format"},
		{"VIN with letter I", "5YJ3E1EA0PI000000", 20, 5, "invalid VIN format"},
		{"VIN with letter O", "5YJ3E1EA0PO000000", 20, 5, "invalid VIN format"},
		{"VIN with lowercase", "5yj3e1ea0pf000000", 20, 5, "invalid VIN format"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := validateConfig(c.vin, c.connectTimeout, c.commandTimeout)
			if c.wantErrContains == "" && got != "" {
				t.Fatalf("validateConfig(%q) = %q, want no error", c.vin, got)
			}
			if c.wantErrContains != "" && got == "" {
				t.Fatalf("validateConfig(%q) = no error, want containing %q", c.vin, c.wantErrContains)
			}
		})
	}
}

func TestSaveConfigLockedAtomic(t *testing.T) {
	oldStateDir := stateDir
	stateDir = t.TempDir()
	defer func() { stateDir = oldStateDir }()

	h := &Helper{cfg: Config{VIN: "5YJ3E1EA0PF000000", KeyName: "harbour-teslacontrol", ConnectTimeout: 20, CommandTimeout: 5}}

	if err := h.saveConfigLocked(); err != nil {
		t.Fatalf("saveConfigLocked: %v", err)
	}

	data, err := os.ReadFile(h.configPath())
	if err != nil {
		t.Fatalf("config file not written: %v", err)
	}
	if string(data) == "" {
		t.Fatal("config file is empty")
	}

	if _, err := os.Stat(h.configPath() + ".tmp"); !os.IsNotExist(err) {
		t.Fatal("temporary file left behind after rename")
	}

	fi, err := os.Stat(h.configPath())
	if err != nil {
		t.Fatal(err)
	}
	if perm := fi.Mode().Perm(); perm != 0600 {
		t.Fatalf("config file permissions = %v, want 0600", perm)
	}

	reloaded := newHelper(nil)
	reloaded.loadConfig()
	if reloaded.cfg.VIN != h.cfg.VIN || reloaded.cfg.ConnectTimeout != h.cfg.ConnectTimeout {
		t.Fatalf("reloaded config mismatch: %+v", reloaded.cfg)
	}
}

func TestPinCommandsCoverage(t *testing.T) {
	for _, cmd := range []string{"valet-mode-on", "parental-controls-on", "parental-controls-off", "parental-controls-clear-pin-admin"} {
		if !pinCommands[cmd] {
			t.Errorf("pin-bearing command %q not in pinCommands redaction set", cmd)
		}
		if !commandCatalog[cmd] {
			t.Errorf("pin-bearing command %q not in commandCatalog", cmd)
		}
	}
	for _, cmd := range []string{"unlock", "honk", "trunk-open", "ping"} {
		if pinCommands[cmd] {
			t.Errorf("non-secret command %q incorrectly marked as pin-bearing", cmd)
		}
	}
}

func TestSplitAndTrim(t *testing.T) {
	got := splitAndTrim(" /usr/bin/harbour-teslacontrol, /opt/foo ,, , /x ")
	want := []string{"/usr/bin/harbour-teslacontrol", "/opt/foo", "/x"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("splitAndTrim = %v, want %v", got, want)
	}
}

func TestResolveCallerPaths(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "target")
	link := filepath.Join(dir, "link")
	if err := os.WriteFile(target, []byte("x"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}

	got := resolveCallerPaths([]string{link, "/nonexistent/definitely/missing"})
	want := []string{target, "/nonexistent/definitely/missing"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("resolveCallerPaths = %v, want %v", got, want)
	}
}
