package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestReviewKeygenWithoutForcePreservesUnreadableExistingKey(t *testing.T) {
	keyFile := filepath.Join(t.TempDir(), "private_key.pem")
	original := []byte("-----BEGIN EC PRIVATE KEY-----\ntruncated-key-evidence\n")
	if err := os.WriteFile(keyFile, original, 0600); err != nil {
		t.Fatal(err)
	}
	s := &session{keyFile: keyFile}
	resp := s.dispatch(request{ID: "key-recovery", Cmd: "keygen"})
	after, err := os.ReadFile(keyFile)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(after, original) {
		t.Error("keygen without -f overwrote an existing unreadable private key")
	}
	if resp.OK {
		t.Error("an unreadable existing key must require explicit replacement, not report reuse success")
	}
}

func TestReviewKeygenAtomicFailureKeepsExistingState(t *testing.T) {
	dir := t.TempDir()
	keyFile := filepath.Join(dir, "private_key.pem")
	if err := os.Mkdir(keyFile, 0700); err != nil {
		t.Fatal(err)
	}
	evidence := filepath.Join(keyFile, "original")
	if err := os.WriteFile(evidence, []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	s := &session{keyFile: keyFile}
	resp := s.dispatch(request{Cmd: "keygen", Args: []string{"-f"}})
	if resp.OK {
		t.Fatal("replacing a directory must fail")
	}
	data, err := os.ReadFile(evidence)
	if err != nil || string(data) != "keep" {
		t.Fatalf("failed key replacement destroyed existing state: %q, %v", data, err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 1 {
		t.Fatalf("failed key replacement left temporary files: %v, %v", entries, err)
	}
}

func TestReviewOptionalScheduleSlotsKeepTheirMeaning(t *testing.T) {
	for _, command := range []string{"charging-schedule-add", "precondition-schedule-add"} {
		args := []string{"all", "06:00", "48.8584", "2.2945", "", "12345", "false"}
		keywords := keywordArguments(commands[command], args)
		if _, exists := keywords["REPEAT"]; exists {
			t.Errorf("%s: omitted REPEAT must retain its backend default", command)
		}
		if keywords["ID"] != "12345" || keywords["ENABLED"] != "false" {
			t.Errorf("%s: optional values shifted: %v", command, keywords)
		}
	}
}

func TestReviewPairingWaitsForVehicleConfirmation(t *testing.T) {
	attempts := 0
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	err := waitForEnrollment(ctx, time.Millisecond, func(context.Context) error {
		attempts++
		if attempts < 3 {
			return errors.New("key not on whitelist yet")
		}
		return nil
	})
	if err != nil || attempts != 3 {
		t.Fatalf("pairing must wait for approval: attempts=%d, error=%v", attempts, err)
	}
}

func TestReviewPairingWithoutNFCApprovalFails(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	err := waitForEnrollment(ctx, time.Millisecond, func(context.Context) error {
		return errors.New("key not on whitelist")
	})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("transmitting an add-key request must not imply successful pairing: %v", err)
	}
}

func TestReviewPairingRejectsStalePublicKeyBeforeBLE(t *testing.T) {
	dir := t.TempDir()
	s := &session{keyFile: filepath.Join(dir, "phone.pem")}
	other := &session{keyFile: filepath.Join(dir, "other.pem")}
	if response := s.dispatch(request{Cmd: "keygen"}); !response.OK {
		t.Fatal(response.Stderr)
	}
	otherKey := other.dispatch(request{Cmd: "keygen"})
	if !otherKey.OK {
		t.Fatal(otherKey.Stderr)
	}
	publicFile := filepath.Join(dir, "public_key.pem")
	if err := os.WriteFile(publicFile, []byte(otherKey.Stdout), 0600); err != nil {
		t.Fatal(err)
	}
	response := s.dispatch(request{ID: "pair", Cmd: "pair", Args: []string{publicFile, "owner", "android_device"}})
	if response.OK || response.ExitCode != 2 || !strings.Contains(response.Stderr, "does not match") {
		t.Fatalf("stale public key must not mark another private key enrolled: %+v", response)
	}
	if s.car != nil {
		t.Fatal("mismatched keys must be refused before connecting")
	}
}

func TestReviewCommandOutputIsConcurrentAndIsolated(t *testing.T) {
	originalOut, originalErr := os.Stdout, os.Stderr
	results := make(chan string, 2)
	for _, label := range []string{"first", "second"} {
		go func() {
			out, errOut := captureOutput(context.Background(), func(ctx context.Context) {
				fmt.Fprintln(commandOutput(ctx).stdout, label)
				writeErr(ctx, "%s error", label)
				commands["lock"].Usage(ctx, "lock")
			})
			if !strings.HasPrefix(out, label+"\nUsage: lock") || errOut != label+" error\n" {
				results <- fmt.Sprintf("mixed output %q %q", out, errOut)
				return
			}
			results <- ""
		}()
	}
	for range 2 {
		if result := <-results; result != "" {
			t.Error(result)
		}
	}
	if os.Stdout != originalOut || os.Stderr != originalErr {
		t.Fatal("command output must never change process-wide writers")
	}
}
