package library

import (
	"electric-eel-session/bluez"
	"fmt"
	"sync"
)

var logging = struct {
	sync.RWMutex
	once     sync.Once
	sessions map[*Tesla]bool
}{sessions: make(map[*Tesla]bool)}

func (t *Tesla) attachDiagnostics() {
	logging.once.Do(func() {
		bluez.SetDiagnosticLogger(func(format string, args ...interface{}) {
			logging.RLock()
			defer logging.RUnlock()
			message := fmt.Sprintf(format, args...)
			for session := range logging.sessions {
				session.observe(Observation{Kind: 6, Error: message})
			}
		})
	})
	logging.Lock()
	logging.sessions[t] = true
	logging.Unlock()
}

func (t *Tesla) detachDiagnostics() {
	// Wait for borrowed callbacks before releasing the Rust callback context.
	logging.Lock()
	delete(logging.sessions, t)
	logging.Unlock()
}
