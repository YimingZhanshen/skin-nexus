// Package i18n is the message-key layer for API error responses.
//
// Every non-2xx body carries a stable machine-readable `code` alongside the
// human-readable `message`; the frontend translates `code` through its language
// packs and falls back to `message` when a code is unknown. Nothing imports
// this package from the backend's language pack side, so it stays a leaf: no
// dependencies, no lookup table, only the namespace.
package i18n

// Error codes for the HTTP envelope `{error, code, message}`.
//
// Prefixing a constant with `Msg` is the greppable convention — `i18n\.Msg`
// finds every call site — and `<area>.<reason>` keeps one namespace per area.
//
// Naming is `<area>.<reason>`, lowercase, dot-separated — codes are stable API
// surface: renaming one breaks every frontend
// translation and any script that branches on it, so retire a code by leaving it
// here and stopping use, never by redefining it to mean something else.
//
// Business codes owned by the account service (player.*, texture.*, library.*,
// account.forbidden, texture.duplicate_public …) live in internal/account and
// are not repeated here — codes_test.go asserts the two sets never collide.
const (
	// transport / generic
	MsgBadBody       = "common.bad_body"      // undecodable JSON or multipart
	MsgMissingField  = "common.missing_field" // a required field is absent
	MsgNoFields      = "common.no_fields"     // PATCH with nothing to change
	MsgInvalidValue  = "common.invalid_value" // enum/format a setting rejects
	MsgTooLong       = "common.too_long"      // length limit exceeded
	MsgNotFound      = "common.not_found"
	MsgForbidden     = "common.forbidden"
	MsgUnauthorized  = "common.unauthorized"
	MsgConflict      = "common.conflict"
	MsgAlreadyDone   = "common.already_handled" // a record someone else closed
	MsgUnknownAction = "common.unknown_action"
	MsgRateLimited   = "common.rate_limited"
	MsgUnavailable   = "common.unavailable"    // dependency not configured
	MsgSessionFailed = "common.session_failed" // session store write failed
	MsgInternal      = "common.internal"

	// admin surface guards
	MsgSuperAdminRequired = "admin.super_admin_required" // permission=2 account needed
	MsgWebSessionRequired = "admin.web_session_required" // X-Admin-Token not accepted
	MsgOriginMismatch     = "admin.origin_mismatch"      // same-origin check failed
	MsgSettingUnknown     = "option.unknown_key"
	MsgOptionSaveFailed   = "option.save_failed"

	// auth / account
	MsgCredentials      = "auth.credentials" // wrong identifier or password
	MsgCurrentPassword  = "auth.current_password"
	MsgEmailInvalid     = "auth.email_invalid"
	MsgEmailTaken       = "auth.email_taken"
	MsgNicknameTaken    = "auth.nickname_taken"
	MsgNicknameInvalid  = "auth.nickname_invalid"
	MsgPasswordTooShort = "auth.password_too_short"
	MsgRegisterFailed   = "auth.register_failed"
	MsgIPLimit          = "auth.ip_limit"
	MsgSelfDeleteAdmin  = "auth.self_delete_forbidden"
	MsgPasswordSame     = "auth.password_unchanged" // new password equals the old one
	MsgIdentifierTaken  = "auth.identifier_taken"   // which of the two is taken is unknown
	// One code for every failed token check on /password/reset: the handler
	// deliberately collapses them into one message so the endpoint never says
	// which check failed, and the code must not leak it either.
	MsgResetLink = "auth.reset_link_invalid"

	// invitation gate
	MsgInviteRequired = "invite.required"
	MsgInviteInvalid  = "invite.invalid"

	// textures / closet / players
	//
	// `texture.source_url_invalid`, `player.not_found` and `player.name_taken`
	// are deliberately absent: the account service raises them and handlers only
	// forward the code they were given. Declaring a second copy here would let a
	// handler emit a code the bridge never produces.
	MsgTextureNotFound   = "texture.not_found"
	MsgTextureNotVisible = "texture.not_visible" // exists, not visible to caller
	MsgTextureBadID      = "texture.bad_id"
	MsgTextureNotOwner   = "texture.not_owner"
	MsgTextureModeration = "texture.moderation_ban" // struck: may not republish
	MsgTextureAvatarSkin = "texture.avatar_skin_only"
	MsgTextureSourceBad  = "texture.source_invalid" // the source enum is not one of three
	MsgTextureModelBad   = "texture.model_invalid"  // PATCH model outside default|slim
	MsgClosetNotFound    = "closet.not_found"
	MsgClosetBadID       = "closet.bad_id"
	MsgLikeMissing       = "skinlib.like_missing" // unlike a texture that is not liked
	MsgPlayerNotOwner    = "player.not_owner"
	MsgPlayerBadID       = "player.bad_id" // malformed or absent player id
	MsgPlayerUUIDBad     = "player.uuid_invalid"
	MsgPlayerNameRuleBad = "player.name_rule_invalid"

	// reports
	MsgReportDuplicate = "report.duplicate"
	MsgReportQuota     = "report.quota_exceeded"
	MsgReportSelf      = "report.self_report"
	MsgReportFailed    = "report.save_failed"

	// mail
	MsgMailNotConfigured = "mail.not_configured"

	// federation (MUA Union)
	MsgUnionConvergeFailed = "union.converge_failed" // hub accepted, local write failed
	MsgUnionUUIDConflict   = "union.uuid_conflict"   // needs an operator to merge
	MsgUnionUpstream       = "union.upstream_error"  // hub unreachable or answered wrongly

	// appearance / themes / plugins
	MsgThemeNotFound    = "theme.not_found"
	MsgThemeBadID       = "theme.bad_id"
	MsgPluginNotFound   = "plugin.not_found"
	MsgPluginNoConfig   = "plugin.no_config"
	MsgPluginNoRollback = "plugin.no_rollback"
	MsgGuardDenied      = "plugin.guard_denied"  // a plugin refused the write; its reason is the message
	MsgPluginForm       = "plugin.form_rejected" // the plugin's own field validator threw

	// setup / install
	MsgSetupDone      = "setup.already_done"
	MsgSetupBadInput  = "setup.bad_input"
	MsgSetupDBMissing = "setup.db_missing"
)
