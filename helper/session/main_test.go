package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"
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
