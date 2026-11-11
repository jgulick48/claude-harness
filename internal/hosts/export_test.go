package hosts

import "time"

// SetRetryBackoff makes automatic reconnects use a short backoff. Call it
// before adding hosts.
func SetRetryBackoff(m *Manager, base, max time.Duration) {
	m.rs.base, m.rs.max = base, max
	m.rs.jitter = func() float64 { return 0 }
}
