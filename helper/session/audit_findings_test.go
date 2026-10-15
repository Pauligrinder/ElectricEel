package main

import (
	"testing"
)

// Production audit findings — regression tests that FAIL on the current code
// and PASS once the underlying logic bug is fixed.

func TestAuditGetDegreeRejectsOutOfRangeLatitude(t *testing.T) {
	// GetDegree is the legacy latitude entry point (thirdparty still routes
	// LATITUDE through it). It delegates to GetLongitude, so a latitude of
	// 100 passes the 180-degree longitude limit and would be sent to the
	// vehicle as a valid coordinate. Latitudes must stay in [-90, 90].
	if _, err := GetDegree("100"); err == nil {
		t.Fatal(`GetDegree("100") succeeded; latitude 100 must be rejected (valid range [-90, 90])`)
	}
	if _, err := GetDegree("-91"); err == nil {
		t.Fatal(`GetDegree("-91") succeeded; latitude -91 must be rejected`)
	}
	if _, err := GetDegree("48.8584"); err != nil {
		t.Fatalf(`GetDegree("48.8584") failed: %v`, err)
	}
}

func TestAuditMinutesAfterMidnightTrimsWhitespace(t *testing.T) {
	// Pasted schedule times carry surrounding spaces (" 22:00", "06:00 ").
	// strconv.Atoi rejects them, so a valid time fails with "invalid time"
	// instead of scheduling. Copy-paste whitespace must be tolerated like
	// GetLatitude/GetLongitude already do via TrimSpace.
	if _, err := MinutesAfterMidnight(" 22:00"); err != nil {
		t.Fatalf(`MinutesAfterMidnight(" 22:00") failed: %v; leading space must be trimmed`, err)
	}
	if _, err := MinutesAfterMidnight("22:00 "); err != nil {
		t.Fatalf(`MinutesAfterMidnight("22:00 ") failed: %v; trailing space must be trimmed`, err)
	}
}
