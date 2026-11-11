package mailer

import (
	"encoding/base64"
	"strings"
	"testing"
	"time"
)

func TestComposeFromHeaderFourWayConsistency(t *testing.T) {
	cases := []struct {
		cfg        Config
		wantHeader string
		wantEnv    string
	}{
		{Config{User: "u@x.com", SiteName: "肤枢"}, "=?utf-8?q?=E8=82=A4=E6=9E=A2?= <u@x.com>", "u@x.com"},
		{Config{From: "Name <a@b.c>", User: "u@x.com", SiteName: "Rig"}, "Name <a@b.c>", "a@b.c"},
		{Config{From: "a@b.c", SiteName: "Rig"}, "Rig <a@b.c>", "a@b.c"},
		{Config{From: "a@b.c"}, "a@b.c", "a@b.c"},
	}
	for _, c := range cases {
		h, env, err := composeFromHeader(&c.cfg)
		if err != nil {
			t.Fatalf("%+v: %v", c.cfg, err)
		}
		if h != c.wantHeader || env != c.wantEnv {
			t.Errorf("%+v: header=%q env=%q, want %q / %q", c.cfg, h, env, c.wantHeader, c.wantEnv)
		}
		if got := c.cfg.SenderAddress(); got != c.wantEnv {
			t.Errorf("SenderAddress = %q, want the envelope %q (four places must agree)", got, c.wantEnv)
		}
	}
}

func TestComposeFromHeaderParseFailureFallsBack(t *testing.T) {
	// A malformed explicit From must not introduce a new failure path: today's
	// envelopeFrom behaviour (bare address, no display name) is the fallback.
	cfg := &Config{From: "not a mailbox at all", User: "u@x.com"}
	h, env, err := composeFromHeader(cfg)
	if err != nil {
		t.Fatalf("compose must not fail on a weird From: %v", err)
	}
	if env != "not a mailbox at all" || h != env {
		t.Errorf("fallback header/env = %q/%q, want the raw address twice", h, env)
	}
}

func TestBuildMultipartStructure(t *testing.T) {
	m := Message{To: "u@x.com", Subject: "s", Text: "plain body", HTML: "<table>html body</table>", Lang: "zh-CN"}
	wire := compose("Rig <s@x.com>", m, "sn-testboundary", "id@x.com", time.Unix(0, 0).UTC())
	head, body, ok := strings.Cut(wire, "\r\n\r\n")
	if !ok {
		t.Fatal("no header/body separator")
	}
	if strings.Contains(head, "Content-Transfer-Encoding") {
		t.Error("a CTE on multipart/* itself is illegal; each part carries its own")
	}
	if !strings.Contains(head, `Content-Type: multipart/alternative; boundary="sn-testboundary"`) {
		t.Errorf("missing multipart content type: %s", head)
	}
	if n := strings.Count(body, "sn-testboundary"); n != 3 {
		t.Errorf("boundary appears %d times, want 3 (open, separator, close)", n)
	}
	pi := strings.Index(body, "text/plain")
	hi := strings.Index(body, "text/html")
	if pi < 0 || hi < 0 || pi > hi {
		t.Error("plain part must come first: RFC 2046 makes the LAST part preferred")
	}
	if !strings.HasSuffix(body, "--sn-testboundary--\r\n") {
		t.Error("closing boundary must end the message with CRLF and no epilogue")
	}
	// Both parts round-trip through base64.
	parts := strings.Split(body, "--sn-testboundary")
	var decoded []string
	for _, p := range parts {
		if i := strings.Index(p, "\r\n\r\n"); i >= 0 {
			b64 := strings.ReplaceAll(strings.TrimSpace(p[i+4:]), "\r\n", "")
			b64 = strings.TrimSuffix(b64, "--")
			raw, err := base64.StdEncoding.DecodeString(b64)
			if err != nil {
				t.Fatalf("part not base64: %v", err)
			}
			decoded = append(decoded, string(raw))
		}
	}
	if len(decoded) != 2 || decoded[0] != m.Text || decoded[1] != m.HTML {
		t.Errorf("parts did not round-trip: %q", decoded)
	}
}

func TestMessageHeadersOrderAndPresence(t *testing.T) {
	wire := compose("Rig <s@x.com>", Message{To: "u@x.com", Subject: "s", Text: "t", Lang: "en"}, "", "id@x.com", time.Unix(0, 0).UTC())
	order := []string{"From:", "To:", "Subject:", "Date:", "Message-ID:", "Auto-Submitted:", "Content-Language:", "MIME-Version:", "Content-Type:"}
	last := -1
	for _, h := range order {
		i := strings.Index(wire, h)
		if i < 0 {
			t.Fatalf("missing header %s", h)
		}
		if i < last {
			t.Errorf("header %s out of order", h)
		}
		last = i
	}
	if !strings.Contains(wire, "Auto-Submitted: auto-generated\r\n") {
		t.Error("Auto-Submitted must suppress vacation auto-reply loops")
	}
	if !strings.Contains(wire, "Message-ID: <id@x.com>\r\n") {
		t.Error("Message-ID missing")
	}
}

func TestMessageIDDomainFallsBackToHost(t *testing.T) {
	if got := newMessageID("localaccount", "smtp.x.com"); !strings.HasSuffix(got, "@smtp.x.com") {
		t.Errorf("bare local account must name the SMTP host, got %s", got)
	}
	if got := newMessageID("a@b.c", "smtp.x.com"); !strings.HasSuffix(got, "@b.c") {
		t.Errorf("envelope domain must win, got %s", got)
	}
}

func TestBoundaryNeverAppearsInBodies(t *testing.T) {
	text, html := "some plain text", "<table>some html</table>"
	for i := 0; i < 64; i++ {
		b := newBoundary(text, html)
		if strings.Contains(text, b) || strings.Contains(html, b) {
			t.Fatalf("boundary %q collides with a body", b)
		}
	}
	// A body that literally contains a candidate forces the regeneration loop.
	collide := "sn-" + strings.Repeat("0", 32)
	if b := newBoundary(collide, ""); strings.Contains(collide, b) {
		t.Fatalf("regeneration loop failed: %q inside body", b)
	}
}

func TestPlainPathKeepsHistoricalBody(t *testing.T) {
	wire := compose("a@b.c", Message{To: "u@b.c", Subject: "中文主题", Text: "正文明文"}, "", "id@b.c", time.Unix(0, 0).UTC())
	if !strings.Contains(wire, "Content-Type: text/plain; charset=utf-8\r\nContent-Transfer-Encoding: base64\r\n") {
		t.Error("plain path must keep the historical content type and CTE")
	}
	enc := base64.StdEncoding.EncodeToString([]byte("正文明文"))
	if !strings.Contains(wire, enc) {
		t.Error("plain body must stay base64 of the exact text")
	}
}
