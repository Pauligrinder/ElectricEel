package library

import "fmt"

// diagnostic forwards this session's BlueZ transport diagnostics to Rust's
// logging worker; other sessions never see them.
func (t *Tesla) diagnostic(format string, args ...interface{}) {
	t.observe(Observation{Kind: 6, Error: fmt.Sprintf(format, args...)})
}
