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

	// access-log diagnostic mode (2026-11-05 拍板). The operator fixes the two
	// input failures differently — a bad/expired timestamp vs a window past the
	// 24h cap — so they carry distinct codes rather than one "invalid value".
	MsgDetailUntilPast   = "access_log.detail_until_past"    // not an absolute, future RFC3339 time
	MsgDetailUntilTooFar = "access_log.detail_until_too_far" // the expiry is past the 24-hour cap

	// auth / account
	MsgCredentials     = "auth.credentials" // wrong identifier or password
	MsgCurrentPassword = "auth.current_password"
	MsgEmailInvalid    = "auth.email_invalid"
	// The join gate needs an address to mail; signup is where that is paid for.
	MsgEmailRequired = "auth.email_required"
	// The gate now also refuses to *use* an account that has not clicked the
	// link: one code for the refusal, so the panel can render the state instead
	// of a spinner, and so "blocked" is never confused with "bad credentials".
	MsgEmailVerifyRequired = "auth.email_verify_required"
	MsgEmailTaken          = "auth.email_taken"
	MsgNicknameTaken       = "auth.nickname_taken"
	MsgNicknameInvalid     = "auth.nickname_invalid"
	MsgPasswordTooShort    = "auth.password_too_short"
	MsgIPLimit             = "auth.ip_limit"
	MsgSelfDeleteAdmin     = "auth.self_delete_forbidden"
	MsgAccountBanned       = "auth.banned"             // permission = -1, refused at the web door
	MsgPasswordSame        = "auth.password_unchanged" // new password equals the old one
	MsgIdentifierTaken     = "auth.identifier_taken"   // which of the two is taken is unknown
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
	MsgTextureBadType    = "texture.bad_type"
	MsgTextureNotOwner   = "texture.not_owner"
	MsgTextureModeration = "texture.moderation_ban" // struck: may not republish
	MsgTextureAvatarSkin = "texture.avatar_skin_only"
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
	// The panel's own copy of the same state (U17 follow-up): "contact the
	// administrator" is addressed to a player, and the reader here IS the
	// administrator. Two audiences, two sentences, one shared cause.
	MsgMailAdminNotConfigured = "mail.admin_not_configured"
	// The panel's test send (U17) names the transport failure itself. It cannot
	// reuse email.verification_failed: that sentence promises a retry will
	// deliver the verification link, which is not what an operator testing an
	// SMTP account needs to hear.
	MsgMailSendFailed = "mail.send_failed"
	// The recipient is a mailbox that can never receive (admin@localhost and
	// friends). Refused before any dial so the operator is told to set a real
	// address instead of reading a transport error they cannot act on.
	MsgMailRecipientPlaceholder = "mail.recipient_placeholder"
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

	// OIDC client registry + authorized-apps self-service
	MsgOIDCSharedClient  = "oidc.shared_client"   // launcher-wide client, not deletable
	MsgOIDCGrantNotFound = "oidc.grant_not_found" // no such jti, or it belongs to someone else
	MsgOIDCRevokePartial = "oidc.revoke_partial"  // some tokens revoked, some survived — never report success

	// setup / install
	MsgSetupDone      = "setup.already_done"
	MsgSetupBadInput  = "setup.bad_input"
	MsgSetupDBMissing = "setup.db_missing"
	MsgSetupTokenBad  = "setup.token_bad"

	// device authorisation (launcher flow). These answer the SPA's own
	// /api/v1/oidc/device/* calls, not the /oauth/token wire contract, so the
	// envelope can carry a code the way every other web endpoint does.
	MsgDeviceCodeInvalid = "device.code_invalid"
	MsgDeviceCodeExpired = "device.code_expired"
	MsgDeviceApproveFail = "device.approve_failed"
	// Consent-form failures the gate found when internal/oidc came under it:
	// three of these shipped as bare prose, one of them in Chinese, and a
	// browser cannot translate a sentence it has to match by spelling.
	MsgDeviceFormInvalid     = "device.form_invalid"
	MsgDeviceDenyFailed      = "device.deny_failed"
	MsgDeviceDecisionUnknown = "device.decision_unknown"
)
