package adminapi

// settings_secret_test.go pins the read-side and write-side masking rule.
//
// The bug it prevents is the one these endpoints had: GET answered with the
// plaintext credential "for the operator's convenience", so the panel's password
// field, the browser cache, any XSS and any archived response all held a secret.
// Masking the read then creates the opposite bug — a form that submits what it
// displays would store the mask as the credential — so `keepsSecret` is the
// other half and is tested just as hard.

import (
	"strings"
	"testing"
)

func secretSpec() settingSpec {
	return settingSpec{key: "smtp_pass", group: "mail", desc: "SMTP 密码", secret: true}
}

func TestProjectSpecMasksSecrets(t *testing.T) {
	m := projectSpec(secretSpec(), "hunter2", "from-env")
	if m.Value != "" || m.Default != "" {
		t.Errorf("secret came back over the wire: value=%q default=%q", m.Value, m.Default)
	}
	if !m.Set {
		t.Error("set=false while a credential exists: the operator cannot tell configured from empty")
	}
	if !m.Secret {
		t.Error("secret=false: the panel would render a plaintext input")
	}

	empty := projectSpec(secretSpec(), "", "")
	if empty.Set {
		t.Error("set=true for an unset secret")
	}

	// A non-secret is unaffected — otherwise this change would have broken every
	// other field in the panel.
	plain := settingSpec{key: "site_name", group: "basic", desc: "站点名称"}
	got := projectSpec(plain, "SkinNexus", "default-name")
	if got.Value != "SkinNexus" || got.Default != "default-name" || got.Secret || !got.Set {
		t.Errorf("ordinary setting was masked: %+v", got)
	}
}

func TestKeepsSecret(t *testing.T) {
	spec := secretSpec()
	if !keepsSecret(spec, "", false) {
		t.Error("an empty secret write must be ignored")
	}
	if keepsSecret(spec, "", true) {
		t.Error("clear=true is an explicit erase and must be written")
	}
	if keepsSecret(spec, "new-token", false) {
		t.Error("a supplied secret must be written")
	}
	if keepsSecret(settingSpec{key: "site_name"}, "", false) {
		t.Error("an empty non-secret value is a legitimate erase (announcement, skin_domains)")
	}
}

// TestSecretKeysAreFlagged guards the flag column itself: a new credential key
// that someone forgets to mark would be served in plaintext with no test to
// notice. The list is short and every entry is named, so adding one is deliberate.
func TestSecretKeysAreFlagged(t *testing.T) {
	want := map[string]bool{
		"smtp_pass": true, "union_member_key": true, "union_oauth2_sig_private_key": true,
	}
	for _, s := range settingsWhitelist {
		if want[s.key] && !s.secret {
			t.Errorf("%s is a credential but is not flagged secret", s.key)
		}
		if s.secret && !want[s.key] {
			t.Errorf("%s is flagged secret without being in the known set — say why in this test", s.key)
		}
		if s.secret && !strings.Contains(s.desc, "不回显") {
			t.Errorf("%s: the panel copy must tell the operator the field is write-only", s.key)
		}
	}
}
