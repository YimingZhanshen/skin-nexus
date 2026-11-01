// Package i18n is the message-key layer for API error responses.
//
// Responses carry a stable machine-readable `code` alongside the legacy
// human-readable `message`; frontends translate `code` through their language
// packs and fall back to `message` when a code is unknown. Backend language
// packs land later — this package only defines the key namespace and a small
// sample so every new call site has a pattern to follow.
package i18n

// Message keys. Keep them namespaced by area: <area>.<reason>.
const (
	// generic
	MsgBadBody      = "common.bad_body"
	MsgNotFound     = "common.not_found"
	MsgInternal     = "common.internal"
	MsgForbidden    = "common.forbidden"
	MsgUnauthorized = "common.unauthorized"

	// setup / install
	MsgSetupDone      = "setup.already_done"
	MsgSetupBadInput  = "setup.bad_input"
	MsgSetupDBMissing = "setup.db_missing"

	// appearance / theme
	MsgThemeNotFound = "theme.not_found"
	MsgThemeBadID    = "theme.bad_id"
	MsgOptSaveFailed = "option.save_failed"
)

// Msg returns the key itself (identity). Call sites pass i18n.Msg(...) so the
// key namespace stays greppable and refactors stay mechanical.
func Msg(key string) string { return key }
