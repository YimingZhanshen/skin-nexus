// Package i18n is the message-key layer for API error responses.
//
// Every non-2xx body carries a stable machine-readable `code` alongside the
// legacy human-readable `message`; the frontend translates `code` through its
// language packs and falls back to `message` when a code is unknown. Codes are
// declared in codes.go — this file only keeps the identity helper so call sites
// stay greppable (`i18n.MsgTextureNotFound`) and refactors stay mechanical.
package i18n

// Msg returns the key itself (identity).
func Msg(key string) string { return key }
