package verifygate

import (
	"testing"
	"time"
)

// The two surfaces (launcher and web panel) now decide through this one
// function, so the table has to cover the corners both of them rely on:
// an unread created_at must not buy an infinite window, and a grace of zero
// must not read as "no policy configured".
func TestBlocksCoversTheAgreedCorners(t *testing.T) {
	now := time.Date(2026, 11, 12, 8, 0, 0, 0, time.UTC)
	recent := now.Add(-time.Hour)
	old := now.Add(-30 * 24 * time.Hour)

	cases := []struct {
		name string
		f    Facts
		want bool
	}{
		{"gate off, unverified, ancient account", Facts{Enabled: false, CreatedAt: old, Now: now}, false},
		{"gate on, verified", Facts{Enabled: true, Verified: true, CreatedAt: old, Now: now}, false},
		{"gate on, unverified, no grace configured", Facts{Enabled: true, CreatedAt: recent, Now: now}, true},
		{"gate on, unverified, inside the window", Facts{Enabled: true, CreatedAt: recent, Grace: 7 * 24 * time.Hour, Now: now}, false},
		{"gate on, unverified, window spent", Facts{Enabled: true, CreatedAt: old, Grace: 7 * 24 * time.Hour, Now: now}, true},
		{
			"gate on, unverified, caller never read created_at",
			Facts{Enabled: true, Grace: 30 * 24 * time.Hour, Now: now},
			true,
		},
		{"negative grace is not a window", Facts{Enabled: true, CreatedAt: recent, Grace: -time.Hour, Now: now}, true},
		{"zero clock falls back instead of blocking everything", Facts{Enabled: true, CreatedAt: time.Now().Add(-time.Minute), Grace: 24 * time.Hour}, false},
	}

	for _, c := range cases {
		if got := Blocks(c.f); got != c.want {
			t.Errorf("%s: Blocks(%+v) = %v, want %v", c.name, c.f, got, c.want)
		}
	}
}
