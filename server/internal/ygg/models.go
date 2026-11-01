// Package ygg: Yggdrasil API DTOs and the textures-property assembler.
//
// Serialization rules are part of the compatibility contract
// (docs/phase1-dev-plan.md §1, analysis/blessing-skin-plugins.md §2):
//   - empty `textures` must serialize as {} — never null or [] ;
//   - timestamps are milliseconds;
//   - UUIDs are 32-char lowercase hex without dashes.
package ygg

import (
	"encoding/base64"
	"encoding/json"
	"time"
)

// Property is one profile property (textures, uploadableTextures).
type Property struct {
	Name      string `json:"name"`
	Value     string `json:"value"`
	Signature string `json:"signature,omitempty"`
}

// Profile is the public profile shape (hasJoined / profile/{uuid}).
type Profile struct {
	ID         string     `json:"id"`
	Name       string     `json:"name"`
	Properties []Property `json:"properties"`
}

// texturesJSON is the decoded textures property value. The custom marshal
// guarantees `textures:{}` for the empty case (Go nil/empty map must not
// collapse to null — clients treat null as a malformed textures property).
type texturesJSON struct {
	Timestamp   int64                  `json:"timestamp"`
	ProfileID   string                 `json:"profileId"`
	ProfileName string                 `json:"profileName"`
	IsPublic    bool                   `json:"isPublic"`
	Textures    map[string]interface{} `json:"textures"`
}

func (t texturesJSON) MarshalJSON() ([]byte, error) {
	type alias texturesJSON
	a := alias(t)
	if a.Textures == nil {
		a.Textures = map[string]interface{}{}
	}
	return json.Marshal(a)
}

// SkinTexture is one SKIN entry.
type SkinTexture struct {
	URL      string            `json:"url"`
	Metadata map[string]string `json:"metadata,omitempty"`
}

// CapeTexture is one CAPE entry.
type CapeTexture struct {
	URL string `json:"url"`
}

// TextureSet describes a player's resolved textures.
type TextureSet struct {
	SkinURL   string // empty = no skin
	SkinModel string // "default" | "slim"
	CapeURL   string // empty = no cape
}

// AssembleTextures builds the Base64 textures property value and its
// signature. `withSig` is true for hasJoined and unsigned=false requests.
// uploadableTextures ("skin,cape") mirrors yggdrasil-connect behavior.
func AssembleTextures(profileID, profileName string, ts TextureSet, now time.Time, sign bool, s Signer) ([]Property, error) {
	tex := texturesJSON{
		Timestamp:   now.UnixMilli(),
		ProfileID:   profileID,
		ProfileName: profileName,
		IsPublic:    true,
		Textures:    map[string]interface{}{},
	}
	if ts.SkinURL != "" {
		entry := SkinTexture{URL: ts.SkinURL}
		if ts.SkinModel != "" && ts.SkinModel != "default" {
			entry.Metadata = map[string]string{"model": ts.SkinModel}
		}
		tex.Textures["SKIN"] = entry
	}
	if ts.CapeURL != "" {
		tex.Textures["CAPE"] = CapeTexture{URL: ts.CapeURL}
	}
	raw, err := json.Marshal(tex)
	if err != nil {
		return nil, err
	}
	value := base64.StdEncoding.EncodeToString(raw)

	props := []Property{{Name: "textures", Value: value}}
	if sign {
		sig, err := s.SignValue(value)
		if err != nil {
			return nil, err
		}
		props[0].Signature = sig
	}
	props = append(props, Property{Name: "uploadableTextures", Value: "skin,cape"})
	return props, nil
}
