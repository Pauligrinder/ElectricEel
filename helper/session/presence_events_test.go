package main

import (
	"bytes"
	"encoding/json"
	"testing"
	"time"

	"github.com/teslamotors/vehicle-command/pkg/protocol/protobuf/vcsec"
	"github.com/teslamotors/vehicle-command/pkg/vehicle"
)

func TestInsideRequiresSettledConnectionAndFreshUserPresence(t *testing.T) {
	now := time.Now()
	present := vcsec.UserPresence_E_VEHICLE_USER_PRESENCE_PRESENT
	for _, tc := range []struct {
		name                  string
		connectedAt, statusAt time.Time
		presence              vcsec.UserPresence_E
		want                  bool
	}{
		{"settled", now.Add(-insideSettleDuration), now, present, true},
		{"too soon", now.Add(-insideSettleDuration + time.Second), now, present, false},
		{"no connection", time.Time{}, now, present, false},
		{"no status", now.Add(-time.Minute), time.Time{}, present, false},
		{"stale status", now.Add(-time.Minute), now.Add(-2*vcsecPrimeInterval - time.Second), present, false},
		{"future status", now.Add(-time.Minute), now.Add(time.Second), present, false},
		{"unknown", now.Add(-time.Minute), now, vcsec.UserPresence_E_VEHICLE_USER_PRESENCE_UNKNOWN, false},
		{"absent", now.Add(-time.Minute), now, vcsec.UserPresence_E_VEHICLE_USER_PRESENCE_NOT_PRESENT, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := readyForInside(tc.connectedAt, tc.statusAt, now, tc.presence); got != tc.want {
				t.Fatalf("readyForInside = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestInsideEventEmitsOnceAndTeardownResetsIt(t *testing.T) {
	now := time.Now()
	var output bytes.Buffer
	s := &session{
		vin: "5YJ3E1EA0PF000000", enc: json.NewEncoder(&output),
		car: &vehicle.Vehicle{}, presenceCancel: func() {},
		connectedAt: now.Add(-time.Minute), lastVCSECPrime: now,
		lastUserPresence: vcsec.UserPresence_E_VEHICLE_USER_PRESENCE_PRESENT,
	}
	s.emitInsideLocked(now)
	s.emitInsideLocked(now.Add(time.Second))
	if bytes.Count(output.Bytes(), []byte("\n")) != 1 || !s.insideEmitted {
		t.Fatal("inside event was duplicated")
	}
	decoder := json.NewDecoder(&output)
	var got taggedEvent
	if err := decoder.Decode(&got); err != nil || got.Kind != "presence_inside" || got.VIN != s.vin {
		t.Fatalf("inside event = %+v, %v", got, err)
	}
	// No real transport is attached to this fixture.
	s.car = nil
	s.teardownLocked()
	if !s.connectedAt.IsZero() || s.insideEmitted ||
		s.lastUserPresence != vcsec.UserPresence_E_VEHICLE_USER_PRESENCE_UNKNOWN {
		t.Fatal("teardown retained integration state from the old link")
	}
	s.emitInsideLocked(now)
	if output.Len() != 0 {
		t.Fatal("disconnected session emitted inside event")
	}
}
