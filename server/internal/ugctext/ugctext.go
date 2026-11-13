// Package ugctext cleans player-written text that a renderer cannot fix later.
package ugctext

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

// Visible removes Unicode format characters (Cf) from user text: the BiDi
// overrides that make a name read backwards, the zero-width spaces that let two
// different names look identical, and the isolates used to hide what follows.
//
// ZWJ and ZWNJ survive. They are Cf too, but they are how emoji sequences and
// several scripts are spelled — deleting them would mangle legitimate copy to
// guard against something the renderer can handle.
func Visible(s string) string {
	if strings.IndexFunc(s, func(r rune) bool { return unicode.Is(unicode.Cf, r) }) < 0 {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		if !unicode.Is(unicode.Cf, r) {
			b.WriteRune(r)
			continue
		}
		switch r {
		case '‌', '‍':
			b.WriteRune(r)
		}
	}
	return b.String()
}

// AliasReason is why a closet alias was rejected; AliasOK means it passed.
type AliasReason int

const (
	AliasOK AliasReason = iota
	AliasTooLong
	AliasUnreadable
)

// Alias is the single closet-alias rule, shared by the player closet and the
// admin closet surface so the two can never accept different names: the text
// must arrive as valid UTF-8 (RuneError in the raw input means it did not),
// stay readable after Visible(), and fit 50 runes.
func Alias(raw string) (string, AliasReason) {
	if strings.ContainsRune(raw, utf8.RuneError) {
		return "", AliasUnreadable
	}
	name := Visible(strings.TrimSpace(raw))
	if len([]rune(name)) > 50 {
		return "", AliasTooLong
	}
	return name, AliasOK
}
