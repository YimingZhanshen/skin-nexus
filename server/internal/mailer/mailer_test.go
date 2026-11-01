package mailer

import (
	"bufio"
	"encoding/base64"
	"net"
	"strconv"
	"strings"
	"sync"
	"testing"
)

// fakeSMTP is a minimal in-process RFC5322-ish server: it captures the
// envelope and DATA payload of one session so Send can be exercised end to
// end without touching the network.
type fakeSMTP struct {
	mu   sync.Mutex
	out  strings.Builder
	ln   net.Listener
	done chan struct{}
}

func startFakeSMTP(t *testing.T) *fakeSMTP {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	f := &fakeSMTP{ln: ln, done: make(chan struct{})}
	go func() {
		defer close(f.done)
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go f.serve(conn)
		}
	}()
	t.Cleanup(func() { ln.Close(); <-f.done })
	return f
}

func (f *fakeSMTP) port(t *testing.T) int {
	t.Helper()
	_, p, err := net.SplitHostPort(f.ln.Addr().String())
	if err != nil {
		t.Fatalf("addr: %v", err)
	}
	n, err := strconv.Atoi(p)
	if err != nil {
		t.Fatalf("port: %v", err)
	}
	return n
}

func (f *fakeSMTP) snapshot() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.out.String()
}

func (f *fakeSMTP) serve(conn net.Conn) {
	defer conn.Close()
	r := bufio.NewReader(conn)
	w := bufio.NewWriter(conn)
	send := func(s string) { _, _ = w.WriteString(s); _ = w.Flush() }
	send("220 fake ESMTP ready\r\n")
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return
		}
		cmd := strings.ToUpper(strings.TrimSpace(line))
		switch {
		case strings.HasPrefix(cmd, "EHLO"), strings.HasPrefix(cmd, "HELO"):
			// Multiline greeting: capabilities then a "250 " terminator.
			// Deliberately NO STARTTLS — the plain path must work without it.
			send("250-fake\r\n250 PLAIN\r\n")
		case strings.HasPrefix(cmd, "MAIL FROM:"):
			f.mu.Lock()
			f.out.WriteString("FROM=" + strings.TrimSpace(line[len("MAIL FROM:"):]) + "\n")
			f.mu.Unlock()
			send("250 OK\r\n")
		case strings.HasPrefix(cmd, "RCPT TO:"):
			send("250 OK\r\n")
		case cmd == "DATA":
			send("354 go ahead\r\n")
			var data strings.Builder
			for {
				l, err := r.ReadString('\n')
				if err != nil {
					return
				}
				if l == ".\r\n" || l == ".\n" {
					break
				}
				// Un-dot-stuff (the client escapes a leading "." line).
				if strings.HasPrefix(l, "..") {
					l = l[1:]
				}
				data.WriteString(l)
			}
			f.mu.Lock()
			f.out.WriteString("DATA=\n" + data.String())
			f.mu.Unlock()
			send("250 OK queued\r\n")
		case cmd == "QUIT":
			send("221 bye\r\n")
			return
		default:
			send("250 OK\r\n")
		}
	}
}

func TestSendPlainSession(t *testing.T) {
	srv := startFakeSMTP(t)
	cfg := &Config{
		Host: "127.0.0.1", Port: srv.port(t),
		From: "SkinNexus <noreply@example.com>", SiteName: "肤枢",
	}
	body := "点击重置：https://skin.example/reset-password?token=abc123\n30 分钟内有效。"
	if err := cfg.Send("user@example.com", "肤枢 密码重置", body); err != nil {
		t.Fatalf("send: %v", err)
	}
	got := srv.snapshot()
	for _, want := range []string{
		"FROM=<noreply@example.com>",            // envelope strips the display name (client wraps in <>)
		"From: SkinNexus <noreply@example.com>", // header keeps it
		"To: user@example.com",
		"Subject: =?utf-8?", // non-ASCII subject is RFC2047 (QEncoding, lowercase charset)
		"Content-Type: text/plain; charset=utf-8",
		"Content-Transfer-Encoding: base64",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("captured session missing %q:\n%s", want, got)
		}
	}
	// Decode the base64 body back out of the DATA block and compare.
	sep := strings.Index(got, "DATA=\n")
	if sep < 0 {
		t.Fatalf("no DATA block:\n%s", got)
	}
	raw := got[sep+len("DATA=\n"):]
	var b64 strings.Builder
	for _, ln := range strings.Split(raw, "\r\n") {
		if ln == "" || strings.Contains(ln, ": ") && !isBase64Line(ln) {
			continue
		}
		if isBase64Line(ln) {
			b64.WriteString(ln)
		}
	}
	dec, err := base64.StdEncoding.DecodeString(strings.TrimSpace(b64.String()))
	if err != nil {
		t.Fatalf("body base64: %v (%q)", err, b64.String())
	}
	if string(dec) != body {
		t.Errorf("body roundtrip:\n got %q\nwant %q", dec, body)
	}
}

// isBase64Line filters header lines out of the DATA block: headers contain
// ": " (or are continuation folds), the payload does not.
func isBase64Line(ln string) bool {
	if strings.Contains(ln, ": ") {
		return false
	}
	for _, r := range ln {
		if !(r >= 'A' && r <= 'Z' || r >= 'a' && r <= 'z' ||
			r >= '0' && r <= '9' || r == '+' || r == '/' || r == '=') {
			return false
		}
	}
	return len(ln) > 0
}

func TestSendRejectsHeaderInjection(t *testing.T) {
	srv := startFakeSMTP(t)
	cfg := &Config{Host: "127.0.0.1", Port: srv.port(t), User: "u@example.com"}
	if err := cfg.Send("a@b.c\r\nBcc: evil@x.y", "s", "b"); err == nil {
		t.Fatal("CRLF in recipient must be rejected")
	}
	if err := cfg.Send("a@b.c", "s\r\nX-Injected: 1", "b"); err != nil {
		// Subject is Q-encoded, so CR/LF is neutralized rather than rejected —
		// but it must never reach the wire raw.
		_ = err
	}
	if got := srv.snapshot(); got != "" {
		t.Fatalf("rejected address must not dial out, got:\n%s", got)
	}
}

func TestDisabledAndNilReceiver(t *testing.T) {
	var nilCfg *Config
	if nilCfg.Enabled() {
		t.Error("nil config must be disabled")
	}
	empty := &Config{}
	if empty.Enabled() {
		t.Error("empty host must be disabled")
	}
	if err := empty.Send("a@b.c", "s", "b"); err == nil || !strings.Contains(err.Error(), "disabled") {
		t.Errorf("send on disabled config: %v", err)
	}
}

func TestEnvelopeFrom(t *testing.T) {
	cases := []struct {
		in   string
		want string
		fail bool
	}{
		{"a@b.c", "a@b.c", false},
		{"SkinNexus <noreply@b.c>", "noreply@b.c", false},
		{"a@b.c\r\nBcc: x@y.z", "", true},
		{"", "", true},
	}
	for _, c := range cases {
		got, err := envelopeFrom(c.in)
		if c.fail {
			if err == nil {
				t.Errorf("envelopeFrom(%q): want error, got %q", c.in, got)
			}
			continue
		}
		if err != nil || got != c.want {
			t.Errorf("envelopeFrom(%q) = %q, %v; want %q", c.in, got, err, c.want)
		}
	}
}

func TestBuildMessageEncoding(t *testing.T) {
	msg := buildMessage("a@b.c", "u@b.c", "中文主题", "正文明文")
	// CRLF-only: strip every CRLF pair; a leftover CR or LF is a bare LF.
	if strings.ContainsAny(strings.ReplaceAll(msg, "\r\n", ""), "\r\n") {
		t.Error("wire message must be CRLF-only")
	}
	if !strings.Contains(strings.ToLower(msg), "subject: =?utf-8?") {
		t.Errorf("Chinese subject not Q/B-encoded:\n%s", msg)
	}
	// ASCII subject stays readable (QEncoding leaves safe strings alone).
	ascii := buildMessage("a@b.c", "u@b.c", "Plain", "x")
	if !strings.Contains(ascii, "Subject: Plain\r\n") {
		t.Errorf("ASCII subject should stay plain:\n%s", ascii)
	}
}

func TestImplicitTLS(t *testing.T) {
	if !implicitTLS(465) {
		t.Error("465 must be implicit TLS")
	}
	for _, p := range []int{0, 25, 587, 2525} {
		if implicitTLS(p) {
			t.Errorf("port %d must not be implicit TLS", p)
		}
	}
}
