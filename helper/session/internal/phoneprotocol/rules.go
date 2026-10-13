package phoneprotocol

import "math"

// CommandDomain uses ABI domain numbers: 0 = no handshake, 1 = VCSEC,
// 2 = infotainment. Callers validate command names before connecting.
func CommandDomain(name string) uint32 {
	switch name {
	case "add-key-request", "session-info", "rename-key", "product-info", "get", "post":
		return 0
	case "", "unlock", "lock", "drive", "wake", "add-key", "remove-key", "list-keys", "trunk-open", "trunk-move", "trunk-close", "frunk-open", "tonneau-open", "tonneau-close", "tonneau-stop", "autosecure-modelx", "body-controller-state":
		return 1
	default:
		return 2
	}
}

func ValidCoordinate(value, limit float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0) && math.Abs(value) <= limit
}

func ValidLatLon(latitude, longitude float64) bool {
	return ValidCoordinate(latitude, 90) && ValidCoordinate(longitude, 180)
}
