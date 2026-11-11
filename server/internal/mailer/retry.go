// retry.go: which transport failures deserve a second attempt.
//
// The rule of thumb: retry only what a minute of patience could fix (a wedged
// dial, a greylist, a dropped connection). Never retry what patience cannot fix
// (bad credentials, a permanent refusal, a TLS misconfiguration) — and never
// retry once the server has accepted DATA, because a second attempt would
// deliver the letter twice.

package mailer

import (
	"context"
	"errors"
	"io"
	"net"
	"net/textproto"
	"os"
)

// committedError marks a failure that happened AFTER the server queued the
// message (the QUIT exchange). retryable must always say no to it.
type committedError struct{ err error }

func (c *committedError) Error() string { return c.err.Error() }
func (c *committedError) Unwrap() error { return c.err }

func retryable(err error) bool {
	var committed *committedError
	if errors.As(err, &committed) {
		return false
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return false // the budget is spent; a retry has no time left
	}
	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) {
		if dnsErr.IsNotFound {
			return false // the host does not exist: a config error, not a blip
		}
		return dnsErr.IsTemporary || dnsErr.IsTimeout
	}
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return true
	}
	var opErr *net.OpError
	if errors.As(err, &opErr) {
		// A reset while connecting, reading or writing means the session died
		// before DATA was accepted (post-accept failures are committedError).
		return opErr.Op == "dial" || opErr.Op == "read" || opErr.Op == "write"
	}
	if errors.Is(err, io.ErrClosedPipe) || errors.Is(err, os.ErrDeadlineExceeded) {
		return true
	}
	if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
		return true // the peer hung up mid-handshake: a blip, not a verdict
	}
	var textErr *textproto.Error
	if errors.As(err, &textErr) {
		return textErr.Code/100 == 4 // greylist / mailbox busy / rate limit
	}
	return false // unknown: do not guess
}
