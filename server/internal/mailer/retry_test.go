package mailer

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/textproto"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestRetryableClassification(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"smtp 451 greylist", &textproto.Error{Code: 451}, true},
		{"smtp 421 busy", &textproto.Error{Code: 421}, true},
		{"smtp 550 permanent", &textproto.Error{Code: 550}, false},
		{"smtp 535 auth", &textproto.Error{Code: 535}, false},
		{"smtp 354 odd", &textproto.Error{Code: 354}, false},
		{"dns not found", &net.DNSError{IsNotFound: true}, false},
		{"dns temporary", &net.DNSError{IsTemporary: true}, true},
		{"dns timeout", &net.DNSError{IsTimeout: true}, true},
		{"deadline exceeded", os.ErrDeadlineExceeded, true},
		{"context deadline", context.DeadlineExceeded, false},
		{"closed pipe", io.ErrClosedPipe, true},
		{"peer hung up", io.EOF, true},
		{"dial refused", &net.OpError{Op: "dial", Err: errors.New("refused")}, true},
		{"read reset", &net.OpError{Op: "read", Err: errors.New("reset")}, true},
		{"unknown", errors.New("x"), false},
		{"committed quit", &committedError{errors.New("quit")}, false},
		{"committed even if transient inside", &committedError{&textproto.Error{Code: 421}}, false},
	}
	for _, c := range cases {
		if got := retryable(c.err); got != c.want {
			t.Errorf("%s: retryable = %v, want %v", c.name, got, c.want)
		}
	}
}

// retryServer is a scriptable SMTP peer: one behaviour per test, counting
// sessions and the MAIL/DATA commands so a retry is observable as two sessions.
type retryServer struct {
	mu       sync.Mutex
	mode     string
	sessions int
	from     int
	data     int
}

func startRetryServer(t *testing.T, mode string) (host string, port int, s *retryServer) {
	t.Helper()
	s = &retryServer{mode: mode}
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
			go s.serve(conn)
		}
	}()
	h, p, err := net.SplitHostPort(ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	fmt.Sscanf(p, "%d", &port)
	return h, port, s
}

func (s *retryServer) serve(conn net.Conn) {
	s.mu.Lock()
	s.sessions++
	n := s.sessions
	s.mu.Unlock()
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
	if s.mode == "silent" {
		time.Sleep(10 * time.Second) // accept and never greet: burns the session deadline
	}
	if s.mode == "rstfirst" && n == 1 {
		return // close immediately: dial succeeds, greeting fails
	}
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
	w := func(x string) { _, _ = conn.Write([]byte(x)) }
	w("220 retry\r\n")
	inData := false
	for {
		line := readLine()
		if line == "" && !inData {
			return
		}
		if inData {
			if line == "." {
				inData = false
				s.mu.Lock()
				s.data++
				s.mu.Unlock()
				w("250 queued\r\n")
			}
			continue
		}
		switch {
		case strings.HasPrefix(line, "EHLO"), strings.HasPrefix(line, "HELO"):
			w("250-retry\r\n250 PLAIN\r\n")
		case strings.HasPrefix(line, "AUTH"):
			if s.mode == "authfail" {
				w("535 no\r\n")
			} else {
				w("235 ok\r\n")
			}
		case strings.HasPrefix(line, "MAIL"):
			s.mu.Lock()
			s.from++
			s.mu.Unlock()
			w("250 ok\r\n")
		case strings.HasPrefix(line, "RCPT"):
			if s.mode == "greylist" && n == 1 {
				w("451 greylisted\r\n")
			} else if s.mode == "permanent" {
				w("550 no such user\r\n")
			} else {
				w("250 ok\r\n")
			}
		case strings.HasPrefix(line, "DATA"):
			inData = true
			w("354 go\r\n")
		case strings.HasPrefix(line, "QUIT"):
			if s.mode == "quitfail" {
				return // DATA already queued; die without answering QUIT
			}
			w("221 bye\r\n")
			return
		default:
			w("250 ok\r\n")
		}
	}
}

func (s *retryServer) counts() (sessions, from, data int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.sessions, s.from, s.data
}

func retryConfig(host string, port int) *Config {
	return &Config{Host: host, Port: port, User: "u@x.com", Pass: "p", From: "u@x.com", SiteName: "Rig"}
}

// These tests mutate package-level window vars, so none of them may run in
// parallel (same discipline as webapi.mailReplyBudget).

func TestSendRetriesOnceOnTemporarySMTP(t *testing.T) {
	host, port, s := startRetryServer(t, "greylist")
	err := retryConfig(host, port).Send("u@x.com", "s", "b")
	if err != nil {
		t.Fatalf("greylist then success should deliver: %v", err)
	}
	sessions, from, data := s.counts()
	if sessions != 2 || from != 2 || data != 1 {
		t.Fatalf("sessions/from/data = %d/%d/%d, want 2/2/1 (one retry, one delivery)", sessions, from, data)
	}
}

func TestSendDoesNotRetryPermanentSMTP(t *testing.T) {
	host, port, s := startRetryServer(t, "permanent")
	err := retryConfig(host, port).Send("u@x.com", "s", "b")
	if err == nil {
		t.Fatal("550 must fail")
	}
	if sessions, _, _ := s.counts(); sessions != 1 {
		t.Fatalf("sessions = %d, want 1 (a permanent refusal is not retried)", sessions)
	}
}

func TestSendDoesNotRetryAuthFailure(t *testing.T) {
	host, port, s := startRetryServer(t, "authfail")
	start := time.Now()
	err := retryConfig(host, port).Send("u@x.com", "s", "b")
	if err == nil {
		t.Fatal("535 must fail")
	}
	if sessions, _, _ := s.counts(); sessions != 1 {
		t.Fatalf("sessions = %d, want 1 (retrying auth can lock the account)", sessions)
	}
	if time.Since(start) > time.Second {
		t.Fatal("auth failure must not sleep the backoff")
	}
}

func TestSendDoesNotRetryWhenSomeoneIsWaiting(t *testing.T) {
	host, port, s := startRetryServer(t, "greylist")
	cfg := retryConfig(host, port)
	cfg.Budget = 2 * time.Second
	err := cfg.Send("u@x.com", "s", "b")
	if err == nil {
		t.Fatal("greylist with a waiting browser must fail, not retry")
	}
	if sessions, _, _ := s.counts(); sessions != 1 {
		t.Fatalf("sessions = %d, want 1 (Budget > 0 disables the retry)", sessions)
	}
}

func TestNoRetryAfterDataAccepted(t *testing.T) {
	host, port, s := startRetryServer(t, "quitfail")
	err := retryConfig(host, port).Send("u@x.com", "s", "b")
	if err == nil {
		t.Fatal("a failed QUIT after queued DATA must surface as an error")
	}
	sessions, _, data := s.counts()
	if sessions != 1 || data != 1 {
		t.Fatalf("sessions/data = %d/%d, want 1/1 — retrying here delivers the letter twice", sessions, data)
	}
}

func TestRetrySharesOneDeadline(t *testing.T) {
	old := defaultSessionWindow
	defaultSessionWindow = 600 * time.Millisecond
	t.Cleanup(func() { defaultSessionWindow = old })
	host, port, s := startRetryServer(t, "silent")
	err := retryConfig(host, port).Send("u@x.com", "s", "b")
	if err == nil {
		t.Fatal("a silent server must fail")
	}
	if sessions, _, _ := s.counts(); sessions != 1 {
		t.Fatalf("sessions = %d, want 1 (the first attempt ate the whole shared deadline)", sessions)
	}
}

func TestRetryStaysInsideTheWindow(t *testing.T) {
	old := defaultSessionWindow
	defaultSessionWindow = 3 * time.Second
	t.Cleanup(func() { defaultSessionWindow = old })
	host, port, s := startRetryServer(t, "rstfirst")
	start := time.Now()
	err := retryConfig(host, port).Send("u@x.com", "s", "b")
	if err != nil {
		t.Fatalf("reset then success should deliver: %v", err)
	}
	if elapsed := time.Since(start); elapsed > 3500*time.Millisecond {
		t.Fatalf("took %v — the second attempt must reuse the remaining window, not a fresh one", elapsed)
	}
	if sessions, _, _ := s.counts(); sessions != 2 {
		t.Fatalf("sessions = %d, want 2", sessions)
	}
}
