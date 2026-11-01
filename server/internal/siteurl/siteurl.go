// Package siteurl holds the site's resolved base address and builds every
// absolute URL the station hands out.
//
// Two problems, one small package:
//
//  1. The base is not known at config-load time. The options panel can override
//     env (`site_url` wins over SKIN_BASE_URL), and that override is read from
//     the database — so any component that captured the env value early, before
//     the override was applied, would keep serving stale links forever. Nothing
//     here captures anything: callers ask at build time.
//  2. An absolute texture URL is signed. Yggdrasil's `textures` property is
//     base64 plus an RSA signature over that exact bytes, so the address that
//     gets signed must be the address the client will fetch. One builder for the
//     path removes the second shape where they could disagree.
//
// The base is set once during boot, after configuration is fully resolved, and
// read through an atomic pointer: a plain string variable would be a data race
// between the boot goroutine and every request goroutine reading it.
package siteurl

import (
	"path"
	"strconv"
	"strings"
	"sync/atomic"
)

var base atomic.Pointer[string]

// Set installs the resolved base address, with or without a trailing slash.
// Call it once, after configuration is final.
func Set(u string) {
	v := strings.TrimRight(u, "/")
	base.Store(&v)
}

// Get is the resolved base, or "" before Set (which only happens in a process
// that is not serving traffic).
func Get() string {
	if p := base.Load(); p != nil {
		return *p
	}
	return ""
}

// Texture is the content-addressed texture file. The path is the whole point:
// the filename is the SHA-256 of the bytes, so this URL is immutable and
// cacheable forever.
func Texture(hash string) string {
	if hash == "" {
		return ""
	}
	return Get() + "/textures/" + hash
}

// Avatar is the head crop rendered from a skin hash.
func Avatar(hash string) string {
	if hash == "" {
		return ""
	}
	return Get() + "/avatar/hash/" + hash
}

// Preview is the rendered skin for a texture row. The version query is the
// caller's business (it changes when the renderer changes), so it is appended
// by callers that know it, against the documented format.
func Preview(tid int64) string {
	if tid <= 0 {
		return ""
	}
	return Get() + "/preview/" + strconv.FormatInt(tid, 10)
}

// AvatarUser is the live head crop of a user's current skin, keyed by uid.
func AvatarUser(uid int64) string {
	if uid <= 0 {
		return ""
	}
	return Get() + "/avatar/user/" + strconv.FormatInt(uid, 10)
}

// Page joins base path segments into one absolute link, e.g.
// Page("skinlib", "7") — path.Join keeps a leading slash and collapses repeats,
// so a caller cannot build `/foo//bar` by accident.
func Page(elem ...string) string {
	return Get() + path.Join(append([]string{"/"}, elem...)...)
}
