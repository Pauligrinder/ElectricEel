package bluez

import (
	"bufio"
	"context"
	"os/exec"
	"testing"
	"time"
)

// Exercise the real godbus connection lifecycle: fakeBluez cannot reproduce
// SystemBus's process-wide cache surviving closure of a protocol session.
func TestBusSessionReplacement(t *testing.T) {
	daemon, err := exec.LookPath("dbus-daemon")
	if err != nil {
		t.Skip("dbus-daemon is required for the bus lifecycle integration test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, daemon, "--session", "--nofork", "--print-address=1")
	out, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	})
	scanner := bufio.NewScanner(out)
	if !scanner.Scan() {
		t.Fatalf("read private bus address: %v", scanner.Err())
	}
	t.Setenv("DBUS_SYSTEM_BUS_ADDRESS", scanner.Text())

	open := func() *Conn {
		t.Helper()
		conn, err := Open(nil)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = conn.Close() })
		return conn
	}
	checkLive := func(conn *Conn) {
		t.Helper()
		_, err := conn.bus.object("org.freedesktop.DBus", "/org/freedesktop/DBus").call(ctx, "org.freedesktop.DBus.ListNames")
		if err != nil {
			t.Fatalf("session bus is unusable: %v", err)
		}
	}

	first := open()
	other := open()
	checkLive(first)
	checkLive(other)
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	// Closing one owner must neither break another nor poison later opens.
	checkLive(other)
	replacement := open()
	checkLive(replacement)
	if err := other.Close(); err != nil {
		t.Fatal(err)
	}
	if err := replacement.Close(); err != nil {
		t.Fatal(err)
	}
	checkLive(open())
}
