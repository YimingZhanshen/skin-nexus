package adminapi

// The 1 MiB admin body cap sits outside the mux, so any route that carries a
// file has to be exempted there or its own, larger ceiling is never reached.
// This gate reads the package source and checks the exemption list against the
// handlers that actually parse a multipart form — in both directions, because
// an exemption nobody uses is the same silent widening as a limit nobody gets.

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"sort"
	"strings"
	"testing"
)

// multipartHandlers returns the names of handlers in this package that read a
// multipart form from the request.
func multipartHandlers(t *testing.T, files []*ast.File) map[string]bool {
	t.Helper()
	out := map[string]bool{}
	for _, f := range files {
		for _, d := range f.Decls {
			fn, ok := d.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				sel, ok := n.(*ast.SelectorExpr)
				if ok && sel.Sel.Name == "ParseMultipartForm" {
					out[fn.Name.Name] = true
				}
				return true
			})
		}
	}
	return out
}

// handleNames digs the b.handleX selectors out of a HandleFunc second argument,
// which is usually wrapped (b.auth(b.handleThemeUpload)).
func handleNames(n ast.Node) []string {
	var out []string
	ast.Inspect(n, func(x ast.Node) bool {
		sel, ok := x.(*ast.SelectorExpr)
		if ok && strings.HasPrefix(sel.Sel.Name, "handle") {
			out = append(out, sel.Sel.Name)
		}
		return true
	})
	return out
}

func TestPackageUploadRoutesMatchTheirHandlers(t *testing.T) {
	fset := token.NewFileSet()
	pkg, err := parser.ParseDir(fset, ".", func(fi os.FileInfo) bool {
		return !strings.HasSuffix(fi.Name(), "_test.go")
	}, 0)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	var files []*ast.File
	for _, p := range pkg {
		for _, f := range p.Files {
			files = append(files, f)
		}
	}
	uploader := multipartHandlers(t, files)
	if len(uploader) == 0 {
		t.Fatal("no handler in this package parses a multipart form; the gate has become vacuous")
	}

	routes := map[string][]string{} // path -> handler names
	for _, f := range files {
		ast.Inspect(f, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok || sel.Sel.Name != "HandleFunc" || len(call.Args) != 2 {
				return true
			}
			lit, ok := call.Args[0].(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING {
				return true
			}
			parts := strings.Fields(lit.Value)
			if len(parts) != 2 {
				return true
			}
			path := strings.Trim(parts[1], `"`)
			routes[path] = append(routes[path], handleNames(call.Args[1])...)
			return true
		})
	}

	// Every upload handler must be reachable through a listed route.
	listed := map[string]bool{}
	for path, names := range routes {
		for _, h := range names {
			if uploader[h] {
				listed[path] = true
			}
		}
	}
	var missing, stale []string
	for path := range listed {
		if !packageUploadRoutes[path] {
			missing = append(missing, path)
		}
	}
	for path := range packageUploadRoutes {
		if !listed[path] {
			stale = append(stale, path)
		}
	}
	sort.Strings(missing)
	sort.Strings(stale)
	if len(missing) > 0 {
		t.Fatalf("these admin routes parse a multipart form but the 1 MiB cap still applies: %s",
			strings.Join(missing, ", "))
	}
	if len(stale) > 0 {
		t.Fatalf("these paths are exempt from the 1 MiB cap but no handler reads a multipart form any more: %s",
			strings.Join(stale, ", "))
	}
}
