package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/teslamotors/vehicle-command/pkg/protocol"
)

// execute() and the commands map are vendored verbatim from upstream (see
// commands_vendor.go) - these tests exercise the same readiness-check code
// path production traffic goes through, without needing a live vehicle,
// the same spirit as this project's existing helper/src tests ("Run
// (unknown command, ...)" against a fake tesla-control stand-in rather than
// a real BLE device).
func TestExecuteReadinessChecks(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	cases := []struct {
		name    string
		args    []string
		wantErr error
	}{
		{"unknown command", []string{"definitely-not-a-command"}, ErrUnknownCommand},
		{"missing VIN (no car)", []string{"lock"}, ErrRequiresVIN},
		{"FleetAPI-only command with no account", []string{"product-info"}, ErrRequiresOAuth},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := execute(ctx, nil, nil, c.args)
			if !errors.Is(err, c.wantErr) {
				t.Errorf("execute(%v) = %v, want %v", c.args, err, c.wantErr)
			}
		})
	}
}

func TestCaptureOutputIsolatesAndRestores(t *testing.T) {
	origOut, origErr := os.Stdout, os.Stderr

	stdout, stderr := captureOutput(func() {
		fmt.Println("hello stdout")
		fmt.Fprintln(os.Stderr, "hello stderr")
	})

	if !strings.Contains(stdout, "hello stdout") {
		t.Errorf("stdout = %q, want it to contain %q", stdout, "hello stdout")
	}
	if !strings.Contains(stderr, "hello stderr") {
		t.Errorf("stderr = %q, want it to contain %q", stderr, "hello stderr")
	}
	if os.Stdout != origOut || os.Stderr != origErr {
		t.Fatal("captureOutput did not restore os.Stdout/os.Stderr")
	}

	// A second call must not see leftover state from the first (guards
	// against the pipe-closing/goroutine-draining logic leaking a stale
	// reader across calls).
	stdout2, _ := captureOutput(func() { fmt.Println("second call") })
	if strings.Contains(stdout2, "hello stdout") {
		t.Errorf("second capture leaked first call's output: %q", stdout2)
	}
}

func TestEnsureConnectedMissingKeyFile(t *testing.T) {
	s := &session{
		vin:            "5YJ3E1EA0PF000000",
		keyFile:        "/nonexistent/path/private_key.pem",
		connectTimeout: 2 * time.Second,
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	err := s.ensureConnectedLocked(context.Background())
	if err == nil {
		t.Fatal("expected an error loading a private key that doesn't exist, got nil")
	}
	if s.car != nil {
		t.Error("session.car should remain nil after a failed connect")
	}
}

func TestDispatchReportsConnectFailure(t *testing.T) {
	// No real adapter/vehicle in this environment - this is exactly the
	// "can't verify without hardware" boundary. What *is* verifiable here:
	// a connect failure is surfaced as a well-formed, non-panicking
	// response rather than as a hang or a crash.
	s := &session{
		vin:            "5YJ3E1EA0PF000000",
		keyFile:        "/nonexistent/path/private_key.pem",
		connectTimeout: 500 * time.Millisecond,
		commandTimeout: 500 * time.Millisecond,
		idleTimeout:    time.Second,
	}
	resp := s.dispatch(request{ID: "t1", Cmd: "lock"})
	if resp.OK {
		t.Fatal("expected ok=false with no reachable vehicle")
	}
	if resp.ID != "t1" {
		t.Errorf("response ID = %q, want %q (request/response correlation)", resp.ID, "t1")
	}
	if resp.Stderr == "" {
		t.Error("expected a non-empty stderr explaining the failure")
	}
}

func TestRequestResponseJSONRoundTrip(t *testing.T) {
	req := request{ID: "abc", Cmd: "seat-heater", Args: []string{"front-left", "2"}}
	data, err := json.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}
	var got request
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, req) {
		t.Errorf("round-trip mismatch: got %+v, want %+v", got, req)
	}

	resp := response{ID: "abc", OK: true, Stdout: "ok", ExitCode: 0}
	data, err = json.Marshal(resp)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"exit_code":0`) {
		t.Errorf("expected snake_case exit_code field in JSON, got %s", data)
	}
}

// TestKeygenCreatesLoadableKey exercises the Phase 3 keygen request end to
// end without any BLE involvement: a fresh key is generated and saved, its
// public half is returned as PEM on Stdout, and the saved private key loads
// back through protocol.LoadPrivateKey (the same call the connection path
// uses).
func TestKeygenCreatesLoadableKey(t *testing.T) {
	dir := t.TempDir()
	keyFile := filepath.Join(dir, "private_key.pem")
	s := &session{keyFile: keyFile}

	resp := s.dispatch(request{ID: "k1", Cmd: "keygen"})
	if !resp.OK {
		t.Fatalf("keygen failed: %s", resp.Stderr)
	}
	if resp.ExitCode != 0 {
		t.Errorf("exit_code = %d, want 0", resp.ExitCode)
	}
	if resp.ID != "k1" {
		t.Errorf("response ID = %q, want %q", resp.ID, "k1")
	}
	pubPEM := strings.TrimSpace(resp.Stdout)
	if !strings.Contains(pubPEM, "BEGIN PUBLIC KEY") {
		t.Errorf("stdout is not a PEM public key:\n%s", resp.Stdout)
	}

	// The public key written must round-trip through LoadPublicKey.
	pubPath := filepath.Join(dir, "public_key.pem")
	if err := os.WriteFile(pubPath, []byte(pubPEM+"\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := protocol.LoadPublicKey(pubPath); err != nil {
		t.Errorf("generated public key does not parse: %v", err)
	}

	// The private key must load back for the connection path.
	if _, err := protocol.LoadPrivateKey(keyFile); err != nil {
		t.Errorf("generated private key does not load: %v", err)
	}
	// And be private (0600, matching tesla-keygen's file keyring write).
	info, err := os.Stat(keyFile)
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0600 {
		t.Errorf("private key permissions = %o, want 600", perm)
	}
}

// TestKeygenWithoutForceReusesExistingKey verifies the -f/force semantics of
// tesla-keygen create: with no force, an existing key is not overwritten,
// just re-printed; with -f it is regenerated (so the public key changes).
func TestKeygenForceSemantics(t *testing.T) {
	dir := t.TempDir()
	keyFile := filepath.Join(dir, "private_key.pem")
	s := &session{keyFile: keyFile}

	first := s.dispatch(request{ID: "k1", Cmd: "keygen"})
	if !first.OK {
		t.Fatalf("first keygen failed: %s", first.Stderr)
	}

	second := s.dispatch(request{ID: "k2", Cmd: "keygen"})
	if !second.OK {
		t.Fatalf("second keygen failed: %s", second.Stderr)
	}
	if second.Stdout != first.Stdout {
		t.Errorf("non-forced keygen regenerated the key; public key changed:\n%q\nvs\n%q", first.Stdout, second.Stdout)
	}

	forced := s.dispatch(request{ID: "k3", Cmd: "keygen", Args: []string{"-f"}})
	if !forced.OK {
		t.Fatalf("forced keygen failed: %s", forced.Stderr)
	}
	if forced.Stdout == first.Stdout {
		t.Error("forced keygen (-f) returned the same public key - it must regenerate")
	}
}

// TestKeygenReportsMalformedKeyWithoutForce ensures a corrupt existing key
// doesn't wedge keygen: without -f it regenerates rather than erroring (and
// without panicking), mirroring upstream's create fall-through.
func TestKeygenRecoversFromCorruptKey(t *testing.T) {
	dir := t.TempDir()
	keyFile := filepath.Join(dir, "private_key.pem")
	if err := os.WriteFile(keyFile, []byte("garbage\n"), 0644); err != nil {
		t.Fatal(err)
	}
	s := &session{keyFile: keyFile}

	resp := s.dispatch(request{ID: "k1", Cmd: "keygen"})
	if !resp.OK {
		t.Fatalf("keygen with corrupt existing key failed: %s", resp.Stderr)
	}
	if _, err := protocol.LoadPrivateKey(keyFile); err != nil {
		t.Errorf("regenerated key does not load: %v", err)
	}
}
