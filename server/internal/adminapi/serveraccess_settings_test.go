package adminapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

func TestServerAccessSettingIsHotBoolean(t *testing.T) {
	var spec *settingSpec
	for i := range settingsWhitelist {
		if settingsWhitelist[i].key == "serveraccess_enabled" {
			spec = &settingsWhitelist[i]
			break
		}
	}
	if spec == nil {
		t.Fatal("serveraccess_enabled is not whitelisted")
	}
	if spec.restart || spec.secret || spec.group != "union" {
		t.Fatalf("serveraccess_enabled metadata = %+v, want hot non-secret optional-module setting", spec)
	}

	b := newDiagBackend(t)
	var changed []string
	b.OnSettingChanged = func(key, value string) { changed = append(changed, key+"="+value) }
	for _, tc := range []struct{ input, want string }{
		{"true", "1"},
		{"off", "0"},
	} {
		rec := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodPut, "/api/admin/v1/settings", strings.NewReader(`{"key":"serveraccess_enabled","value":"`+tc.input+`"}`))
		b.handleSettingsPut(rec, r)
		if rec.Code != http.StatusOK {
			t.Fatalf("PUT %s = %d %s", tc.input, rec.Code, rec.Body.String())
		}
		got, ok, err := b.Store.GetOption(context.Background(), "serveraccess_enabled")
		if err != nil || !ok || got != tc.want {
			t.Fatalf("stored %s = %q/%v/%v, want %q", tc.input, got, ok, err, tc.want)
		}
	}
	if strings.Join(changed, ",") != "serveraccess_enabled=1,serveraccess_enabled=0" {
		t.Fatalf("hot-toggle callbacks = %v", changed)
	}

	rec := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPut, "/api/admin/v1/settings", strings.NewReader(`{"key":"serveraccess_enabled","value":"maybe"}`))
	b.handleSettingsPut(rec, r)
	if rec.Code != http.StatusBadRequest || len(changed) != 2 {
		t.Fatalf("invalid boolean = %d, callbacks %v; want 400 and no hot apply", rec.Code, changed)
	}
}

func TestServerAccessSettingUsesToggleWidget(t *testing.T) {
	raw, err := os.ReadFile(settingsViewPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "'serveraccess_enabled'") {
		t.Fatal("settings UI does not route serveraccess_enabled to a boolean toggle")
	}
	for _, key := range vueSet(t, string(raw), "boolKeys") {
		if key == "serveraccess_enabled" {
			return
		}
	}
	t.Fatal("serveraccess_enabled is absent from the settings boolean widget set")
}
