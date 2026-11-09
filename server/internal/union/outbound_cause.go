package union

// Cause attribution for the diagnose report (2026-11-10, slice D1).
//
// The hub call already returns a usable message, but a message is prose: the
// operator reading "失败" in the panel cannot tell whether to fix the member
// key in the settings form or to fix DNS on the host, and those are the two
// outcomes that actually happen. classifyError maps one failure onto a stable
// token so the UI can attach advice instead of a dump.
//
// The vocabulary is a contract with web/src/components/admin/UnionView.vue and
// is documented on DiagnoseReport; changing a token means changing the label
// map in the same commit.

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"net"
	"net/http"
	"os"
	"syscall"
)

// CauseOf is the exported face of the vocabulary, for callers that hold a
// union error rather than a DiagnoseReport (the admin ops route answers a
// module-level refusal before any report exists). It exists so that second
// caller cannot invent its own token for the same situation.
func CauseOf(err error) string { return classifyError(err) }

func classifyError(err error) string {
	if err == nil {
		return ""
	}
	// The module being switched off mid-request is not a network outcome, and
	// the panel must not dress it up as one.
	if errors.Is(err, ErrDisabled) {
		return "disabled"
	}

	// Leg one got an answer: the status code is the whole story.
	var apiErr *APIError
	if errors.As(err, &apiErr) {
		return classifyStatus(apiErr.StatusCode)
	}

	// Transport. Order matters: a DNS failure arrives inside a *net.OpError,
	// and a socket timeout does too, so the specific shapes are matched before
	// the generic "it was a TCP operation" fallback.
	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) {
		return "dns"
	}
	if isTLSError(err) {
		return "tls"
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, os.ErrDeadlineExceeded) {
		return "timeout"
	}
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return "timeout"
	}
	if errors.Is(err, syscall.ECONNREFUSED) {
		return "refused"
	}
	var opErr *net.OpError
	if errors.As(err, &opErr) {
		return "tcp"
	}
	return "unknown"
}

// classifyStatus groups hub answers by what the operator can act on, not by
// RFC taxonomy: 401 and 403 both mean "the hub does not accept this member
// key", 404 means "union_api_root points somewhere that is not the hub".
func classifyStatus(code int) string {
	switch {
	case code == http.StatusUnauthorized || code == http.StatusForbidden:
		return "http_key"
	case code == http.StatusNotFound:
		return "http_path"
	case code == http.StatusRequestTimeout || code == http.StatusTooManyRequests ||
		code == http.StatusServiceUnavailable:
		return "http_busy"
	case code >= 500:
		return "http_5xx"
	default:
		return "http_other"
	}
}

// isTLSError covers both shapes Go produces for a bad hub certificate: the
// 1.20+ wrapper and the bare x509 error, plus protocol-level alerts.
func isTLSError(err error) bool {
	var certVerErr *tls.CertificateVerificationError
	if errors.As(err, &certVerErr) {
		return true
	}
	var alert tls.AlertError
	if errors.As(err, &alert) {
		return true
	}
	var uaErr x509.UnknownAuthorityError
	if errors.As(err, &uaErr) {
		return true
	}
	var ciErr x509.CertificateInvalidError
	if errors.As(err, &ciErr) {
		return true
	}
	var hnErr x509.HostnameError
	if errors.As(err, &hnErr) {
		return true
	}
	var cvErr x509.ConstraintViolationError
	if errors.As(err, &cvErr) {
		return true
	}
	var srErr x509.SystemRootsError
	if errors.As(err, &srErr) {
		return true
	}
	return false
}
