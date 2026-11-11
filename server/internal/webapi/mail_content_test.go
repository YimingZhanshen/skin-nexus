package webapi

import (
	"context"
	"encoding/base64"
	"mime"
	"net"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"skin-nexus/server/internal/dialect"
	"skin-nexus/server/internal/migrate"
	"skin-nexus/server/internal/store"
)

// captureSMTP accepts one letter and keeps the whole DATA payload so a test can
// assert what the player would actually receive (subject language, HTML band).
type captureSMTP struct {
	mu   sync.Mutex
	data []string
}

func (c *captureSMTP) String() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return strings.Join(c.data, "\n=====\n")
}

func startCaptureSMTP(t *testing.T) (host, port string, cap *captureSMTP) {
	t.Helper()
	cap = &captureSMTP{}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go serveCapture(conn, cap)
		}
	}()
	h, p, err := net.SplitHostPort(ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	return h, p, cap
}

func serveCapture(conn net.Conn, cap *captureSMTP) {
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
	buf := make([]byte, 0, 4096)
	tmp := make([]byte, 1024)
	readLine := func() string {
		for {
			if i := strings.Index(string(buf), "\r\n"); i >= 0 {
				line := string(buf[:i])
				buf = buf[i+2:]
				return line
			}
			n, err := conn.Read(tmp)
			if err != nil {
				return ""
			}
			buf = append(buf, tmp[:n]...)
		}
	}
	write := func(s string) { _, _ = conn.Write([]byte(s)) }
	write("220 capture\r\n")
	inData := false
	var data strings.Builder
	for {
		line := readLine()
		if line == "" && !inData {
			return
		}
		if inData {
			if line == "." {
				inData = false
				cap.mu.Lock()
				cap.data = append(cap.data, data.String())
				cap.mu.Unlock()
				write("250 queued\r\n")
			} else {
				data.WriteString(line + "\r\n")
			}
			continue
		}
		switch {
		case strings.HasPrefix(line, "EHLO"), strings.HasPrefix(line, "HELO"):
			write("250-capture\r\n250 PLAIN\r\n")
		case strings.HasPrefix(line, "AUTH"):
			write("235 ok\r\n")
		case strings.HasPrefix(line, "DATA"):
			inData = true
			data.Reset()
			write("354 go\r\n")
		case strings.HasPrefix(line, "QUIT"):
			write("221 bye\r\n")
			return
		default:
			write("250 ok\r\n")
		}
	}
}

func mailContentBackend(t *testing.T, palette func() map[string]string) (*Backend, *captureSMTP) {
	t.Helper()
	m, err := store.Open("sqlite", "sqlite:"+filepath.Join(t.TempDir(), "mc.db"), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = m.Close() })
	if err := migrate.ApplySQLDirTracked(m.DB(), filepath.Join("..", "..", "migrations"), dialect.SQLite); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	uid, err := m.CreateUser(ctx, "own@example.com", "owner", "x", 3, "")
	if err != nil {
		t.Fatal(err)
	}
	if err := m.CreateSession(ctx, "sess-own", uid, time.Now().UTC().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	host, port, cap := startCaptureSMTP(t)
	p, _ := strconv.Atoi(port)
	for k, v := range map[string]string{
		"smtp_host": host, "smtp_port": strconv.Itoa(p),
		"smtp_user": "site@example.test", "smtp_pass": "secret", "smtp_from": "site@example.test",
		"site_name": "Rig",
	} {
		if err := m.SetOption(ctx, k, v); err != nil {
			t.Fatal(err)
		}
	}
	return &Backend{Store: m, BaseURL: "http://rig.test", ThemePalette: palette}, cap
}

func waitForLetter(t *testing.T, cap *captureSMTP) string {
	t.Helper()
	for i := 0; i < 40; i++ {
		if s := cap.String(); s != "" {
			return s
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("no letter arrived at the capture SMTP within 2s")
	return ""
}

func decodedSubject(t *testing.T, wire string) string {
	t.Helper()
	for _, line := range strings.Split(wire, "\r\n") {
		if strings.HasPrefix(line, "Subject: ") {
			s, err := new(mime.WordDecoder).DecodeHeader(strings.TrimPrefix(line, "Subject: "))
			if err != nil {
				t.Fatalf("decode subject: %v", err)
			}
			return s
		}
	}
	t.Fatal("no Subject header")
	return ""
}

func htmlPart(t *testing.T, wire string) string {
	t.Helper()
	i := strings.Index(wire, "Content-Type: text/html")
	if i < 0 {
		t.Fatal("letter has no HTML part")
	}
	rest := wire[i:]
	j := strings.Index(rest, "\r\n\r\n")
	if j < 0 {
		t.Fatal("HTML part has no body")
	}
	body := rest[j+4:]
	if k := strings.Index(body, "\r\n--"); k >= 0 {
		body = body[:k]
	}
	raw, err := base64.StdEncoding.DecodeString(strings.ReplaceAll(strings.TrimSpace(body), "\r\n", ""))
	if err != nil {
		t.Fatalf("HTML part not base64: %v", err)
	}
	return string(raw)
}

// The reset letter must speak the requester's language, and the answer must
// still arrive inside the browser's window (the send stays detached).
func TestForgotMailIsBilingualAndDetached(t *testing.T) {
	b, cap := mailContentBackend(t, nil)
	h := b.Handler()
	rec := postWithin(t, h, "/api/v1/password/forgot?hl=en", `{"email":"own@example.com"}`, "")
	if rec.Code != http.StatusOK || strings.TrimSpace(rec.Body.String()) != `{"ok":true}` {
		t.Fatalf("forgot = %d %s, want the unchanged 200 oracle-safe shape", rec.Code, rec.Body.String())
	}
	if s := decodedSubject(t, waitForLetter(t, cap)); !strings.Contains(s, "password reset") {
		t.Errorf("en requester got subject %q", s)
	}

	b2, cap2 := mailContentBackend(t, nil)
	rec = postWithin(t, b2.Handler(), "/api/v1/password/forgot?hl=zh-CN", `{"email":"own@example.com"}`, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("zh forgot = %d", rec.Code)
	}
	if s := decodedSubject(t, waitForLetter(t, cap2)); !strings.Contains(s, "密码重置") {
		t.Errorf("zh requester got subject %q", s)
	}
}

// The verification letter carries the active theme's accent as a baked value.
func TestVerifyMailCarriesThemeAccent(t *testing.T) {
	b, cap := mailContentBackend(t, func() map[string]string { return map[string]string{"--accent": "#2266aa"} })
	rec := postWithin(t, b.Handler(), "/api/v1/email/verify", `{}`, "sess-own")
	if rec.Code != http.StatusOK {
		t.Fatalf("verify request = %d %s", rec.Code, rec.Body.String())
	}
	html := htmlPart(t, waitForLetter(t, cap))
	if !strings.Contains(html, `bgcolor="#2266aa"`) {
		t.Error("the theme accent must be baked into the band/button bgcolor")
	}
	if !strings.Contains(html, `bgcolor="#f7f5f2"`) {
		t.Error("the reading surface must stay the constant light paper")
	}

	b2, cap2 := mailContentBackend(t, nil)
	rec = postWithin(t, b2.Handler(), "/api/v1/email/verify", `{}`, "sess-own")
	if rec.Code != http.StatusOK {
		t.Fatalf("verify request (no theme) = %d", rec.Code)
	}
	if html := htmlPart(t, waitForLetter(t, cap2)); !strings.Contains(html, `bgcolor="#b0453a"`) {
		t.Error("no theme must fall back to the default accent")
	}
}
