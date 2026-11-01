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
	MsgBadID         = "common.bad_id"         // a path or body id that is not a number, or is not positive
	MsgInternal      = "common.internal"

	// admin surface guards
	MsgSuperAdminRequired = "admin.super_admin_required" // permission=2 account needed
	MsgWebSessionRequired = "admin.web_session_required" // X-Admin-Token not accepted
	MsgOriginMismatch     = "admin.origin_mismatch"      // same-origin check failed
	MsgAdminTokenInvalid  = "admin.token_invalid"        // neither a valid X-Admin-Token nor an admin session
	// The blessing user-management guards, one code each so the panel can say
	// which rule stopped the edit rather than echoing prose it cannot localise.
	MsgAdminSelfEdit    = "admin.self_edit_forbidden"
	MsgAdminRank        = "admin.rank_required" // only strictly lower ranks may be touched
	MsgSettingUnknown   = "option.unknown_key"
	MsgOptionSaveFailed = "option.save_failed"

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
	MsgInviteMissing  = "invite.code_not_found" // no such code to revoke or inspect

	// user administration (the panel's user table; auth.* above covers the
	// player's own credentials)
	MsgUserNotFound       = "user.not_found"
	MsgUserAdminProtected = "user.admin_protected" // uid 1 and permission=2 accounts

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
	// The rename touched a UUID other accounts already claim — an operator has
	// to resolve it, so the code is the actionable half.
	MsgPlayerUUIDDrift = "player.uuid_drift"

	// reports
	MsgReportDuplicate = "report.duplicate"
	MsgReportQuota     = "report.quota_exceeded"
	MsgReportSelf      = "report.self_report"
	MsgReportFailed    = "report.save_failed"
	MsgReportNotFound  = "report.not_found"

	// mail
	MsgMailNotConfigured = "mail.not_configured"
	// Email verification (2026-11-02). link_invalid covers every failed token
	// check for the same reason auth.reset_link_invalid does: the endpoint must
	// not say which check failed.
	MsgEmailNotSet          = "email.not_set"             // account has no address to verify
	MsgEmailAlreadyVerified = "email.already_verified"    // nothing to send
	MsgEmailLink            = "email.link_invalid"        // unknown / expired / used token
	MsgEmailSendFailed      = "email.verification_failed" // mail transport failed

	// federation (MUA Union)
	MsgUnionConvergeFailed = "union.converge_failed" // hub accepted, local write failed
	MsgUnionUUIDConflict   = "union.uuid_conflict"   // needs an operator to merge
	MsgUnionUpstream       = "union.upstream_error"  // hub unreachable or answered wrongly

	// appearance / themes / plugins
	MsgThemeNotFound    = "theme.not_found"
	MsgThemeActive      = "theme.active" // cannot uninstall the live theme
	MsgThemeBadID       = "theme.bad_id"
	MsgPluginNotFound   = "plugin.not_found"
	MsgPluginNoConfig   = "plugin.no_config"
	MsgPluginNoRollback = "plugin.no_rollback"
	MsgPluginOpFailed   = "plugin.operation_failed" // the runtime refused; the reason is in the log
	// Install refusals the panel must show rather than hide: each one is a
	// decision the operator can overturn in the dialog that follows.
	MsgPluginUnverified = "plugin.unverified"      // local package, unknown or no signer key
	MsgPluginBlocked    = "plugin.install_blocked" // dependency or conflict stops the plan
	MsgPluginConsent    = "plugin.consent_required"
	MsgDigestMismatch   = "plugin.digest_mismatch" // the expected zip SHA did not match
	MsgGuardDenied      = "plugin.guard_denied"    // a plugin refused the write; its reason is the message
	MsgPluginForm       = "plugin.form_rejected"   // the plugin's own field validator threw

	// signed registries (plugin market and theme market share the discipline)
	MsgMarketUpstream = "market.upstream_error" // registry unreachable or answered wrongly

	// background jobs
	MsgJobNotFound     = "job.not_found"
	MsgJobNotResumable = "job.not_resumable" // cancelled past its replay window

	// OIDC client registry
	MsgOIDCSharedClient = "oidc.shared_client" // launcher-wide client, not deletable

	// setup / install
	MsgSetupDone      = "setup.already_done"
	MsgSetupBadInput  = "setup.bad_input"
	MsgSetupDBMissing = "setup.db_missing"
)
