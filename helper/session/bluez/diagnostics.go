package bluez

import "sync"

var (
	diagnosticMu     sync.RWMutex
	diagnosticLogger func(string, ...interface{})
)

// SetDiagnosticLogger installs the session child's phone-key logger. Diagnostics
// do not deliberately log advertisement payloads or device addresses. The logger is set
// once at startup; tests and other callers can leave it unset.
func SetDiagnosticLogger(logger func(string, ...interface{})) {
	diagnosticMu.Lock()
	diagnosticLogger = logger
	diagnosticMu.Unlock()
}

func diagnostic(format string, args ...interface{}) {
	diagnosticMu.RLock()
	logger := diagnosticLogger
	diagnosticMu.RUnlock()
	if logger != nil {
		logger(format, args...)
	}
}
