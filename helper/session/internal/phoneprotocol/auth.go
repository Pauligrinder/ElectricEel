package phoneprotocol

import (
	"context"
	"fmt"

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
	AuthLevelNone   = 0
	AuthLevelUnlock = 1
	AuthLevelDrive  = 2
)

// authenticationRequest is the subset of VCSEC.AuthenticationRequest we need
// for passive entry: the level the vehicle is asking the phone key to grant.
type AuthenticationRequest struct {
	RequestedLevel int
	Token          []byte
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
func ParseAuthenticationRequest(datagram []byte) (AuthenticationRequest, bool) {
	if len(datagram) == 0 {
		return AuthenticationRequest{}, false
	}
	var msg universal.RoutableMessage
	if err := proto.Unmarshal(datagram, &msg); err == nil {
		if payload := msg.GetProtobufMessageAsBytes(); len(payload) > 0 {
			return ParseAuthRequestFromFromVCSEC(payload)
		}
	}
	return ParseAuthRequestFromFromVCSEC(datagram)
}

// parseAuthRequestFromFromVCSEC walks a FromVCSECMessage looking for
// sub_message field 3 (authenticationRequest). Generated v0.4.1 types omit
// that field, so decoding is manual via protowire.
func ParseAuthRequestFromFromVCSEC(b []byte) (AuthenticationRequest, bool) {
	var req AuthenticationRequest
	found := false
	for len(b) > 0 {
		num, typ, n := protowire.ConsumeTag(b)
		if n < 0 {
			return AuthenticationRequest{}, false
		}
		b = b[n:]
		switch {
		case num == 3 && typ == protowire.BytesType:
			val, m := protowire.ConsumeBytes(b)
			if m < 0 {
				return AuthenticationRequest{}, false
			}
			b = b[m:]
			inner, ok := ParseAuthRequestMessage(val)
			if !ok {
				return AuthenticationRequest{}, false
			}
			req = inner
			found = true
		default:
			m := protowire.ConsumeFieldValue(num, typ, b)
			if m < 0 {
				return AuthenticationRequest{}, false
			}
			b = b[m:]
		}
	}
	if !found {
		return AuthenticationRequest{}, false
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
func ParseAuthRequestMessage(b []byte) (AuthenticationRequest, bool) {
	var req AuthenticationRequest
	haveLevel := false
	for len(b) > 0 {
		num, typ, n := protowire.ConsumeTag(b)
		if n < 0 {
			return AuthenticationRequest{}, false
		}
		b = b[n:]
		switch {
		case num == 2 && typ == protowire.BytesType:
			val, m := protowire.ConsumeBytes(b)
			if m < 0 {
				return AuthenticationRequest{}, false
			}
			b = b[m:]
			token, ok := parseAuthRequestToken(val)
			if !ok {
				return AuthenticationRequest{}, false
			}
			req.Token = token
		case num == 3 && typ == protowire.VarintType:
			v, m := protowire.ConsumeVarint(b)
			if m < 0 {
				return AuthenticationRequest{}, false
			}
			b = b[m:]
			req.RequestedLevel = int(v)
			haveLevel = true
		default:
			m := protowire.ConsumeFieldValue(num, typ, b)
			if m < 0 {
				return AuthenticationRequest{}, false
			}
			b = b[m:]
		}
	}
	if !haveLevel {
		return AuthenticationRequest{}, false
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
func EncodeAuthenticationResponse(level int) []byte {
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
func SendAuthenticationResponse(ctx context.Context, car *vehicle.Vehicle, level int) error {
	switch level {
	case AuthLevelUnlock, AuthLevelDrive:
		// Grant exactly what was requested.
	case AuthLevelNone:
		return fmt.Errorf("refusing AuthenticationResponse at NONE")
	default:
		return fmt.Errorf("unsupported authentication level %d", level)
	}
	if car == nil {
		return protocol.ErrNotConnected
	}
	payload := EncodeAuthenticationResponse(level)
	_, err := car.Send(ctx, universal.Domain_DOMAIN_VEHICLE_SECURITY, payload, connector.AuthMethodGCM)
	return err
}
