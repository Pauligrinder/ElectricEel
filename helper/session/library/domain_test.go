package library

import (
	"testing"

	"electric-eel-session/internal/phoneprotocol"
)

// Every ABI command must authenticate exactly the domain its vehicle method
// uses: VCSEC handlers (locks, keys, closures, wake, body-controller state)
// must not wait for an infotainment handshake on a sleeping car, and
// enrollment/session-info must not handshake at all.
func TestCommandDomainsMatchVehicleHandlers(t *testing.T) {
	none := map[string]bool{"add-key-request": true, "session-info": true, "rename-key": true, "product-info": true}
	vcsec := map[string]bool{
		"lock": true, "unlock": true, "drive": true, "wake": true,
		"add-key": true, "remove-key": true, "list-keys": true,
		"trunk-open": true, "trunk-move": true, "trunk-close": true, "frunk-open": true,
		"tonneau-open": true, "tonneau-close": true, "tonneau-stop": true,
		"autosecure-modelx": true, "body-controller-state": true,
	}
	for _, name := range commandNames {
		want := uint32(2)
		switch {
		case none[name]:
			want = 0
		case vcsec[name]:
			want = 1
		}
		if got := phoneprotocol.CommandDomain(name); got != want {
			t.Errorf("%s: domain %d, want %d", name, got, want)
		}
	}
}
