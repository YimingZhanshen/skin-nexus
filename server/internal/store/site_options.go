package store

import (
	"context"
	"regexp"
	"strconv"
	"sync"
	"unicode/utf8"
)

// Site options consumption helpers + the player-name rule group (B4 管理
// 面板对齐, 2026-10-24). Keys use the blessing原名 — the migration importer
// copies production options verbatim, so a renamed key would strand the
// operator's live value as a dead row.

// Site options consumption (B4): keys use the blessing原名 — the migration
// importer copies production options verbatim, so a renamed key would strand
// the operator's live value as a dead row.

// Announcement returns the site announcement (blessing `announcement`,
// plain text — markdown rendering is a deliberate non-goal until an XSS
// sanitizer is chosen, see dev plan §7).
func (m *Mysql) Announcement(ctx context.Context) string {
	v, _, _ := m.GetOption(ctx, "announcement")
	return v
}

// RegsPerIP returns the per-IP registration cap (blessing `regs_per_ip`,
// default 3 — config/options.php). 0 disables the cap.
func (m *Mysql) RegsPerIP(ctx context.Context) int {
	v, ok, err := m.GetOption(ctx, "regs_per_ip")
	if err != nil || !ok || v == "" {
		return 3
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < 0 {
		return 3
	}
	return n
}

// CountUsersByIP counts accounts registered from one IP (blessing
// User::where('ip', $ip)->count()).
func (m *Mysql) CountUsersByIP(ctx context.Context, ip string) (int, error) {
	var n int
	err := m.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM users WHERE ip = ?`, ip).Scan(&n)
	return n, err
}

// MaxUploadKB returns the admin upload-size knob in KiB (blessing
// `max_upload_file_size`, default 1024). Values above hardMaxKB (the
// pipeline's anti-bomb ceiling) are clamped — the option is a knob, never
// a bypass.
func (m *Mysql) MaxUploadKB(ctx context.Context, hardMaxKB int) int {
	v, ok, err := m.GetOption(ctx, "max_upload_file_size")
	if err != nil || !ok || v == "" {
		if hardMaxKB < 1024 {
			return hardMaxKB
		}
		return 1024
	}
	n, err := strconv.Atoi(v)
	if err != nil || n <= 0 {
		return 1024
	}
	if n > hardMaxKB {
		return hardMaxKB
	}
	return n
}

// ---- player name rule group (blessing App\Rules\PlayerName + min/max) ----

// cached compiled rules keyed by "rule|regexp" (options can flip at runtime;
// compile cost is trivial but the cache keeps hot paths allocation-light).
var nameRuleCache sync.Map // string -> *regexp.Regexp or nil (utf8 rule)

// ValidatePlayerName enforces the configured character-name rule
// (player_name_rule + custom_player_name_regexp + player_name_length_min/
// max). Defaults mirror config/options.php: official / 3-16. Only NEW
// writes go through here (存量名字不回溯校验 — 数据兼容铁律).
//
// Rules (blessing App\Rules\PlayerName.php):
//
//	official: ^[A-Za-z0-9_]+$
//	cjk:      ^[A-Za-z0-9_§一-龥]+$
//	utf8:     valid UTF-8, no whitespace
//	custom:   custom_player_name_regexp (empty = allow all)
func (m *Mysql) ValidatePlayerName(ctx context.Context, name string) error {
	rule, _, _ := m.GetOption(ctx, "player_name_rule")
	if rule == "" {
		rule = "official"
	}
	minL, maxL := 3, 16
	if v, ok, err := m.GetOption(ctx, "player_name_length_min"); err == nil && ok {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			minL = n
		}
	}
	if v, ok, err := m.GetOption(ctx, "player_name_length_max"); err == nil && ok {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			maxL = n
		}
	}
	// length in runes (blessing uses mb-aware Laravel validators)
	n := utf8.RuneCountInString(name)
	if n < minL || n > maxL {
		return errPlayerNameLen(minL, maxL)
	}
	switch rule {
	case "official":
		if !compiled(`^[A-Za-z0-9_]+$`, "official").MatchString(name) {
			return errPlayerNameRule(rule)
		}
	case "cjk":
		if !compiled(`^[A-Za-z0-9_§一-龥]+$`, "cjk").MatchString(name) {
			return errPlayerNameRule(rule)
		}
	case "utf8":
		if !utf8.ValidString(name) || regexp.MustCompile(`\s`).MatchString(name) {
			return errPlayerNameRule(rule)
		}
	case "custom":
		custom, _, _ := m.GetOption(ctx, "custom_player_name_regexp")
		if custom == "" {
			return nil // allow-all (blessing: `?: '/.*/'`)
		}
		re, err := compileCached(custom)
		if err != nil {
			return errPlayerNameRule(rule) // invalid stored regexp fails closed
		}
		if !re.MatchString(name) {
			return errPlayerNameRule(rule)
		}
	default:
		// unknown rule value fails closed to official (never open the gate)
		if !compiled(`^[A-Za-z0-9_]+$`, "official").MatchString(name) {
			return errPlayerNameRule(rule)
		}
	}
	return nil
}

func compiled(expr, key string) *regexp.Regexp {
	if v, ok := nameRuleCache.Load(key); ok {
		return v.(*regexp.Regexp)
	}
	re := regexp.MustCompile(expr)
	nameRuleCache.Store(key, re)
	return re
}

func compileCached(expr string) (*regexp.Regexp, error) {
	if v, ok := nameRuleCache.Load(expr); ok {
		return v.(*regexp.Regexp), nil
	}
	re, err := regexp.Compile(expr)
	if err != nil {
		return nil, err // never cached: an invalid stored regexp re-fails per call
	}
	nameRuleCache.Store(expr, re)
	return re, nil
}

// error constructors keep the messages in one place. They reach the wire as the
// envelope's `message`, so they stay English like every other answer on this
// host; the browser shows the translated `player.name_invalid` instead.
func errPlayerNameLen(min, max int) error {
	return &playerNameErr{msg: "player name must be " + strconv.Itoa(min) + "-" + strconv.Itoa(max) + " characters"}
}
func errPlayerNameRule(rule string) error {
	return &playerNameErr{msg: "player name does not match the site naming rule (" + rule + ")"}
}

type playerNameErr struct{ msg string }

func (e *playerNameErr) Error() string { return e.msg }
