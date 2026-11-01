package accesslog

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestNilLoggerNeverPanics(t *testing.T) {
	var nilL *Logger
	nilL.Log(context.Background(), Entry{Kind: WebLogin, Outcome: OutcomeOK})     // nil receiver
	New(nil).Log(context.Background(), Entry{Kind: WebLogin, Outcome: OutcomeOK}) // nil sink
	// Kind/outcome are mandatory — half-filled entries are dropped.
	called := false
	l := New(func(context.Context, Entry) error { called = true; return nil })
	l.Log(context.Background(), Entry{Outcome: OutcomeOK})
	l.Log(context.Background(), Entry{Kind: WebLogin})
	if called {
		t.Error("incomplete entries must not reach the sink")
	}
}

func TestLogSwallowsSinkErrors(t *testing.T) {
	l := New(func(context.Context, Entry) error { return errors.New("db down") })
	l.Log(context.Background(), Entry{Kind: WebLogin, Outcome: OutcomeFail}) // must not panic
}

func TestLogTruncatesRuneSafe(t *testing.T) {
	var got Entry
	l := New(func(_ context.Context, e Entry) error { got = e; return nil })
	// 中 =3 bytes; cut at 64 bytes must land on a rune boundary.
	long := strings.Repeat("中", 100) // 300 bytes
	l.Log(context.Background(), Entry{Kind: WebLogin, Outcome: OutcomeOK,
		Who: long, UA: long, Detail: long, IP: long})
	for name, s := range map[string]string{"who": got.Who, "ua": got.UA, "detail": got.Detail, "ip": got.IP} {
		if len(s) > 255 {
			t.Errorf("%s too long: %d", name, len(s))
		}
		if !strings.HasPrefix(s, "中") || strings.Count(s, "中")*3 != len(s) {
			t.Errorf("%s not rune-aligned: len=%d", name, len(s))
		}
	}
	if len(got.Who) != 63 { // 64/3 → floor to whole runes = 21 runes = 63 bytes
		t.Errorf("who = %d bytes, want 63", len(got.Who))
	}
}

func TestLogDetachesContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // caller already gave up — the operator still gets the line
	called := false
	l := New(func(c context.Context, _ Entry) error {
		if c.Err() != nil {
			t.Error("sink received a cancelled context")
		}
		called = true
		return nil
	})
	l.Log(ctx, Entry{Kind: WebLogin, Outcome: OutcomeOK})
	if !called {
		t.Fatal("entry not recorded for cancelled request context")
	}
}
