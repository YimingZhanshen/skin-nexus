package webapi

// players_payload_test.go pins what /api/v1/me/players hands the page. The
// launcher protocol keeps the raw texture hashes out of JSON, so the web
// projection re-declares them at depth 0: if that shadowing ever stops working,
// every closet and skin-library dialog loses the ability to say which texture a
// character is already wearing, and would fall back to matching a URL that a
// plugin filter is free to rewrite.

import (
	"encoding/json"
	"testing"

	"skin-nexus/server/internal/ygg"
)

func TestPlayerViewCarriesTheTextureIdentity(t *testing.T) {
	blob, err := json.Marshal(playerView{
		Player: ygg.Player{
			ID: 1, UID: 2, Name: "Steve", UUID: "abc",
			SkinURL: "https://cdn.example/skin.png", SkinModel: "slim",
			// the embedded one is json:"-" on purpose; only the outer fields ship
			SkinHash: "must-not-appear",
		},
		SkinTID:  7,
		CapeTID:  8,
		SkinHash: "deadbeef",
		CapeHash: "cafebabe",
	})
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(blob, &got); err != nil {
		t.Fatal(err)
	}
	for key, want := range map[string]any{
		"ID": float64(1), "UID": float64(2), "Name": "Steve", "UUID": "abc",
		"SkinURL": "https://cdn.example/skin.png", "SkinModel": "slim",
		"SkinTID": float64(7), "CapeTID": float64(8),
		"SkinHash": "deadbeef", "CapeHash": "cafebabe",
	} {
		if v, ok := got[key]; !ok || v != want {
			t.Errorf("%s = %#v (present=%v), want %#v — the payload reached by the SPA changed", key, v, ok, want)
		}
	}
}
