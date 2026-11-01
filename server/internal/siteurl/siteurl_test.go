package siteurl

import "testing"

func TestBuildersFollowOneBase(t *testing.T) {
	Set("https://skin.example.test/")
	if Get() != "https://skin.example.test" {
		t.Errorf("Get() = %q, want the trailing slash trimmed", Get())
	}
	cases := []struct {
		name string
		got  string
		want string
	}{
		{"texture", Texture("deadbeef"), "https://skin.example.test/textures/deadbeef"},
		{"preview", Preview(12), "https://skin.example.test/preview/12"},
		{"avatar", Avatar("deadbeef"), "https://skin.example.test/avatar/hash/deadbeef"},
		{"page", Page("skinlib", "7"), "https://skin.example.test/skinlib/7"},
	}
	for _, tc := range cases {
		if tc.got != tc.want {
			t.Errorf("%s = %q, want %q", tc.name, tc.got, tc.want)
		}
	}
}

// The empty-value rules matter because these builders sit on the response path:
// a player with no skin must get no URL, not a URL to /textures/<nothing>.
func TestEmptyInputsBuildNoURL(t *testing.T) {
	Set("https://skin.example.test")
	if Texture("") != "" {
		t.Error(`Texture("") must be empty`)
	}
	if Avatar("") != "" {
		t.Error(`Avatar("") must be empty`)
	}
	for _, tid := range []int64{0, -1} {
		if Preview(tid) != "" {
			t.Errorf("Preview(%d) must be empty for a non-positive id", tid)
		}
	}
}

// TestSetAfterReadersExist is the boot ordering this package exists for: a
// component built while the base was still env-derived must see the overridden
// value once boot resolves it, because it asks at build time rather than keeping
// a copy.
func TestSetAfterReadersExist(t *testing.T) {
	Set("http://127.0.0.1:8080")
	early := Texture("abc")
	Set("https://skin.public.test")
	if late := Texture("abc"); late == early {
		t.Fatalf("URL did not follow the base: %q", late)
	} else if late != "https://skin.public.test/textures/abc" {
		t.Errorf("URL after the override = %q", late)
	}
}

func TestPageCollapsesStraySlashes(t *testing.T) {
	Set("https://x.test")
	if got := Page("skinlib/", "/7"); got != "https://x.test/skinlib/7" {
		t.Errorf("Page() = %q, want stray slashes collapsed", got)
	}
}
