package accesslog

import (
	"encoding/json"
	"strings"
)

// Body scrubbing (2026-11-09 rig finding).
//
// body_excerpt is the request body, and the two surfaces that log a detail row
// take their body from a POST that carries a PASSWORD: /api/yggdrasil/…
// /authenticate and /api/v1/login. credential_ref has a fixed 24h clock;
// body_excerpt follows the retention knob (7..90 days), and the reader API
// hands it to any admin with permission ≥ 1. Without this scrub, "diagnostic
// mode stores a plaintext password for a quarter" is not a hypothetical — it is
// what the table does, and the promise in deploy.md §2.6 would be false.

// secretFieldNames are the JSON keys whose values are credentials. Matched
// case-insensitively: the yggdrasil wire uses camelCase, a form POST may not.
var secretFieldNames = []string{
	"password", "oldPassword", "newPassword", "currentPassword", "passwd", "pwd",
	"accessToken", "clientToken", "securityToken", "refreshToken", "token",
	"authorization", "auth", "secret", "client_secret", "code", "deviceCode",
	"userCode", "setup_token", "setupToken",
}

// SecretFieldNames copies the list (tests and docs name what is treated as
// secret-shaped; callers must not be able to edit the gate).
func SecretFieldNames() []string {
	return append([]string(nil), secretFieldNames...)
}

// bodyRedacted replaces a value the scrub removed. Kept short and obviously
// synthetic so an operator never reads it as captured data.
const bodyRedacted = "[redacted]"

// bodyDroppedMarker stands in for a body that mentions a secret but cannot be
// parsed: the value is worth more than the excerpt, so nothing is kept.
const bodyDroppedMarker = "[body redacted: unparseable body mentioning credential fields]"

// ScrubBody removes credential values from a request body while keeping the
// shape a debugging session needs.
//
// Rules, in order:
//  1. no secret-shaped name anywhere → the body is returned byte-for-byte
//     (over-redaction would defeat the reason the window was opened);
//  2. parseable JSON → every secret-shaped value is replaced wherever it is
//     nested, and if nothing was replaced the original text is kept;
//  3. unparseable but secret-shaped → the whole body is replaced by the marker.
//     A form-encoded "password=…" is exactly the case a JSON walker misses.
//
// It never returns a value it just read, and it is idempotent: applying it to
// its own output changes nothing, which matters because InsertAccessDetail
// sanitizes a second time on the way into the column.
func ScrubBody(body string) string {
	if body == "" {
		return body
	}
	lower := strings.ToLower(body)
	touched := false
	for _, name := range secretFieldNames {
		if strings.Contains(lower, strings.ToLower(name)) {
			touched = true
			break
		}
	}
	if !touched {
		return body
	}
	var v any
	if err := json.Unmarshal([]byte(body), &v); err != nil {
		return bodyDroppedMarker
	}
	if !redactValue(v) {
		return body // the name appeared in some other field's text, not as a key
	}
	out, err := json.Marshal(v)
	if err != nil {
		return bodyDroppedMarker
	}
	return string(out)
}

// redactValue walks a decoded JSON tree and reports whether it changed
// anything. Keys are compared case-insensitively against the secret list.
func redactValue(v any) bool {
	switch t := v.(type) {
	case map[string]any:
		changed := false
		for k, val := range t {
			if isSecretKey(k) {
				if s, ok := val.(string); ok && s == bodyRedacted {
					continue // already scrubbed: keeps the pass idempotent
				}
				t[k] = bodyRedacted
				changed = true
				continue
			}
			if redactValue(val) {
				changed = true
			}
		}
		return changed
	case []any:
		changed := false
		for _, item := range t {
			if redactValue(item) {
				changed = true
			}
		}
		return changed
	default:
		return false
	}
}

func isSecretKey(k string) bool {
	lower := strings.ToLower(strings.TrimSpace(k))
	for _, name := range secretFieldNames {
		if lower == strings.ToLower(name) {
			return true
		}
	}
	return false
}
