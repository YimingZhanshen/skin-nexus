package webapi

// siteurl_test.go pins the single-source rule across the layers that hand a URL
// to a client. Each of these takes a different route — the store scans a row,
// the account service builds a player view, this package answers a skinlib
// list — and they must not disagree about what one hash looks like as a URL:
// the Yggdrasil signature covers the signed string, and a launcher that retries
// on a URL it cannot fetch has no way to tell "different host" from "broken
// deployment".

import (
	"testing"

	"skin-nexus/server/internal/account"
	"skin-nexus/server/internal/siteurl"
	"skin-nexus/server/internal/store"
)

func TestTextureURLHasOneSource(t *testing.T) {
	const base = "https://skin.example.test"
	siteurl.Set(base) // boot does this once, after the option override
	const hash = "0123456789abcdef0123456789abcdef"
	want := base + "/textures/" + hash

	// TextureURL reads no database, so a zero-value store is enough.
	if got := (&store.Mysql{}).TextureURL(hash); got != want {
		t.Errorf("store.TextureURL = %q, want %q", got, want)
	}
	view := (&account.Service{}).PlayerViewOf(account.Player{
		ID: 1, Name: "steve", SkinTID: 7, SkinHash: hash, SkinModel: "default",
	})
	if view.Skin == nil || view.Skin.URL != want {
		t.Errorf("account player view skin URL = %+v, want %q", view.Skin, want)
	}
	if got := siteurl.Texture(hash); got != want {
		t.Errorf("siteurl.Texture = %q, want %q", got, want)
	}

	// A late Set must be visible to all three: this is the bug the package
	// removes — a component that captured the pre-override base kept serving it.
	siteurl.Set("https://after-the-option.test")
	if got := (&store.Mysql{}).TextureURL(hash); got != "https://after-the-option.test/textures/"+hash {
		t.Errorf("store kept a stale base: %q", got)
	}
}
