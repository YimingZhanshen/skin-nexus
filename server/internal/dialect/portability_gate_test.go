// The portability gate: engine-specific SQL has exactly two homes in this
// project — internal/dialect plus internal/dbx (the helpers and the connection
// setup) and the per-engine DDL under server/migrations/<engine>/ — plus the
// migration CLI/bootstrap in internal/migrate, which has to know each engine's
// spelling of "does this object already exist". Anywhere else, a MySQL-only
// construct compiles fine and only fails when somebody points the site at
// SQLite or PostgreSQL, which is how this repo accumulated its first dialect
// bugs.
//
// The check reads SQL string literals out of the AST rather than grepping the
// file, so a comment or an error message cannot trip it and a query cannot hide
// in a const.
package dialect

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// bannedTokens are MySQL spellings that have a portable equivalent here. The
// second column is what the store is expected to call instead.
var bannedTokens = []struct {
	token   string
	replace string
}{
	{"NOW(3)", "bind a Go UTC time (write and compare on one clock)"},
	{"NOW()", "bind a Go UTC time (write and compare on one clock)"},
	{"DATE_ADD", "compute the deadline in Go and bind it"},
	{"INSERT IGNORE", "Engine.InsertIgnore"},
	{"ON DUPLICATE KEY", "Engine.Upsert"},
	{"GROUP_CONCAT", "Engine.ConcatList"},
	{"IFNULL", "COALESCE"},
	{"GET_LOCK", "Engine.SessionLock"},
	{"RELEASE_LOCK", "Engine.SessionLock"},
	{"<=>", "dialect.NullSafeEq"},
	{"COLLATE utf8mb4", "Engine.CIEquals / Engine.CIContains"},
	{"CONCAT(", "Engine.Concat"},
	{" IF(", "CASE WHEN … THEN … ELSE … END"},
	{"FOR UPDATE", "Engine.LockSQL"},
	{"information_schema", "Engine.TableExistsQuery"},
	{"DATABASE()", "Engine.TableExistsQuery"},
	{"@@session", "a Go-side value, or the engine helper"},
	{"SHOW CREATE TABLE", "the per-engine catalogue query"},
}

var (
	reSQL   = regexp.MustCompile(`(?i)\b(select|insert|update|delete)\b`)
	reLimit = regexp.MustCompile(`(?i)\b(delete from|update)\b[\s\S]*\blimit\b`)
	reBina  = regexp.MustCompile(`(?i)\bbinary\s+[a-z_]`)
)

// sqlViolations reports every MySQL-only construct in one SQL literal.
func sqlViolations(val string) []string {
	if !reSQL.MatchString(val) {
		return nil
	}
	var out []string
	for _, b := range bannedTokens {
		if strings.Contains(val, b.token) {
			out = append(out, b.token+" — "+b.replace)
		}
	}
	// Only a top-level LIMIT is MySQL's extension; inside a derived table it is
	// the portable spelling of "delete/update these n rows".
	if reLimit.MatchString(topLevel(val)) {
		out = append(out, "DELETE/UPDATE … LIMIT — select the ids in a subquery (SELECT … LIMIT) instead")
	}
	if reBina.MatchString(val) {
		out = append(out, "BINARY <col> — compare byte-exact per engine, or use Engine.CIEquals")
	}
	return out
}

func topLevel(sql string) string {
	var out strings.Builder
	depth := 0
	for _, r := range sql {
		switch r {
		case '(':
			depth++
		case ')':
			if depth > 0 {
				depth--
			}
		default:
			if depth == 0 {
				out.WriteRune(r)
			}
		}
	}
	return out.String()
}

// allowlist holds the package prefixes that may speak one engine's dialect.
var allowlist = []string{
	filepath.Join("internal", "dialect"),
	filepath.Join("internal", "dbx"),
	filepath.Join("internal", "migrate"),
}

func TestNoMySQLOnlySQLOutsideTheDialectLayer(t *testing.T) {
	root := filepath.Join("..", "..")
	var findings []string
	err := filepath.Walk(root, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			switch info.Name() {
			case ".git", "node_modules", "testdata":
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(p, ".go") || strings.HasSuffix(p, "_test.go") {
			return nil
		}
		rel, _ := filepath.Rel(root, p)
		for _, a := range allowlist {
			if strings.HasPrefix(rel, a+string(filepath.Separator)) {
				return nil
			}
		}
		fset := token.NewFileSet()
		f, perr := parser.ParseFile(fset, p, nil, 0)
		if perr != nil {
			return perr
		}
		ast.Inspect(f, func(n ast.Node) bool {
			lit, ok := n.(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING {
				return true
			}
			val, uerr := parseStringLit(lit.Value)
			if uerr != nil {
				return true
			}
			for _, v := range sqlViolations(val) {
				pos := fset.Position(lit.Pos())
				findings = append(findings,
					rel+":"+strconv.Itoa(pos.Line)+": "+v)
			}
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatalf("walk: %v", err)
	}
	if len(findings) > 0 {
		t.Errorf("%d MySQL-only construct(s) outside the dialect layer:\n%s",
			len(findings), strings.Join(findings, "\n"))
	}
}

// TestPortabilityGateIsNotVacuous keeps the gate itself under test: a scanner
// that matches nothing would let the whole codebase drift back to MySQL-only
// SQL and still report green.
func TestPortabilityGateIsNotVacuous(t *testing.T) {
	cases := []struct {
		sql     string
		wantHit bool
		why     string
	}{
		{"DELETE FROM t WHERE a = 1 LIMIT 10", true, "top-level DELETE…LIMIT"},
		{"UPDATE t SET x = NOW(3) WHERE id = ?", true, "SQL clock"},
		{"SELECT id FROM t FOR UPDATE", true, "row lock clause"},
		{"INSERT IGNORE INTO t (a) VALUES (?)", true, "MySQL-only insert"},
		{"SELECT CONCAT(a, b) FROM t", true, "scalar concat"},
		{"SELECT id FROM t WHERE a <=> b", true, "null-safe equals"},
		{"DELETE FROM t WHERE id IN (SELECT i FROM (SELECT id AS i FROM t LIMIT 10) x)", false, "LIMIT inside a derived table is portable"},
		{"SELECT id FROM t ORDER BY id LIMIT ?", false, "SELECT…LIMIT is fine everywhere"},
		{"UPDATE t SET a = CASE WHEN b = 0 THEN NULL ELSE b END WHERE id = ?", false, "CASE WHEN is the portable form"},
		{"this is not sql at all", false, "non-SQL text is not scanned"},
	}
	for _, c := range cases {
		got := len(sqlViolations(c.sql)) > 0
		if got != c.wantHit {
			t.Errorf("gate on %q (%s) = %v, want %v", c.sql, c.why, got, c.wantHit)
		}
	}
}

func parseStringLit(raw string) (string, error) {
	if strings.HasPrefix(raw, "`") {
		return strings.Trim(raw, "`"), nil
	}
	return strconv.Unquote(raw)
}
