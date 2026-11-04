package dialect

import "strings"

// Search text must never carry LIKE's wildcards. A player who types "%" gets the
// whole table — on the skin library that is an unbounded scan behind an anonymous
// request — and "_" silently matches any single character, so "a_b" also finds
// "axb". Every LIKE in the store binds through the helpers below, and each
// predicate names the escape character the same way.

// likeEscape is the escape character used by every LIKE … ESCAPE clause. It is
// chosen so the SQL text is identical on all four engines: backslash needs
// doubling inside a MySQL string literal, "!" does not.
const likeEscape = "!"

// LikeArg escapes the wildcards (and the escape character itself) in user text.
func LikeArg(s string) string {
	if !strings.ContainsAny(s, likeEscape+"%_") {
		return s
	}
	var b strings.Builder
	b.Grow(len(s) + 8)
	for i := 0; i < len(s); i++ {
		switch c := s[i]; c {
		case likeEscape[0], '%', '_':
			b.WriteByte(likeEscape[0])
			b.WriteByte(c)
		default:
			b.WriteByte(c)
		}
	}
	return b.String()
}

// LikeContains is the bind value for a "somewhere in the field" search.
func LikeContains(s string) string { return "%" + LikeArg(s) + "%" }

// LikePrefix is the bind value for a "starts with" scan, used with Concat for
// the engines that cannot spell pattern concatenation identically.
func LikePrefix(s string) string { return LikeArg(s) + "%" }

// LikeEscapeClause is the ESCAPE tail every LIKE predicate must carry.
func LikeEscapeClause() string { return " ESCAPE '" + likeEscape + "'" }

// CIContainsSQL is the case-insensitive "contains" predicate. LOWER on both
// sides: MySQL folded case through the column collation, PostgreSQL and SQLite
// do not, so a search must not depend on which engine the site runs on. The
// bound value has to come from LikeContains.
func CIContainsSQL(col string) string {
	return "LOWER(" + col + ") LIKE LOWER(?)" + LikeEscapeClause()
}
