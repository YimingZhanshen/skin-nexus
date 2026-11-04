// Package ugctext cleans player-written text that a renderer cannot fix later.
package ugctext

import (
	"strings"
	"unicode"
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
