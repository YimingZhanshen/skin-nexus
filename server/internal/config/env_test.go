package config

// env_test.go keeps KnownEnv true. A list nobody maintains becomes a list people
// stop trusting: the warning either fires at every deploy (so it is silenced) or
// stays silent while a new variable goes unread.

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// envCallNames are the readers whose first argument is a variable name: the two
// stdlib calls plus this module's helpers. config.getenv and resolveDataPath
// forward their first argument straight to os.Getenv, so leaving them out would
// hide most of the real reads and make the "30 reads" floor the only thing
// keeping the walk honest.
var envCallNames = map[string]bool{
	"Getenv": true, "LookupEnv": true, "envOr": true,
	"getenv": true, "resolveDataPath": true,
}

// sourceEnvReads walks the whole module and returns every SKIN_* literal passed
// to an env reader, with the file that reads it.
func sourceEnvReads(t *testing.T) map[string]string {
	t.Helper()
	found := map[string]string{}
	fset := token.NewFileSet()
	root := filepath.Join("..", "..")
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if name := d.Name(); name == "data" || name == "node_modules" || name == ".git" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		file, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			return err
		}
		ast.Inspect(file, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok || len(call.Args) == 0 {
				return true
			}
			var name string
			switch fn := call.Fun.(type) {
			case *ast.SelectorExpr:
				name = fn.Sel.Name
			case *ast.Ident:
				name = fn.Name
			}
			if !envCallNames[name] {
				return true
			}
			lit, ok := call.Args[0].(*ast.BasicLit)
			if !ok {
				return true // computed name: cannot be checked statically
			}
			v := strings.Trim(lit.Value, `"`)
			if strings.HasPrefix(v, "SKIN_") {
				found[v] = path
			}
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatalf("walk: %v", err)
	}
	return found
}

func TestKnownEnvMatchesSourceReads(t *testing.T) {
	known := isKnownEnvSet()
	reads := sourceEnvReads(t)
	if len(reads) < 30 {
		t.Fatalf("found only %d env reads — the walk is broken and this test proves nothing", len(reads))
	}
	for k, where := range reads {
		if !known[k] {
			t.Errorf("%s is read in %s but not declared in KnownEnv — add it, or the boot warning fires on a variable that works", k, where)
		}
	}
	for k := range known {
		if _, ok := reads[k]; ok || isTestOnly(k) {
			continue
		}
		t.Errorf("KnownEnv lists %s but nothing reads it — delete it, or move it to ComposeEnv naming its consumer", k)
	}
}

func TestUnknownEnvReportsOnlyUnread(t *testing.T) {
	t.Setenv("SKIN_BASE_URL", "https://example.test")
	t.Setenv("SKIN_TEXUTRES_DIR", "/wrong/by/one/key") // the exact class of typo this catches
	t.Setenv("SKIN_PORT", "8080")
	got := unknownEnv()
	if len(got) != 1 || got[0] != "SKIN_TEXUTRES_DIR" {
		t.Fatalf("unknownEnv() = %v, want exactly [SKIN_TEXUTRES_DIR]", got)
	}
}

func TestKnownEnvIsCleanlyDeclared(t *testing.T) {
	seen := map[string]bool{}
	for _, k := range KnownEnv {
		if !strings.HasPrefix(k, "SKIN_") {
			t.Errorf("%s is not a SKIN_* variable", k)
		}
		if seen[k] {
			t.Errorf("%s is listed twice", k)
		}
		seen[k] = true
	}
	for k, consumer := range ComposeEnv {
		if !strings.HasPrefix(k, "SKIN_") {
			t.Errorf("%s is not a SKIN_* variable", k)
		}
		if seen[k] {
			t.Errorf("%s is in both KnownEnv and ComposeEnv", k)
		}
		if !strings.HasPrefix(consumer, "compose:") {
			t.Errorf("%s: the note must name its consumer and start with `compose:`", k)
		}
	}
}

// TestComposeFilesMatchKnownEnv is the other direction: every ${SKIN_*} the
// deploy templates interpolate must be read by the process or be a documented
// compose-only variable. A template that passes a name nothing consumes is a
// silent no-op for the operator.
func TestComposeFilesMatchKnownEnv(t *testing.T) {
	refs := regexp.MustCompile(`\$\{(SKIN_[A-Z0-9_]+)`)
	seen := 0
	for _, name := range []string{"docker-compose.yml", "docker-compose.quickstart.yml"} {
		raw, err := os.ReadFile(filepath.Join(repoRoot(t), name))
		if err != nil {
			t.Fatalf("read %s: %v (the deploy template moved; this check is now blind)", name, err)
		}
		for _, m := range refs.FindAllStringSubmatch(string(raw), -1) {
			seen++
			if !knownName(m[1]) && !composeName(m[1]) {
				t.Errorf("%s passes %s, which no process reads and ComposeEnv does not list", name, m[1])
			}
		}
	}
	if seen == 0 {
		t.Fatal("no ${SKIN_*} references found in the compose files")
	}
}

func knownName(name string) bool {
	for _, k := range KnownEnv {
		if k == name {
			return true
		}
	}
	return false
}

func composeName(name string) bool {
	_, ok := ComposeEnv[name]
	return ok
}

func isTestOnly(name string) bool {
	for _, k := range TestOnlyEnv {
		if k == name {
			return true
		}
	}
	return false
}

// repoRoot walks up to the module's go.mod, so a moved package cannot silently
// narrow what these tests look at.
func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("wd: %v", err)
	}
	for i := 0; i < 8; i++ {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			// go.mod sits in server/, one level below the repo's docs and
			// compose files.
			return filepath.Clean(filepath.Join(dir, ".."))
		}
		dir = filepath.Dir(dir)
	}
	t.Fatal("no go.mod found above the test's working directory")
	return ""
}

func isKnownEnvSet() map[string]bool {
	out := map[string]bool{}
	for _, k := range KnownEnv {
		out[k] = true
	}
	return out
}
