package library

import (
	"bytes"
	"context"
	"sync/atomic"
	"time"

	"crypto/ecdh"
	"electric-eel-session/bluez"
	"electric-eel-session/internal/phoneprotocol"
	"github.com/teslamotors/vehicle-command/pkg/protocol"
	"github.com/teslamotors/vehicle-command/pkg/protocol/protobuf/signatures"
	"google.golang.org/protobuf/encoding/protojson"
)

func authenticationLevel(data []byte) (int, bool) {
	request, ok := phoneprotocol.ParseAuthenticationRequest(data)
	level := request.RequestedLevel
	return level, ok && (level == phoneprotocol.AuthLevelUnlock || level == phoneprotocol.AuthLevelDrive)
}

// authTap returns the GATT inbound observer for passive entry. It runs on the
// RX loop, so it never blocks: ordinary command responses are discarded
// here, and only the newest real AuthenticationRequest level is queued (the
// vehicle repeats requests, so an older pending one is obsolete). A replaced
// request is a diagnostic, not a session fault.
func authTap(ctx context.Context, passive *atomic.Bool, requests chan int, logf func(string, ...interface{})) bluez.InboundObserver {
	return func(data []byte) {
		if ctx.Err() != nil || !passive.Load() {
			return
		}
		level, ok := authenticationLevel(data)
		if !ok {
			return
		}
		for {
			select {
			case requests <- level:
				return
			default:
			}
			select {
			case <-requests:
				logf("passive entry replaced an unanswered authentication request")
			default:
			}
		}
	}
}

func (t *Tesla) startLinkObserver() {
	bz, ok := t.conn.(*bluez.Connection)
	if !ok {
		return
	}
	root := t.root
	if root == nil {
		root = context.Background()
	}
	ctx, cancel := context.WithCancel(root)
	t.linkCancel, t.linkDone = cancel, make(chan struct{})
	done, car := t.linkDone, t.car
	requests := make(chan int, 1)
	bz.SetInboundObserver(authTap(ctx, &t.passive, requests, t.diagnostic))
	go func() {
		defer close(done)
		defer t.backgroundGuard()
		defer bz.SetInboundObserver(nil)
		ticker := time.NewTicker(2 * time.Second)
		defer ticker.Stop()
		down := 0
		for {
			select {
			case <-ctx.Done():
				return
			case <-bz.Dropped():
				if ctx.Err() == nil {
					t.linkActive.Store(false)
					t.observe(Observation{Kind: 2, Status: NotConnected, Error: "GATT link dropped"})
				}
				return
			case <-ticker.C:
				check, done := context.WithTimeout(ctx, 400*time.Millisecond)
				connected, err := bz.DeviceConnected(check)
				done()
				if ctx.Err() != nil {
					return
				}
				if err == nil && !connected {
					down++
				} else if err == nil {
					down = 0
				}
				if down >= 2 {
					t.linkActive.Store(false)
					t.observe(Observation{Kind: 2, Status: NotConnected, Error: "BlueZ reports disconnected"})
					return
				}
			case level := <-requests:
				if !t.passive.Load() {
					continue
				}
				sendCtx, sendCancel := context.WithTimeout(ctx, 5*time.Second)
				err := phoneprotocol.SendAuthenticationResponse(sendCtx, car, level)
				sendCancel()
				if ctx.Err() != nil {
					return
				}
				if err == nil {
					t.observe(Observation{Kind: 3, Value: uint32(level)})
				} else {
					failure := classify(err)
					t.observe(Observation{Kind: 4, Status: failure.Status, Error: failure.Error})
				}
			}
		}
	}()
}

func (t *Tesla) enrollment(ctx context.Context, req Request) Result {
	if t.car == nil {
		return classify(protocol.ErrNotConnected)
	}
	key, err := ecdh.P256().NewPublicKey([]byte(req.Text))
	if err != nil {
		return Result{Status: Invalid, Error: "invalid enrollment public key"}
	}
	info, err := t.car.SessionInfo(ctx, key, protocol.DomainVCSEC)
	if err != nil {
		return classify(err)
	}
	if info.GetStatus() != signatures.Session_Info_Status_SESSION_INFO_STATUS_OK {
		return Result{Status: AuthenticationFailed, Error: "requested key is not enrolled"}
	}
	if err := ctx.Err(); err != nil {
		return classify(err)
	}
	if bytes.Equal(key.Bytes(), t.key.PublicBytes()) {
		if err := t.car.StartSession(ctx, []protocol.Domain{protocol.DomainVCSEC}); err != nil {
			return classify(err)
		}
	}
	payload, err := (protojson.MarshalOptions{}).Marshal(info)
	result := classify(err)
	result.Payload = payload
	return result
}
