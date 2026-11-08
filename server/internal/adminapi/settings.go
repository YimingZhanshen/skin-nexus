package adminapi

import (
	"context"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"skin-nexus/server/internal/i18n"
)

// Settings panel (user directive, 2026-10-23): site knobs that used to live
// in env vars move into the admin UI. Request-time keys apply immediately;
// boot-time keys apply after the container restarts (routing/wiring built at
// startup) — the UI shows that per key.

type settingMeta struct {
	Key     string `json:"key"`
	Value   string `json:"value"`
	Default string `json:"default"` // env-derived fallback (displayed as placeholder)
	Restart bool   `json:"restart"`
	// DescKey names the sentence in the locale dictionary (`settings.<key>.desc`).
	// This used to carry the Chinese sentence itself, which made the English panel
	// a wall of Chinese: the server was the only author of the copy.
	DescKey string `json:"descKey"`
	Group   string `json:"group"` // panel section id (basic/register/mail/security/ygg/texture/union)
	// Secret keys never return a value: `set` says whether one exists. The UI
	// mask used to be the only thing standing between a credential and the page
	// source, so anything reading the response — a log, an XSS, a saved copy —
	// saw the plaintext.
	Secret bool `json:"secret"`
	Set    bool `json:"set"`
}

// settingSpec is one panel knob: its option key, whether applying it needs a
// restart, which panel section it belongs to, and whether its value is a secret
// (write-only over this endpoint). Its sentence lives in the locale dictionaries
// under settings.<key>.desc, and check-locales requires it on both sides.
type settingSpec struct {
	key     string
	restart bool
	group   string
	secret  bool
}

// settingsWhitelist: key / restart / group / secret. Group ids drive the panel
// section grouping (UI-ordered by the frontend's group sequence; unknown ids
// fall to the end) — keep them lowercase-stable, they are part of the API.
var settingsWhitelist = []settingSpec{
	{"site_name", true, "basic", false},
	{"server_name", true, "basic", false},
	{"site_url", true, "basic", false}, // B4 (blessing 原名 — 迁移导入器原样搬运生产 options，改名会让存量值成死键)
	{"announcement", false, "basic", false},
	{"invite_required", false, "register", false},
	{"require_verification", false, "register", false},
	{"email_verify_grace_days", false, "register", false},
	{"regs_per_ip", false, "register", false},
	{"smtp_host", false, "mail", false},
	{"smtp_port", false, "mail", false},
	{"smtp_user", false, "mail", false},
	{"smtp_pass", false, "mail", true},
	{"smtp_from", false, "mail", false},
	{"rate_limit_auth", true, "security", false},
	{"login_fail_limit", true, "security", false},
	{"cdn_enabled", false, "security", false},
	{"cdn_headers", false, "security", false},
	// 访问日志诊断模式明细保留天数（2026-11-05 拍板，默认 90）。归入 security 组：
	// 面板现有分组里没有独立的日志/留存组，明细行的留存即数据最小化策略，与安全同节；
	// 新增组 id 会让 SettingsView 的 GROUP_ORDER（本切片不改动）把它甩到末尾且无标题。
	{"access_detail_retention_days", false, "security", false},
	{"skin_domains", true, "ygg", false},
	{"ygg_token_expire_1", false, "ygg", false}, // B5（blessing yggdrasil-connect 原名，callbacks.php:93-98 默认值）
	{"ygg_token_expire_2", false, "ygg", false},
	{"ygg_tokens_limit", false, "ygg", false},
	{"ygg_search_profile_max", false, "ygg", false},
	{"ygg_rate_limit", false, "ygg", false},
	{"ygg_mojang_fallback", false, "ygg", false}, // A2（2026-10-26）：官方 sessionserver 兜底，热开关——关=本地未命中直接 204
	{"max_upload_file_size", false, "texture", false},
	{"player_name_rule", false, "texture", false},
	{"custom_player_name_regexp", false, "texture", false},
	{"player_name_length_min", false, "texture", false},
	{"player_name_length_max", false, "texture", false},
	{"texture_name_regexp", false, "texture", false},
	{"union_enabled", false, "union", false}, // Union（blessing yggdrasil-connect 原名；union_enabled 为本项目新增键——老站用插件启停表达，无 option 行。热切换 2026-10-24 拍板）
	{"union_api_root", false, "union", false},
	{"union_member_key", false, "union", true},
	{"union_enable_update", true, "union", false},
	{"union_enable_oauth2", true, "union", false},
	{"union_queryemail_mode", true, "union", false},
	{"union_oauth2_sig_private_key", true, "union", true},
	{"union_oauth2_sig_public_key", true, "union", false},
}

func (b *Backend) settingValue(ctx context.Context, key, def string) string {
	if v, ok, err := b.Store.GetOption(ctx, key); err == nil && ok {
		return v
	}
	return def
}

// GET /api/admin/v1/settings — whitelist values (option wins over env default).
func (b *Backend) handleSettingsGet(w http.ResponseWriter, r *http.Request) {
	out := make([]settingMeta, 0, len(settingsWhitelist))
	for _, s := range settingsWhitelist {
		out = append(out, b.metaFor(r.Context(), s))
	}
	writeJSON(w, http.StatusOK, map[string]any{"settings": out})
}

// PUT /api/admin/v1/settings {key, value} — whitelist-guarded option write.
func (b *Backend) handleSettingsPut(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Key   string `json:"key"`
		Value string `json:"value"`
		Clear bool   `json:"clear"`
	}
	if err := jsonDecode(r, &req); err != nil {
		writeErrCode(w, http.StatusBadRequest, "invalid JSON", i18n.MsgBadBody)
		return
	}
	req.Key = strings.TrimSpace(req.Key)
	var entry *settingSpec
	for i := range settingsWhitelist {
		if settingsWhitelist[i].key == req.Key {
			entry = &settingsWhitelist[i]
			break
		}
	}
	if entry == nil {
		writeErrCode(w, http.StatusBadRequest, "unknown setting", i18n.MsgSettingUnknown)
		return
	}
	val := strings.TrimSpace(req.Value)
	if keepsSecret(*entry, val, req.Clear) {
		// An empty secret means "leave it alone" — the form has no value to resend
		// because GET never returns one, and writing what the field shows would
		// store the mask as the credential. Clearing is its own explicit action.
		b.writeSettingState(r.Context(), w, *entry)
		return
	}
	switch req.Key {
	case "invite_required", "require_verification", "union_enabled", "union_enable_update", "union_enable_oauth2", "ygg_mojang_fallback", "cdn_enabled":
		normalized, ok := normalizeBool(val)
		if !ok {
			writeErrCode(w, http.StatusBadRequest, "boolean must be 1/0/true/false/on/off/yes/no", i18n.MsgInvalidValue)
			return
		}
		val = normalized
	case "skin_domains":
		// accept newline/comma separated, store canonical comma CSV
		val = strings.Join(strings.FieldsFunc(val, func(r rune) bool {
			return r == ',' || r == '\n' || r == ' '
		}), ",")
	case "cdn_headers":
		parts := strings.FieldsFunc(val, func(r rune) bool { return r == ',' || r == '\n' || r == ' ' })
		clean := make([]string, 0, len(parts))
		for _, p := range parts {
			p = strings.TrimSpace(p)
			if p == "" {
				continue
			}
			if len(p) > 64 || !regexp.MustCompile(`^[A-Za-z0-9-]+$`).MatchString(p) {
				writeErrCode(w, http.StatusBadRequest, "CDN header names allow letters, digits and hyphens only", i18n.MsgInvalidValue)
				return
			}
			switch strings.ToLower(p) {
			case "authorization", "cookie", "host", "content-length", "content-type":
				writeErrCode(w, http.StatusBadRequest, "that header may not be used as the client IP source", i18n.MsgInvalidValue)
				return
			}
			clean = append(clean, http.CanonicalHeaderKey(p))
		}
		if len(clean) == 0 {
			writeErrCode(w, http.StatusBadRequest, "keep at least one CDN header", i18n.MsgInvalidValue)
			return
		}
		val = strings.Join(clean, ",")
	case "rate_limit_auth", "login_fail_limit", "regs_per_ip", "player_name_length_min", "player_name_length_max",
		"ygg_token_expire_1", "ygg_token_expire_2", "ygg_tokens_limit", "ygg_rate_limit", "email_verify_grace_days",
		"access_detail_retention_days":
		// numeric knobs (review P2: garbage silently desynced panel vs boot
		// fallback, and negatives silently disabled the limiter layers)
		n, err := strconv.Atoi(val)
		if err != nil || n < 0 || n > 1000000 {
			writeErrCode(w, http.StatusBadRequest, "value must be an integer between 0 and 1000000", i18n.MsgInvalidValue)
			return
		}
		val = strconv.Itoa(n)
	case "ygg_search_profile_max":
		n, err := strconv.Atoi(val)
		if err != nil || n < 1 || n > 100 {
			writeErrCode(w, http.StatusBadRequest, "batch lookup limit must be an integer between 1 and 100", i18n.MsgInvalidValue)
			return
		}
		val = strconv.Itoa(n)
	case "max_upload_file_size":
		n, err := strconv.Atoi(val)
		if err != nil || n < 1 || n > 65536 {
			writeErrCode(w, http.StatusBadRequest, "upload cap must be an integer between 1 and 65536 (KB)", i18n.MsgInvalidValue)
			return
		}
		val = strconv.Itoa(n)
	case "player_name_rule":
		switch val {
		case "official", "cjk", "utf8", "custom":
		default:
			writeErrCode(w, http.StatusBadRequest, "player name rule must be official/cjk/utf8/custom", i18n.MsgInvalidValue)
			return
		}
	case "union_api_root":
		// same shape gate as union.Config.Validate: absolute http(s) URL
		if val != "" {
			if !strings.HasPrefix(val, "http://") && !strings.HasPrefix(val, "https://") {
				writeErrCode(w, http.StatusBadRequest, "union_api_root must be an absolute http(s) URL", i18n.MsgInvalidValue)
				return
			}
		}
	case "union_queryemail_mode":
		switch val {
		case "full", "off":
		default:
			writeErrCode(w, http.StatusBadRequest, "email lookup mode must be full or off", i18n.MsgInvalidValue)
			return
		}
	case "union_oauth2_sig_private_key", "union_oauth2_sig_public_key":
		// PEM sanity only (full parse happens at boot; restart-marked key)
		if val != "" && (!strings.Contains(val, "-----BEGIN ") || !strings.Contains(val, "-----END ")) {
			writeErrCode(w, http.StatusBadRequest, "a PEM key is required (leave empty to generate one)", i18n.MsgInvalidValue)
			return
		}
	case "custom_player_name_regexp", "texture_name_regexp":
		// an unparsable regexp would silently fail-closed at every new write
		if val != "" {
			if _, err := regexp.Compile(val); err != nil {
				writeLogged(r, w, http.StatusBadRequest, i18n.MsgInvalidValue, "the regular expression is not valid", err)
				return
			}
		}
	case "site_url":
		// 拍板（dev-plan §9）: 仅收 http(s) 绝对地址。留空=回退 env 首启默认，
		// 所以放行空串。boot 侧 isHTTPBaseURL 再兜一次（防直写 DB）。
		if val != "" && !strings.HasPrefix(val, "http://") && !strings.HasPrefix(val, "https://") {
			writeErrCode(w, http.StatusBadRequest, "site_url must be an absolute http(s) URL", i18n.MsgInvalidValue)
			return
		}
		val = strings.TrimRight(val, "/")
	case "announcement":
		// Markdown source（拍板双层消毒：服务端 goldmark+bluemonday，前端
		// marked+DOMPurify 再消毒——渲染实现见 internal/announce）；长度
		// 上限保持 options TEXT 列诚实。
		if len(val) > 4096 {
			writeErrCode(w, http.StatusBadRequest, "announcement is limited to 4096 characters", i18n.MsgTooLong)
			return
		}
	}
	if err := b.Store.SetOption(r.Context(), req.Key, val); err != nil {
		writeErrCode(w, http.StatusInternalServerError, "save failed", i18n.MsgOptionSaveFailed)
		return
	}
	if b.OnSettingChanged != nil {
		b.OnSettingChanged(req.Key, val) // hot-apply (union keys)
	}
	// Credential discipline: never persist secret values to the audit log —
	// record shape only. (union_member_key is a shared hub credential and the
	// sig private key signs user-info; their VALUES stay in the options row.)
	auditVal := val
	if entry.secret {
		auditVal = "<redacted>"
	}
	b.Store.InsertAudit(r.Context(), actorUID(r), "settings_update", map[string]any{
		"key": req.Key, "value": auditVal,
	})
	b.writeSettingState(r.Context(), w, *entry)
}

// metaFor renders one setting, masking a secret's value and default alike: the
// plaintext lives in the options row and nowhere a client can archive it.
func (b *Backend) metaFor(ctx context.Context, s settingSpec) settingMeta {
	def := b.SettingsDefaults[s.key]
	return projectSpec(s, b.settingValue(ctx, s.key, def), def)
}

// keepsSecret is the write-side half of the masking rule: a blank secret field
// is not a request to erase, it is a form that has nothing to send.
func keepsSecret(s settingSpec, val string, clear bool) bool {
	return s.secret && val == "" && !clear
}

// projectSpec is the masking rule, kept apart from the store read so it can be
// tested without a database: a secret never carries its value or its env
// fallback into the response, only whether one exists.
func projectSpec(s settingSpec, val, def string) settingMeta {
	m := settingMeta{
		Key: s.key, Value: val, Default: def, Restart: s.restart,
		DescKey: "settings." + s.key + ".desc", Group: s.group, Secret: s.secret, Set: val != "",
	}
	if s.secret {
		m.Value, m.Default = "", ""
	}
	return m
}

// writeSettingState answers a settings write with the same masked shape the GET
// produces — echoing the value back would put a credential in the response twice.
func (b *Backend) writeSettingState(ctx context.Context, w http.ResponseWriter, entry settingSpec) {
	m := b.metaFor(ctx, entry)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "key": m.Key, "value": m.Value, "setting": m})
}

// normalizeBool maps the accepted boolean spellings to "1"/"0"; ok=false on
// unknown input (review P2: typos silently became "0", disabling gates).
func normalizeBool(v string) (string, bool) {
	switch strings.ToLower(v) {
	case "1", "true", "on", "yes":
		return "1", true
	case "0", "false", "off", "no":
		return "0", true
	default:
		return "0", false
	}
}
