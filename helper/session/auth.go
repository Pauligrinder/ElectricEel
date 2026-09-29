package main

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/teslamotors/vehicle-command/pkg/connector"
	"github.com/teslamotors/vehicle-command/pkg/protocol"
	universal "github.com/teslamotors/vehicle-command/pkg/protocol/protobuf/universalmessage"
	"github.com/teslamotors/vehicle-command/pkg/vehicle"
	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/proto"
)

// AuthenticationLevel values from VCSEC's AuthenticationLevel_E. The public
// vehicle-command v0.4.1 protobuf bindings omit AuthenticationRequest /
// AuthenticationResponse, so these constants (and the encode/decode helpers
// below) are maintained against the wire layout documented by Tesla's VCSEC
// protos and reverse-engineered phone-key flows.
const (
	authLevelNone   = 0
	authLevelUnlock = 1
	authLevelDrive  = 2
)

// AuthenticationReason_E values the vehicle may put on reasonsForAuth.
// Handle-pull / autopresent are the only ones we treat as "getting in".
const (
	authReasonIdentification   = 1
	authReasonPowerOn          = 2
	authReasonGTW              = 3
	authReasonUIUnlock         = 4
	authReasonExteriorHandle   = 5
	authReasonInteriorHandle   = 6
	authReasonAutopresentDoor  = 7
	authReasonHigherAuthZone   = 8
	authReasonWalkUpUnlock     = 9
	authReasonImmobilizer      = 10
)

// handlePullMinRSSI is the fallback when the car omits reasonsForAuth:
// a DRIVE grant at this RSSI or stronger is treated as at-the-door, not
// a bedroom-range reconnect (-93 last night).
const handlePullMinRSSI int16 = -80

func authLevelName(level int) string {
	switch level {
	case authLevelNone:
		return "NONE"
	case authLevelUnlock:
		return "UNLOCK"
	case authLevelDrive:
		return "DRIVE"
	default:
		return fmt.Sprintf("LEVEL_%d", level)
	}
}

func authReasonName(reason int) string {
	switch reason {
	case authReasonIdentification:
		return "IDENTIFICATION"
	case authReasonPowerOn:
		return "POWER_ON"
	case authReasonGTW:
		return "GTW"
	case authReasonUIUnlock:
		return "UI_UNLOCK"
	case authReasonExteriorHandle:
		return "EXTERIOR_HANDLE"
	case authReasonInteriorHandle:
		return "INTERIOR_HANDLE"
	case authReasonAutopresentDoor:
		return "AUTOPRESENT_DOOR"
	case authReasonHigherAuthZone:
		return "HIGHER_AUTH_ZONE"
	case authReasonWalkUpUnlock:
		return "WALK_UP_UNLOCK"
	case authReasonImmobilizer:
		return "IMMOBILIZER"
	default:
		return fmt.Sprintf("REASON_%d", reason)
	}
}

func formatAuthReasons(reasons []int) string {
	if len(reasons) == 0 {
		return "-"
	}
	parts := make([]string, len(reasons))
	for i, r := range reasons {
		parts[i] = authReasonName(r)
	}
	return strings.Join(parts, ",")
}

func authHasHandlePullReason(reasons []int) bool {
	for _, r := range reasons {
		switch r {
		case authReasonExteriorHandle, authReasonInteriorHandle, authReasonAutopresentDoor:
			return true
		}
	}
	return false
}

// handlePullFromAuth is true for an exterior/interior/autopresent pull.
// If the car sent reasons and none of them is a pull, it is false (a
// bedroom-range IDENTIFICATION DRIVE must not look like getting in).
// If reasons are omitted, DRIVE plus a strong live RSSI is the fallback.
func handlePullFromAuth(req authenticationRequest, rssi int16, hasRSSI bool) bool {
	if authHasHandlePullReason(req.Reasons) {
		return true
	}
	if len(req.Reasons) > 0 {
		return false
	}
	return req.RequestedLevel == authLevelDrive && hasRSSI && rssi >= handlePullMinRSSI
}

// authenticationRequest is the subset of VCSEC.AuthenticationRequest we need
// for passive entry: the level the vehicle is asking the phone key to grant,
// plus optional reasonsForAuth (field 4) when the firmware sends them.
type authenticationRequest struct {
	RequestedLevel int
	Token          []byte
	Reasons        []int
}

// parseAuthenticationRequest inspects an inbound BLE datagram for an
// unsolicited VCSEC AuthenticationRequest. Accepts either:
//   - a UniversalMessage.RoutableMessage whose protobuf_message_as_bytes
//     payload is a FromVCSECMessage carrying field 3 (authenticationRequest), or
//   - a bare FromVCSECMessage (legacy length-prefixed VCSEC framing still
//     seen in some phone-key traces).
//
// Returns ok=false when the datagram is anything else (ordinary command
// replies, session info, malformed bytes). Does not decrypt: encrypted
// payloads that the upstream dispatcher would also fail to route without a
// handler are skipped.
func parseAuthenticationRequest(datagram []byte) (authenticationRequest, bool) {
	if len(datagram) == 0 {
		return authenticationRequest{}, false
	}
	var msg universal.RoutableMessage
	if err := proto.Unmarshal(datagram, &msg); err == nil {
		if payload := msg.GetProtobufMessageAsBytes(); len(payload) > 0 {
			return parseAuthRequestFromFromVCSEC(payload)
		}
	}
	return parseAuthRequestFromFromVCSEC(datagram)
}

// parseAuthRequestFromFromVCSEC walks a FromVCSECMessage looking for
// sub_message field 3 (authenticationRequest). Generated v0.4.1 types omit
// that field, so decoding is manual via protowire.
func parseAuthRequestFromFromVCSEC(b []byte) (authenticationRequest, bool) {
	var req authenticationRequest
	found := false
	for len(b) > 0 {
		num, typ, n := protowire.ConsumeTag(b)
		if n < 0 {
			return authenticationRequest{}, false
		}
		b = b[n:]
		switch {
		case num == 3 && typ == protowire.BytesType:
			val, m := protowire.ConsumeBytes(b)
			if m < 0 {
				return authenticationRequest{}, false
			}
			b = b[m:]
			inner, ok := parseAuthRequestMessage(val)
			if !ok {
				return authenticationRequest{}, false
			}
			req = inner
			found = true
		default:
			m := protowire.ConsumeFieldValue(num, typ, b)
			if m < 0 {
				return authenticationRequest{}, false
			}
			b = b[m:]
		}
	}
	if !found {
		return authenticationRequest{}, false
	}
	return req, true
}

// parseAuthRequestMessage decodes AuthenticationRequest:
//
//	sessionInfo (field 2) = AuthenticationRequestToken { token (field 1) }
//	requestedLevel (field 3) = AuthenticationLevel_E
//
// requestedLevel is required. Treating a missing level as NONE made the
// responder answer identification/status traffic with AuthenticationResponse
// NONE, which poisoned the VCSEC session so even a dashboard refresh could
// not unlock.
func parseAuthRequestMessage(b []byte) (authenticationRequest, bool) {
	var req authenticationRequest
	haveLevel := false
	for len(b) > 0 {
		num, typ, n := protowire.ConsumeTag(b)
		if n < 0 {
			return authenticationRequest{}, false
		}
		b = b[n:]
		switch {
		case num == 2 && typ == protowire.BytesType:
			val, m := protowire.ConsumeBytes(b)
			if m < 0 {
				return authenticationRequest{}, false
			}
			b = b[m:]
			token, ok := parseAuthRequestToken(val)
			if !ok {
				return authenticationRequest{}, false
			}
			req.Token = token
		case num == 3 && typ == protowire.VarintType:
			v, m := protowire.ConsumeVarint(b)
			if m < 0 {
				return authenticationRequest{}, false
			}
			b = b[m:]
			req.RequestedLevel = int(v)
			haveLevel = true
		case num == 4 && typ == protowire.VarintType:
			v, m := protowire.ConsumeVarint(b)
			if m < 0 {
				return authenticationRequest{}, false
			}
			b = b[m:]
			req.Reasons = append(req.Reasons, int(v))
		case num == 4 && typ == protowire.BytesType:
			val, m := protowire.ConsumeBytes(b)
			if m < 0 {
				return authenticationRequest{}, false
			}
			b = b[m:]
			for len(val) > 0 {
				v, n := protowire.ConsumeVarint(val)
				if n < 0 {
					return authenticationRequest{}, false
				}
				val = val[n:]
				req.Reasons = append(req.Reasons, int(v))
			}
		default:
			m := protowire.ConsumeFieldValue(num, typ, b)
			if m < 0 {
				return authenticationRequest{}, false
			}
			b = b[m:]
		}
	}
	if !haveLevel {
		return authenticationRequest{}, false
	}
	return req, true
}

func parseAuthRequestToken(b []byte) ([]byte, bool) {
	var token []byte
	for len(b) > 0 {
		num, typ, n := protowire.ConsumeTag(b)
		if n < 0 {
			return nil, false
		}
		b = b[n:]
		switch {
		case num == 1 && typ == protowire.BytesType:
			val, m := protowire.ConsumeBytes(b)
			if m < 0 {
				return nil, false
			}
			b = b[m:]
			token = append([]byte(nil), val...)
		default:
			m := protowire.ConsumeFieldValue(num, typ, b)
			if m < 0 {
				return nil, false
			}
			b = b[m:]
		}
	}
	return token, true
}

// encodeAuthenticationResponse builds an UnsignedMessage whose sub_message
// is AuthenticationResponse (field 3) at the requested level. Field 3 is
// absent from v0.4.1's generated UnsignedMessage, so encoding is manual.
func encodeAuthenticationResponse(level int) []byte {
	authResp := protowire.AppendTag(nil, 1, protowire.VarintType)
	authResp = protowire.AppendVarint(authResp, uint64(level))
	msg := protowire.AppendTag(nil, 3, protowire.BytesType)
	msg = protowire.AppendBytes(msg, authResp)
	return msg
}

// sendAuthenticationResponse transmits a signed/encrypted AuthenticationResponse
// for level through the existing authenticated VCSEC session via the public
// vehicle.Send API (same path RKE lock/unlock uses for GCM auth).
//
// Only UNLOCK/DRIVE are sent: those are the handle-pull grants. Unsolicited
// NONE (teslabtapi's legacy ToVCSEC Authenticating step) sent via Universal
// Message poisoned the live session so refresh+handle-pull stopped working.
func sendAuthenticationResponse(ctx context.Context, car *vehicle.Vehicle, level int) error {
	switch level {
	case authLevelUnlock, authLevelDrive:
		// Grant exactly what was requested.
	case authLevelNone:
		return fmt.Errorf("refusing AuthenticationResponse at NONE")
	default:
		return fmt.Errorf("unsupported authentication level %d", level)
	}
	if car == nil {
		return protocol.ErrNotConnected
	}
	payload := encodeAuthenticationResponse(level)
	_, err := car.Send(ctx, universal.Domain_DOMAIN_VEHICLE_SECURITY, payload, connector.AuthMethodGCM)
	return err
}

// sessionDroppedError reports whether err indicates the live BLE/session
// should be torn down so presence mode can reconnect on the next near tick.
func sessionDroppedError(err error) bool {
	if err == nil {
		return false
	}
	return errors.Is(err, protocol.ErrNotConnected) ||
		errors.Is(err, protocol.ErrNoSession) ||
		errors.Is(err, context.Canceled)
}
