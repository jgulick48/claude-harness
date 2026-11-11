package hosts

import (
	"context"
	"errors"
	"io"
	"math/rand/v2"
	"net"
	"strings"
	"syscall"
	"time"

	"github.com/jgulick48/claude-harness/internal/sshconn"
)

const (
	backoffBase   = time.Second
	backoffMax    = 60 * time.Second
	backoffJitter = 0.2 // +/- 20%
)

// stopper is the part of *time.Timer the retry logic needs.
type stopper interface{ Stop() bool }

// backoffDelay is the wait before retry number attempt (0-based): 1s, 2s, 4s,
// ... capped at max, then scaled by (1 + jitter*j) where j is in [-1, 1].
func backoffDelay(attempt int, base, max time.Duration, j float64) time.Duration {
	d := base
	for i := 0; i < attempt && d < max; i++ {
		d *= 2
	}
	if d > max {
		d = max
	}
	if j < -1 {
		j = -1
	} else if j > 1 {
		j = 1
	}
	return time.Duration(float64(d) * (1 + backoffJitter*j))
}

// Windows socket error codes (syscall.Errno values on that platform differ
// from the POSIX ones, so compare numerically).
var winRetryable = map[syscall.Errno]bool{
	10061: true, // WSAECONNREFUSED
	10054: true, // WSAECONNRESET
	10065: true, // WSAEHOSTUNREACH
	10051: true, // WSAENETUNREACH
	10060: true, // WSAETIMEDOUT
}

// isRetryable reports whether a failed connect (or a lost connection) is
// likely temporary, so core should retry on its own. Failures that need the
// user (unknown or changed host key, no usable keys, authentication failure)
// and anything unrecognised are not retryable.
func isRetryable(err error) bool {
	if err == nil {
		return false
	}
	var unknown *sshconn.UnknownHostKeyError
	var changed *sshconn.HostKeyChangedError
	if errors.As(err, &unknown) || errors.As(err, &changed) ||
		errors.Is(err, sshconn.ErrNoAuthMethods) || errors.Is(err, context.Canceled) {
		return false
	}
	if strings.Contains(err.Error(), "unable to authenticate") {
		return false
	}
	if errors.Is(err, sshconn.ErrConnectionLost) || errors.Is(err, io.EOF) ||
		errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, context.DeadlineExceeded) ||
		errors.Is(err, syscall.ECONNREFUSED) || errors.Is(err, syscall.ECONNRESET) ||
		errors.Is(err, syscall.EHOSTUNREACH) || errors.Is(err, syscall.ENETUNREACH) {
		return true
	}
	var en syscall.Errno
	if errors.As(err, &en) && winRetryable[en] {
		return true
	}
	var dns *net.DNSError
	if errors.As(err, &dns) {
		return true
	}
	var ne net.Error
	if errors.As(err, &ne) && ne.Timeout() {
		return true
	}
	return false
}

// retrySeams are the injectable clock/jitter/timer used by the backoff logic.
type retrySeams struct {
	now       func() time.Time
	jitter    func() float64 // in [-1, 1]
	afterFunc func(d time.Duration, f func()) stopper
	base, max time.Duration
}

func (m *Manager) nowTime() time.Time {
	if m.rs.now != nil {
		return m.rs.now()
	}
	return time.Now()
}

func (m *Manager) delayFor(attempt int) time.Duration {
	base, max := m.rs.base, m.rs.max
	if base <= 0 {
		base = backoffBase
	}
	if max <= 0 {
		max = backoffMax
	}
	j := 0.0
	if m.rs.jitter != nil {
		j = m.rs.jitter()
	} else {
		j = rand.Float64()*2 - 1
	}
	return backoffDelay(attempt, base, max, j)
}

func (m *Manager) afterFunc(d time.Duration, f func()) stopper {
	if m.rs.afterFunc != nil {
		return m.rs.afterFunc(d, f)
	}
	return time.AfterFunc(d, f)
}

// stableAfter is how long a connection must stay up before a later loss
// restarts the backoff from 1s. A var so tests can shorten it.
var stableAfter = 30 * time.Second
