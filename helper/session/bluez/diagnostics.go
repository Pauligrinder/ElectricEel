package bluez

// Logger receives a session's BlueZ transport diagnostics. Diagnostics do not
// deliberately log advertisement payloads or device addresses.
type Logger func(format string, args ...interface{})

func (l Logger) logf(format string, args ...interface{}) {
	if l != nil {
		l(format, args...)
	}
}
