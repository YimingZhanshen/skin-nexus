package mailer

import (
	"strconv"
	"strings"
)

// ConfigFrom resolves the SMTP account from a key lookup. Both APIs need the
// same answer — the user-facing password reset and the admin panel's mail
// status — so the rule lives once here: the panel option wins over the env
// default per key, and an empty host means the feature is off.
func ConfigFrom(get func(key string) string, siteName string) *Config {
	if get == nil {
		return nil
	}
	host := strings.TrimSpace(get("smtp_host"))
	if host == "" {
		return nil
	}
	port := 465
	if p, err := strconv.Atoi(strings.TrimSpace(get("smtp_port"))); err == nil && p > 0 && p < 65536 {
		port = p
	}
	cfg := &Config{
		Host:     host,
		Port:     port,
		User:     strings.TrimSpace(get("smtp_user")),
		Pass:     get("smtp_pass"),
		From:     strings.TrimSpace(get("smtp_from")),
		SiteName: siteName,
	}
	// A From address is mandatory (header + envelope), and it falls back to
	// smtp_user — with NEITHER set there is nothing to send FROM, so the
	// config is as unusable as a missing host. Treat it as unconfigured so the
	// user gets the honest 503 instead of a 200 that mails nothing (live
	// finding: a host-only flip answered 200 while every send died on "empty
	// address" in the log). Config state is address-independent, so the 503
	// leaks no user existence.
	if cfg.From == "" && cfg.User == "" {
		return nil
	}
	return cfg
}
