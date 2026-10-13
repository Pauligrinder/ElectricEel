package main

import (
	"electric-eel-session/internal/phoneprotocol"
	"testing"

	"github.com/teslamotors/vehicle-command/pkg/protocol"
)

// Keep the shared ABI domain mapping aligned with the pinned vendor handlers.
// These handlers use VCSEC directly even though their metadata omits it.
func TestSharedDomainsMatchVendorHandlers(t *testing.T) {
	vcsec := map[string]bool{
		"lock": true, "unlock": true, "drive": true,
		"add-key": true, "remove-key": true, "list-keys": true,
		"wake": true, "trunk-open": true, "trunk-move": true, "trunk-close": true,
		"frunk-open": true, "tonneau-open": true, "tonneau-close": true,
		"tonneau-stop": true, "autosecure-modelx": true,
	}
	for name, info := range commands {
		want := uint32(2)
		switch {
		case commandsWithoutSession[name]:
			want = 0
		case vcsec[name] || info.domain == protocol.DomainVCSEC:
			want = 1
		case !info.requiresAuth && info.domain == protocol.DomainNone:
			want = 0
		}
		if got := phoneprotocol.CommandDomain(name); got != want {
			t.Errorf("%s: shared domain %d, vendor handler domain %d", name, got, want)
		}
	}
}
