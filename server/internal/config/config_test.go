package config

import (
	"os"
	"path/filepath"
	"testing"

	"skin-nexus/server/internal/signedpkg"
)

func TestPluginStateDirAndUploadMax(t *testing.T) {
	t.Setenv("SKIN_DATABASE_DSN", "u:p@tcp(localhost:3306)/x")
	t.Setenv("SKIN_DATA_DIR", t.TempDir())
	t.Setenv("SKIN_PLUGIN_STATE_DIR", "")
	t.Setenv("SKIN_PLUGIN_UPLOAD_MAX", "")
	c, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if c.PluginStateDir != filepath.Join(c.PluginsDir, ".state") {
		t.Fatalf("state dir = %s", c.PluginStateDir)
	}
	if c.PluginUploadMax != 64<<20 {
		t.Fatalf("upload max = %d", c.PluginUploadMax)
	}

	custom := t.TempDir()
	t.Setenv("SKIN_PLUGIN_STATE_DIR", custom)
	t.Setenv("SKIN_PLUGIN_UPLOAD_MAX", "1024")
	c, err = Load()
	if err != nil {
		t.Fatal(err)
	}
	if c.PluginStateDir != custom || c.PluginUploadMax != 1024 {
		t.Fatalf("custom = %s/%d", c.PluginStateDir, c.PluginUploadMax)
	}
}

func TestMarketDefaultsAndDisable(t *testing.T) {
	t.Setenv("SKIN_DATABASE_DSN", "u:p@tcp(localhost:3306)/x")
	t.Setenv("SKIN_DATA_DIR", t.TempDir())

	unset := func(keys ...string) {
		for _, k := range keys {
			t.Setenv(k, "placeholder")
			_ = os.Unsetenv(k)
		}
	}

	unset("SKIN_PLUGIN_MARKET_URL", "SKIN_PLUGIN_TRUST_KEYS", "SKIN_THEME_MARKET_URL", "SKIN_THEME_TRUST_KEYS")
	c, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if c.PluginMarketURL != OfficialPluginMarketURL || c.PluginTrustKeys != OfficialPluginTrustKeys {
		t.Fatalf("plugin defaults = %q/%q", c.PluginMarketURL, c.PluginTrustKeys)
	}
	if c.ThemeMarketURL != OfficialThemeMarketURL || c.ThemeTrustKeys != OfficialThemeTrustKeys {
		t.Fatalf("theme defaults = %q/%q", c.ThemeMarketURL, c.ThemeTrustKeys)
	}
	// The compiled-in keyrings must actually parse and stay independent.
	for _, keys := range []string{c.PluginTrustKeys, c.ThemeTrustKeys} {
		if _, err := signedpkg.ParseKeys(keys); err != nil {
			t.Fatalf("official keyring %q: %v", keys, err)
		}
	}
	if c.PluginTrustKeys == c.ThemeTrustKeys {
		t.Fatal("plugin and theme markets must not share a keyring")
	}

	t.Setenv("SKIN_PLUGIN_MARKET_URL", "")
	t.Setenv("SKIN_PLUGIN_TRUST_KEYS", "")
	t.Setenv("SKIN_THEME_MARKET_URL", "")
	t.Setenv("SKIN_THEME_TRUST_KEYS", "")
	c, err = Load()
	if err != nil {
		t.Fatal(err)
	}
	if c.PluginMarketURL != "" || c.PluginTrustKeys != "" || c.ThemeMarketURL != "" || c.ThemeTrustKeys != "" {
		t.Fatalf("explicit empty should disable, got %+v", []string{c.PluginMarketURL, c.PluginTrustKeys, c.ThemeMarketURL, c.ThemeTrustKeys})
	}
}
