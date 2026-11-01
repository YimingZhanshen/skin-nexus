// sqldir.go: the statement splitter the tracked migrator feeds to a live
// connection. The shipped DDL contains no routines or DELIMITER blocks, so
// splitting on a line-final ';' is enough; whole-line '--' comments are dropped
// (an inline comment rides along with its statement, which every engine
// accepts).
package migrate

import "strings"

// SplitSQL splits a DDL script into executable statements.
func SplitSQL(script string) []string {
	var out []string
	var sb strings.Builder
	for _, line := range strings.Split(script, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "--") {
			continue
		}
		sb.WriteString(line)
		sb.WriteString("\n")
		if strings.HasSuffix(trimmed, ";") {
			stmt := strings.TrimSpace(sb.String())
			if stmt != "" && stmt != ";" {
				out = append(out, strings.TrimSuffix(stmt, ";"))
			}
			sb.Reset()
		}
	}
	if s := strings.TrimSpace(sb.String()); s != "" {
		out = append(out, s)
	}
	return out
}
