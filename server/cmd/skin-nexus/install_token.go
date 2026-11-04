package main

// The install wizard is open exactly while the site has no accounts — which for
// a container behind a public interface is a window measured in minutes to
// hours, during which the first anonymous POST to /api/v1/setup used to become
// the super admin. installToken turns that window into one that needs a value
// the operator already holds: SKIN_SETUP_TOKEN if set, otherwise a fresh token
// for this boot, printed to the log.

import (
	"crypto/rand"
	"strings"
)

// installTokenAlphabet drops the glyphs that get misread when someone copies a
// token out of a terminal (0/O, 1/l/I).
const installTokenAlphabet = "23456789abcdefghjkmnpqrstuvwxyz"

// installToken returns the wizard token and whether it was generated (as
// opposed to supplied by the operator).
func installToken(fromEnv string) (string, bool) {
	if v := strings.TrimSpace(fromEnv); v != "" {
		return v, false
	}
	const want = 10
	out := make([]byte, 0, want)
	buf := make([]byte, 64)
	// Rejection sampling: 256 is not a multiple of the alphabet length, so
	// taking the modulo would bias the leading characters.
	limit := 256 - (256 % len(installTokenAlphabet))
	for tries := 0; len(out) < want && tries < 64; tries++ {
		if _, err := rand.Read(buf); err != nil {
			return "", false
		}
		for _, b := range buf {
			if int(b) >= limit {
				continue
			}
			out = append(out, installTokenAlphabet[int(b)%len(installTokenAlphabet)])
			if len(out) == want {
				break
			}
		}
	}
	if len(out) != want {
		return "", false
	}
	return string(out), true
}
