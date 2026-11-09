package union

// Leg-one self-observation for the diagnose report (2026-11-10, slice D2).
//
// The hub's diagnose payload describes the hub's own probe of this site — its
// TLS diary, the status it got back from our inbound endpoint. Nothing in it
// describes the connection WE made, so the panel could only give a verdict for
// leg one and then paste the hub's diary. Two things are only knowable here,
// in this process, on this connection:
//
//   - what TLS we actually negotiated with the hub and when its certificate
//     expires. An expiring hub cert is a dated, actionable failure that today
//     shows up as an unreadable handshake dump;
//   - how far the hub's clock sits from ours, read off its Date header. The
//     judgement reuses VerifyPastSkew / VerifyFutureSkew, the same constants
//     the inbound verifier enforces, so the panel can never claim a hub is
//     "in step" while its signed callbacks are being rejected next door.
//
// Both are observations of a call that already happened. No new endpoint, no
// new host, nothing added to the wire.

import (
	"crypto/tls"
	"net/http"
	"time"
)

// TLSSnapshot describes the member -> hub connection. Absent (nil) when the
// hub was reached over plain HTTP — "no certificate" and "broken certificate"
// must not read the same.
type TLSSnapshot struct {
	Version      string `json:"version"`
	Cipher       string `json:"cipher"`
	CertSubject  string `json:"cert_subject,omitempty"`
	CertIssuer   string `json:"cert_issuer,omitempty"`
	CertNotAfter string `json:"cert_not_after,omitempty"` // RFC3339, UTC
	CertDaysLeft int    `json:"cert_days_left,omitempty"`
}

// ClockEvidence compares the hub clock (its Date header, second resolution)
// with ours. offset_seconds is hub minus local: positive means the hub is
// ahead, which is the direction that trips the +30s future allowance.
type ClockEvidence struct {
	HubClock      string  `json:"hub_clock"`
	OffsetSeconds float64 `json:"offset_seconds"`
	WithinWindow  bool    `json:"within_window"`
}

type DiagnoseProbe struct {
	TLS   *TLSSnapshot   `json:"tls,omitempty"`
	Clock *ClockEvidence `json:"clock,omitempty"`
}

// buildProbe reads the observation off a response that has already been
// consumed. A nil response (no connection was made) yields no probe at all;
// the cause token carries that story instead.
func buildProbe(resp *http.Response, now time.Time) *DiagnoseProbe {
	if resp == nil {
		return nil
	}
	p := &DiagnoseProbe{}
	if st := resp.TLS; st != nil {
		p.TLS = snapshotTLS(st, now)
	}
	if raw := resp.Header.Get("Date"); raw != "" {
		// Second granularity is enough: the window this is judged against is
		// measured in tens of seconds. An unparseable Date is treated as
		// absent rather than as zero, because zero would claim the clocks agree.
		if hub, err := http.ParseTime(raw); err == nil {
			off := hub.Sub(now)
			p.Clock = &ClockEvidence{
				HubClock:      raw,
				OffsetSeconds: off.Seconds(),
				WithinWindow:  off <= VerifyFutureSkew && off >= -VerifyPastSkew,
			}
		}
	}
	if p.TLS == nil && p.Clock == nil {
		return nil
	}
	return p
}

func snapshotTLS(st *tls.ConnectionState, now time.Time) *TLSSnapshot {
	snap := &TLSSnapshot{
		Version: tls.VersionName(st.Version),
		Cipher:  tls.CipherSuiteName(st.CipherSuite),
	}
	leaf := st.PeerCertificates
	if len(leaf) == 0 {
		return snap
	}
	c := leaf[0]
	snap.CertSubject = c.Subject.CommonName
	snap.CertIssuer = c.Issuer.CommonName
	snap.CertNotAfter = c.NotAfter.UTC().Format(time.RFC3339)
	snap.CertDaysLeft = int(c.NotAfter.Sub(now).Hours() / 24)
	return snap
}
