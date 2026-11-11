package adminapi

import (
	"context"
	"encoding/base64"
	"net"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// The test letter's summary must describe the configuration without ever
// carrying the credential: an operator screenshots this mail when asking for
// help, and the screenshot leaves the machine.
func TestTestMailSummaryHasNoCredentials(t *testing.T) {
	ctx := context.Background()
	b, uid := mailTestBackend(t, true)

	var mu sync.Mutex
	var letters []string
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
			go func() {
				defer conn.Close()
				_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
				buf := make([]byte, 0, 4096)
				tmp := make([]byte, 1024)
				readLine := func() string {
					for {
						if i := strings.Index(string(buf), "\r\n"); i >= 0 {
							l := string(buf[:i])
							buf = buf[i+2:]
							return l
						}
						n, err := conn.Read(tmp)
						if err != nil {
							return ""
						}
						buf = append(buf, tmp[:n]...)
					}
				}
				w := func(s string) { _, _ = conn.Write([]byte(s)) }
				w("220 capture\r\n")
				inData, data := false, strings.Builder{}
				for {
					line := readLine()
					if line == "" && !inData {
						return
					}
					if inData {
						if line == "." {
							inData = false
							mu.Lock()
							letters = append(letters, data.String())
							mu.Unlock()
							w("250 queued\r\n")
						} else {
							data.WriteString(line + "\r\n")
						}
						continue
					}
					switch {
					case strings.HasPrefix(line, "EHLO"), strings.HasPrefix(line, "HELO"):
						w("250-capture\r\n250 PLAIN\r\n")
					case strings.HasPrefix(line, "AUTH"):
						w("235 ok\r\n")
					case strings.HasPrefix(line, "DATA"):
						inData = true
						data.Reset()
						w("354 go\r\n")
					case strings.HasPrefix(line, "QUIT"):
						w("221 bye\r\n")
						return
					default:
						w("250 ok\r\n")
					}
				}
			}()
		}
	}()
	_, portStr, _ := net.SplitHostPort(ln.Addr().String())
	p, _ := strconv.Atoi(portStr)
	for _, kv := range [][2]string{
		{"smtp_host", "127.0.0.1"}, {"smtp_port", strconv.Itoa(p)},
		{"smtp_from", "site@example.com"}, {"smtp_pass", "super-secret-value"},
	} {
		if err := b.Store.SetOption(ctx, kv[0], kv[1]); err != nil {
			t.Fatal(err)
		}
	}
	b.ThemeID = func() string { return "azure-glass" }

	w := httptest.NewRecorder()
	b.handleMailTest(w, mailTestRequest(uid))
	if w.Code != 200 {
		t.Fatalf("mail-test = %d %s", w.Code, w.Body.String())
	}
	var wire string
	for i := 0; i < 40 && wire == ""; i++ {
		mu.Lock()
		if len(letters) > 0 {
			wire = letters[0]
		}
		mu.Unlock()
		time.Sleep(50 * time.Millisecond)
	}
	if wire == "" {
		t.Fatal("no letter captured")
	}
	// The summary lives in the base64 plain part, not the headers.
	body := wire
	if i := strings.Index(body, "Content-Type: text/plain"); i >= 0 {
		body = body[i:]
	}
	if i := strings.Index(body, "\r\n\r\n"); i >= 0 {
		body = body[i+4:]
	}
	if k := strings.Index(body, "\r\n--"); k >= 0 {
		body = body[:k]
	}
	raw, err := base64.StdEncoding.DecodeString(strings.ReplaceAll(strings.TrimSpace(body), "\r\n", ""))
	if err != nil {
		t.Fatalf("body not base64: %v", err)
	}
	wire = string(raw)
	for _, want := range []string{"127.0.0.1:" + portStr, "site@example.com", "azure-glass"} {
		if !strings.Contains(wire, want) {
			t.Errorf("summary missing %q", want)
		}
	}
	if strings.Contains(wire, "super-secret-value") {
		t.Error("the test letter leaked the SMTP credential")
	}
}
