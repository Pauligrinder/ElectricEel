package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Production findings: desired behavior. Each test FAILS now and PASSES
// after the fix. Behavioral where possible; source-symmetric where BLE
// hardware would be required.

func TestProductionFindingsRejectsEmptySeatSelection(t *testing.T) {
	// commands_vendor.go auto-seat-and-climate: len(positions)!=len(arg)
	// passes for empty "" (0==0) and calls the car with empty positions.
	// Rust (protocol_commands.rs) and library/commands.go reject empty.
	// Desired: handler rejects before touching BLE.
	//
	// Call the handler directly to bypass readiness (which needs a real
	// VIN/key): with a nil car, the current code panics instead of
	// returning an error — both outcomes fail this test until validated.
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("handler panicked on empty seat selection (%v); must return an error before BLE", r)
		}
	}()
	err := commands["auto-seat-and-climate"].handler(context.Background(), nil, nil, map[string]string{"POSITIONS": ""})
	if err == nil {
		t.Fatal(`auto-seat-and-climate with POSITIONS="" must fail fast (usage) before BLE; got nil error`)
	}
}

func TestProductionFindingsRejectsEmptyChargeInterval(t *testing.T) {
	// charging-schedule-add with TIME "-" (both START/END empty) is
	// accepted; Rust rejects "empty time interval" and library requires
	// i(4)!=0||i(5)!=0. Desired: vendored handler rejects before BLE.
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("handler panicked on empty charge interval (%v); must return an error before BLE", r)
		}
	}()
	err := commands["charging-schedule-add"].handler(context.Background(), nil, nil, map[string]string{
		"DAYS": "all", "TIME": "-", "LATITUDE": "48.85", "LONGITUDE": "2.35",
	})
	if err == nil {
		t.Fatal(`charging-schedule-add with TIME="-" (empty interval) must fail fast before BLE; got nil error`)
	}
}

func TestProductionFindingsReusedLinkFailureRetiresSession(t *testing.T) {
	// main.go ensureConnectedLocked: the fresh-link StartSession failure
	// calls teardownLocked(); the reused-link failure just returns err,
	// leaving s.car/s.conn cached. After a link drop every later command
	// reuses the dead link until the 90s idle timer fires (extended by
	// each failure's resetIdleTimer). Desired: symmetric teardown.
	//
	// Hardware-free symmetry check: both StartSession call sites must be
	// followed by a teardown. Fails now (reuse path has none), passes
	// once the reuse path retires the zombie (at least on
	// sessionDroppedError, ideally always).
	data, err := os.ReadFile(filepath.Join(".", "main.go"))
	if err != nil {
		t.Fatal(err)
	}
	src := string(data)
	reuseIdx := strings.Index(src, "StartSession additional failed")
	if reuseIdx < 0 {
		t.Fatal("reuse-path StartSession failure marker not found")
	}
	window := src[max(0, reuseIdx-600):min(len(src), reuseIdx+400)]
	if !strings.Contains(window, "teardownLocked") {
		t.Fatalf("reused-link StartSession failure does not retire the session (no teardownLocked near reuse path); zombie link is reused indefinitely")
	}
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
