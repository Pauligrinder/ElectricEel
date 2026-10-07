package main

import (
	"bytes"
	"context"
	"fmt"
	"time"

	"github.com/teslamotors/vehicle-command/pkg/protocol"
	"github.com/teslamotors/vehicle-command/pkg/protocol/protobuf/signatures"
)

// Core pairing must enroll the key that this phone will actually use. A crash
// between private/public file replacement can leave a stale public file; do not
// enroll it and then mark a different private key as paired.
func (s *session) dispatchPair(req request) response {
	if len(req.Args) != 3 {
		return response{ID: req.ID, Stderr: "pair requires public key, role, and form factor", ExitCode: 2}
	}
	publicKey, err := protocol.LoadPublicKey(req.Args[0])
	if err != nil {
		return response{ID: req.ID, Stderr: fmt.Sprintf("cannot load pairing public key: %v", err), ExitCode: 2}
	}
	privateKey, err := protocol.LoadPrivateKey(s.keyFile)
	if err != nil {
		return response{ID: req.ID, Stderr: fmt.Sprintf("cannot load pairing private key: %v", err), ExitCode: 2}
	}
	if !bytes.Equal(publicKey.Bytes(), privateKey.PublicBytes()) {
		return response{ID: req.ID, Stderr: "public key does not match this phone's private key; generate/reuse the key before pairing", ExitCode: 2}
	}
	req.Cmd = "add-key-request"
	return s.dispatch(req)
}

// Transmission is not enrollment. Keep the link up while awaiting NFC approval,
// querying the requested public key, not merely whether some other key works.
func (s *session) confirmEnrollment(ctx context.Context, publicKeyFile string) error {
	key, err := protocol.LoadPublicKey(publicKeyFile)
	if err != nil {
		return err
	}
	privateKey, err := protocol.LoadPrivateKey(s.keyFile)
	if err != nil {
		return err
	}
	return waitForEnrollment(ctx, time.Second, func(attempt context.Context) error {
		info, err := s.car.SessionInfo(attempt, key, protocol.DomainVCSEC)
		if err != nil {
			return err
		}
		if info.GetStatus() != signatures.Session_Info_Status_SESSION_INFO_STATUS_OK {
			return fmt.Errorf("vehicle has not enrolled the requested key (%s)", info.GetStatus())
		}
		if bytes.Equal(key.Bytes(), privateKey.PublicBytes()) {
			// Pairing this phone's own key additionally proves that an
			// authenticated VCSEC session can actually be established.
			return s.car.StartSession(attempt, []protocol.Domain{protocol.DomainVCSEC})
		}
		return nil
	})
}

func waitForEnrollment(ctx context.Context, interval time.Duration, verify func(context.Context) error) error {
	var lastErr error
	for {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("NFC enrollment was not confirmed: %w (last response: %v)", err, lastErr)
		}
		attempt, cancel := context.WithTimeout(ctx, 3*time.Second)
		lastErr = verify(attempt)
		cancel()
		if lastErr == nil {
			return nil
		}
		timer := time.NewTimer(interval)
		select {
		case <-ctx.Done():
			timer.Stop()
		case <-timer.C:
		}
	}
}
