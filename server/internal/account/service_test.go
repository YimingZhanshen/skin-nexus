package account

import "testing"

func TestPlayerViewURLs(t *testing.T) {
	s := &Service{BaseURL: "https://skin.test/"}
	v := s.PlayerViewOf(Player{
		ID: 7, Name: "Steve", UUID: "abc",
		SkinTID: 12, SkinHash: "deadbeef", SkinModel: "slim",
		CapeTID: 13, CapeHash: "cafebabe",
	})
	if v.Skin == nil || v.Skin.URL != "https://skin.test/textures/deadbeef" ||
		v.Skin.PreviewURL != "https://skin.test/preview/12" ||
		v.Skin.AvatarURL != "https://skin.test/avatar/hash/deadbeef" {
		t.Fatalf("skin view = %+v", v.Skin)
	}
	if v.Cape == nil || v.Cape.URL != "https://skin.test/textures/cafebabe" || v.Cape.PreviewURL != "" {
		t.Fatalf("cape view = %+v", v.Cape)
	}
	if v := s.PlayerViewOf(Player{ID: 1, Name: "bare"}); v.Skin != nil || v.Cape != nil {
		t.Fatalf("empty slots = %+v", v)
	}
}

func TestErrorMappingCodes(t *testing.T) {
	e := errf(CodeNameTaken, "占用", nil)
	if e.Code != "player.name_taken" || e.Message != "占用" {
		t.Fatalf("err = %+v", e)
	}
}

func TestValidSourceURL(t *testing.T) {
	for raw, want := range map[string]bool{
		"https://example.com/a":      true,
		"http://example.com":         true,
		"ftp://example.com/x":        false,
		"javascript:alert(1)":        false,
		"https://user@example.com/x": false,
		"https://":                   false,
	} {
		if got := validSourceURL(raw); got != want {
			t.Errorf("validSourceURL(%q) = %v, want %v", raw, got, want)
		}
	}
}
