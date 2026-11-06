package ygg

// CHARACTERIZATION TESTS — ticket G10 / E3 (Yggdrasil protocol surface).
//
// These tests PIN TODAY'S BEHAVIOUR of the frozen /api/yggdrasil wire
// surface. They do not assert what the surface SHOULD be. If a future round
// intentionally changes any of it, this file is expected to go red and must
// be updated DELIBERATELY together with docs/compat-matrix.md — never by
// relaxing an assertion just to get a green suite.
//
// Evidence pairs (analysis/g10-e3-protocol-surface.md):
//   - refs/AsterYggdrasil src/services/yggdrasil_service/properties.rs:65-83
//     (uploadableTextures emitted ONLY when non-empty) and
//     src/config/yggdrasil.rs:37 + session.rs:636-641 (skinDomains).
//   - refs/authlib-injector wiki/src/en/
//     yggdrasil-server-technical-specification.md:138-175 (property table,
//     uploadableTextures absent == nothing uploadable) and :576-587
//     (client-side domain matching rules).
//   - refs/yggdrasil-connect src/Models/Profile.php:73-100 (SKIN before CAPE,
//     every property signed).

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

const g10FixedMS = int64(1700000000000)

func g10Signer(t *testing.T) Signer {
	t.Helper()
	s, err := NewLocalSigner(t.TempDir() + "/g10-key.pem")
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func g10Decode(t *testing.T, p Property) string {
	t.Helper()
	raw, err := base64.StdEncoding.DecodeString(p.Value)
	if err != nil {
		t.Fatalf("property value is not base64: %v", err)
	}
	return string(raw)
}

// TestG10UploadableTexturesAlwaysPresentAndUnsigned pins three observed facts:
//  1. the property is ALWAYS emitted (models.go:103), including for a profile
//     with no textures at all — so our wire form of "absent" never occurs and
//     we never emit the empty string either (the two forms Aster keeps apart
//     at properties.rs:65);
//  2. its value is the constant "skin,cape", independent of any site option;
//  3. it is NEVER signed, even when the textures property is signed
//     (models.go:96-101 signs props[0] only). yggdrasil-connect Profile.php:92
//     and Aster properties.rs:66-71 both sign it — FINDING, not a fix.
func TestG10UploadableTexturesAlwaysPresentAndUnsigned(t *testing.T) {
	signer := g10Signer(t)

	cases := []struct {
		name string
		ts   TextureSet
	}{
		{"no textures", TextureSet{}},
		{"skin only", TextureSet{SkinURL: "https://skin.invalid/textures/" + strings.Repeat("aa", 32)}},
		{"skin and cape", TextureSet{
			SkinURL: "https://skin.invalid/textures/" + strings.Repeat("aa", 32),
			CapeURL: "https://skin.invalid/textures/" + strings.Repeat("cc", 32),
		}},
	}
	for _, c := range cases {
		for _, sign := range []bool{false, true} {
			props, err := AssembleTextures(testUUID, "Steve", c.ts, time.UnixMilli(g10FixedMS), sign, signer)
			if err != nil {
				t.Fatalf("%s: %v", c.name, err)
			}
			if len(props) != 2 {
				t.Fatalf("%s: want exactly 2 properties, got %d (%+v)", c.name, len(props), props)
			}
			if props[0].Name != "textures" || props[1].Name != "uploadableTextures" {
				t.Fatalf("%s: property order/names changed: %q then %q", c.name, props[0].Name, props[1].Name)
			}
			// (1)+(2): always present, never the empty string, always "skin,cape".
			if props[1].Value != "skin,cape" {
				t.Fatalf("%s: uploadableTextures value = %q, want the constant %q", c.name, props[1].Value, "skin,cape")
			}
			if props[0].Signature == "" != !sign {
				t.Fatalf("%s: textures signature presence does not track sign=%v", c.name, sign)
			}
			// (3): the uploadableTextures property carries no signature even
			// when signed=true — the divergence from Profile.php:92-96.
			if props[1].Signature != "" {
				t.Fatalf("%s: uploadableTextures is UNSIGNED today; found signature %q", c.name, props[1].Signature)
			}
		}
	}
}

// TestG10TexturesPropertyExactJSON pins the byte-exact textures property value
// for a slim skin plus a cape. Two things are being recorded here:
//   - the inner `textures` object is a Go map, and encoding/json sorts map
//     keys, so CAPE is emitted BEFORE SKIN. That is deterministic (verified
//     empirically, see the analysis note), but it is the opposite insertion
//     order of yggdrasil-connect Profile.php:57-71 (SKIN first) and happens to
//     match Aster's BTreeMap (properties.rs:114).
//   - `metadata` with a single `model` key appears only for a non-"default"
//     model (models.go:81-83) and only on SKIN — CAPE has no metadata field at
//     all (models.go:57-59).
func TestG10TexturesPropertyExactJSON(t *testing.T) {
	const (
		skinURL = "https://skin.invalid/textures/" + "ab"
		capeURL = "https://skin.invalid/textures/" + "cd"
	)
	slim, err := AssembleTextures(testUUID, "Steve", TextureSet{
		SkinURL: skinURL, SkinModel: "slim", CapeURL: capeURL,
	}, time.UnixMilli(g10FixedMS), false, nil)
	if err != nil {
		t.Fatal(err)
	}
	wantSlim := `{"timestamp":1700000000000,"profileId":"` + testUUID +
		`","profileName":"Steve","isPublic":true,"textures":{` +
		`"CAPE":{"url":"` + capeURL + `"},` +
		`"SKIN":{"url":"` + skinURL + `","metadata":{"model":"slim"}}}}`
	if got := g10Decode(t, slim[0]); got != wantSlim {
		t.Fatalf("slim textures JSON drifted\n got: %s\nwant: %s", got, wantSlim)
	}

	def, err := AssembleTextures(testUUID, "Steve", TextureSet{
		SkinURL: skinURL, SkinModel: "default", CapeURL: capeURL,
	}, time.UnixMilli(g10FixedMS), false, nil)
	if err != nil {
		t.Fatal(err)
	}
	wantDefault := `{"timestamp":1700000000000,"profileId":"` + testUUID +
		`","profileName":"Steve","isPublic":true,"textures":{` +
		`"CAPE":{"url":"` + capeURL + `"},` +
		`"SKIN":{"url":"` + skinURL + `"}}}`
	if got := g10Decode(t, def[0]); got != wantDefault {
		t.Fatalf(`"default" model must carry NO metadata key\n got: %s\nwant: %s`, got, wantDefault)
	}

	// An empty model string behaves exactly like "default" (models.go:81).
	empty, err := AssembleTextures(testUUID, "Steve", TextureSet{SkinURL: skinURL},
		time.UnixMilli(g10FixedMS), false, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := g10Decode(t, empty[0]); strings.Contains(got, "metadata") {
		t.Fatalf("empty SkinModel must not emit metadata: %s", got)
	}

	// Empty CapeURL is a missing CAPE key, not a CAPE with an empty url.
	gotEmpty := g10Decode(t, empty[0])
	if strings.Contains(gotEmpty, `"CAPE"`) {
		t.Fatalf("empty CapeURL must not emit a CAPE key: %s", gotEmpty)
	}
	if !strings.Contains(gotEmpty, `"textures":{"SKIN"`) {
		t.Fatalf("expected a SKIN-only textures object, got %s", gotEmpty)
	}
}

// TestG10TexturesPropertyExtraFields pins the two extra/missing top-level keys
// that the reference servers disagree about, so the next round does not have
// to re-derive them:
//   - we DO emit `isPublic: true` (models.go:37) — yggdrasil-connect Profile.php:39
//     emits it too, Aster's TexturesProperty (properties.rs:107-115) does not.
//   - we do NOT emit `signatureRequired: true` for signed requests, which
//     yggdrasil-connect adds at Profile.php:51.
func TestG10TexturesPropertyExtraFields(t *testing.T) {
	props, err := AssembleTextures(testUUID, "Steve", TextureSet{},
		time.UnixMilli(g10FixedMS), true, g10Signer(t))
	if err != nil {
		t.Fatal(err)
	}
	raw := g10Decode(t, props[0])
	if !strings.Contains(raw, `"isPublic":true`) {
		t.Fatalf("isPublic:true is emitted today and must stay: %s", raw)
	}
	if strings.Contains(raw, "signatureRequired") {
		t.Fatalf("signatureRequired is NOT emitted today: %s", raw)
	}
	var doc map[string]any
	if err := json.Unmarshal([]byte(raw), &doc); err != nil {
		t.Fatal(err)
	}
	if _, ok := doc["textures"]; !ok {
		t.Fatal("textures key missing")
	}
}

// TestG10SkinDomainsEmissionIsPassthrough pins what WE do with skinDomains:
// we only CONCATENATE and emit, we never match. http.go:171-193 has no
// host-comparison code at all, so the leading-dot / suffix semantics live
// entirely in the client (authlib-injector
// SkinWhitelistTransformUnit.java:65-69 and the wiki rules at
// yggdrasil-server-technical-specification.md:581-587). Consequences pinned:
//   - a leading-dot entry is forwarded byte-for-byte, un-normalised;
//   - dedup is by EXACT string, so "a.invalid" and ".a.invalid" both survive
//     and mean different things to the client;
//   - an empty entry is dropped (so empty != a wildcard rule);
//   - the site_url host and the request Host header are always appended, so
//     the list is never empty even with zero configuration;
//   - the appended request-host entry keeps its PORT, and authlib-injector
//     compares against URI.getHost(), which has no port — a port-bearing
//     default entry therefore matches nothing on the client. FINDING.
func TestG10SkinDomainsEmissionIsPassthrough(t *testing.T) {
	api, _ := newTestAPI(t)
	api.cfg.SkinDomains = []string{".skin.invalid", "skin.invalid", "", ".skin.invalid"}
	api.cfg.Homepage = "https://site.invalid"
	api.cfg.ExtraSkinDomains = nil

	rec := do(t, api.Handler(), "GET", "/api/yggdrasil/", "", nil)
	var doc struct {
		SkinDomains []string `json:"skinDomains"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &doc); err != nil {
		t.Fatal(err)
	}

	// Position 0 is the configured entry, verbatim, dot intact.
	if len(doc.SkinDomains) == 0 || doc.SkinDomains[0] != ".skin.invalid" {
		t.Fatalf("leading-dot entry must be forwarded verbatim, got %v", doc.SkinDomains)
	}
	// Exact-string dedup only: the bare and the dotted form are both present.
	var bare, dotted bool
	for _, d := range doc.SkinDomains {
		if d == "skin.invalid" {
			bare = true
		}
		if d == ".skin.invalid" {
			dotted = true
		}
	}
	if !bare || !dotted {
		t.Fatalf(`"skin.invalid" and ".skin.invalid" must BOTH survive dedup: %v`, doc.SkinDomains)
	}
	for _, d := range doc.SkinDomains {
		if d == "" {
			t.Fatalf("empty entry must be dropped (client rule: empty matches nothing): %v", doc.SkinDomains)
		}
	}
	// Homepage host then request host, in that order, always appended.
	if doc.SkinDomains[len(doc.SkinDomains)-1] != "example.com" {
		t.Fatalf("last entry must be the request host, got %v", doc.SkinDomains)
	}
	var sawSite bool
	for _, d := range doc.SkinDomains {
		if d == "site.invalid" {
			sawSite = true
		}
	}
	if !sawSite {
		t.Fatalf("site_url host must be appended, got %v", doc.SkinDomains)
	}

	// Zero configuration still yields a non-empty array (request host only).
	api2, _ := newTestAPI(t)
	api2.cfg.SkinDomains = nil
	api2.cfg.Homepage = ""
	api2.cfg.ExtraSkinDomains = nil
	rec2 := do(t, api2.Handler(), "GET", "/api/yggdrasil/", "", nil)
	var doc2 struct {
		SkinDomains []string `json:"skinDomains"`
	}
	if err := json.Unmarshal(rec2.Body.Bytes(), &doc2); err != nil {
		t.Fatal(err)
	}
	if len(doc2.SkinDomains) != 1 || doc2.SkinDomains[0] != "example.com" {
		t.Fatalf("unconfigured default = %v, want exactly [request host]", doc2.SkinDomains)
	}
}

// TestG10SkinDomainsPortBareHost pins the port case on its own: an entry with
// a port is emitted as-is. authlib-injector extracts URI.getHost() (no port),
// so such an entry can never match. Recorded, not fixed.
func TestG10SkinDomainsPortBareHost(t *testing.T) {
	api, _ := newTestAPI(t)
	api.cfg.SkinDomains = []string{"skin.invalid:8443"}
	api.cfg.Homepage = ""
	api.cfg.ExtraSkinDomains = nil
	rec := do(t, api.Handler(), "GET", "/api/yggdrasil/", "", nil)
	var doc struct {
		SkinDomains []string `json:"skinDomains"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &doc); err != nil {
		t.Fatal(err)
	}
	if doc.SkinDomains[0] != "skin.invalid:8443" {
		t.Fatalf("port-bearing entry must pass through untouched, got %v", doc.SkinDomains)
	}
}

// TestG10NoServerSideDomainMatching pins the negative half of question 2:
// nothing in the ygg package filters or validates a texture URL host against
// skinDomains. The list we publish and the URL we sign are built
// independently (siteurl.Texture / models.go:84), so a texture host missing
// from the list would still be signed and served — the failure would be
// silent and client-side.
func TestG10NoServerSideDomainMatching(t *testing.T) {
	api, _ := newTestAPI(t)
	api.cfg.SkinDomains = nil
	api.svc.TextureURL = nil

	// A skin on a host that is in NO whitelist is still emitted verbatim.
	store := newMemStore()
	u := User{ID: 7, Nickname: "g10", EmailVerified: true, CanJoinServer: true}
	store.byID[7] = u
	store.users["g10"] = u
	p := Player{ID: 70, UID: 7, Name: "G10Ghost", UUID: "aaaa1111bbbb2222cccc3333dddd4444",
		SkinURL: "https://not-whitelisted.invalid/textures/" + strings.Repeat("ee", 32)}
	store.byUUID[p.UUID] = p
	store.players[p.Name] = p

	svc := NewService(store, g10Signer(t))
	api2 := NewAPI(svc, MetadataConfig{ServerName: "g10", Homepage: "https://site.invalid"})

	prof, err := svc.ProfileByUUID(t.Context(), p.UUID, false)
	if err != nil {
		t.Fatal(err)
	}
	raw := g10Decode(t, prof.Properties[0])
	if !strings.Contains(raw, "not-whitelisted.invalid") {
		t.Fatalf("skin URL host must be emitted untouched (no server-side whitelist check): %s", raw)
	}

	// And the metadata list contains no such filtering either.
	rec := do(t, api2.Handler(), "GET", "/api/yggdrasil/", "", nil)
	var doc struct {
		SkinDomains []string `json:"skinDomains"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &doc); err != nil {
		t.Fatal(err)
	}
	for _, d := range doc.SkinDomains {
		if d == "not-whitelisted.invalid" {
			t.Fatalf("texture host must NOT be auto-added to skinDomains today: %v", doc.SkinDomains)
		}
	}
}

// TestG10MetadataIsManualJSON pins the emission mechanism behind the two
// findings above: the metadata document is hand-built with strings.Builder
// (http.go:130-163), so its top-level key order is fixed by code, not by the
// map-sorting rule that governs the textures property.
func TestG10MetadataIsManualJSON(t *testing.T) {
	api, _ := newTestAPI(t)
	rec := do(t, api.Handler(), "GET", "/api/yggdrasil/", "", nil)
	body := rec.Body.String()
	if !strings.HasPrefix(body, `{"meta":{"serverName":`) {
		t.Fatalf("metadata top-level order is literal in http.go, got %s", body[:min(60, len(body))])
	}
	if strings.Index(body, `"skinDomains"`) > strings.Index(body, `"signaturePublickey"`) {
		t.Fatal("skinDomains must precede signaturePublickey")
	}
	// signaturePublickey is the LAST key, and the document is exactly three
	// top-level keys (http.go:131-153) — nothing is appended after it.
	if !strings.HasSuffix(body, "}\n") && !strings.HasSuffix(body, "}") {
		t.Fatalf("unexpected metadata trailer: %q", body[len(body)-20:])
	}
	var doc map[string]json.RawMessage
	if err := json.Unmarshal([]byte(body), &doc); err != nil {
		t.Fatal(err)
	}
	if len(doc) != 3 {
		t.Fatalf("metadata has exactly 3 top-level keys today, got %d: %v", len(doc), doc)
	}
}
