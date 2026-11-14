package main

// spacing_grid_gate_test.go — the SSR inline styles sit on the 4px grid.
//
// W6-1 swept style.css; this gate covers the two server-rendered style hosts
// (landing.go, texturepage.go). Spacing-like properties must use multiples of
// 4px. Exemptions, each deliberate:
//   - negative values: off-screen parking (skip-link left:-9999px), not spacing
//   - 2px: the micro-step W6-1 kept (e.g. .meta dt margin-bottom)
//   - border / outline / border-radius: hairlines and the radius scale (6/10/14)
//     are not spacing and are excluded by the property allowlist
// Comments are stripped first so prose numbers (the 379px/222px measurement
// note) never trip the gate.

import (
	"os"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

var gridProps = map[string]bool{
	"margin": true, "margin-top": true, "margin-right": true, "margin-bottom": true, "margin-left": true,
	"padding": true, "padding-top": true, "padding-right": true, "padding-bottom": true, "padding-left": true,
	"gap": true, "width": true, "height": true,
	"min-width": true, "min-height": true, "max-width": true, "max-height": true,
	"top": true, "left": true, "bottom": true, "right": true,
	"font-size": true, "line-height": true,
}

var (
	gridDeclRE  = regexp.MustCompile(`([a-z-]+)\s*:\s*([^;{}]+)`)
	gridPxRE    = regexp.MustCompile(`(-?\d+(?:\.\d+)?)px`)
	gridComment = regexp.MustCompile(`(?s)/\*.*?\*/|<!--.*?-->`)
	gridGoCmtRE = regexp.MustCompile(`(?m)^\s*//.*$`)
)

func TestSSRInlineStylesOnFourGrid(t *testing.T) {
	for _, f := range []string{"landing.go", "texturepage.go"} {
		raw, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		src := gridComment.ReplaceAllString(string(raw), "")
		src = gridGoCmtRE.ReplaceAllString(src, "")
		for _, m := range gridDeclRE.FindAllStringSubmatch(src, -1) {
			prop, val := m[1], m[2]
			if !gridProps[prop] {
				continue
			}
			for _, pm := range gridPxRE.FindAllStringSubmatch(val, -1) {
				v, _ := strconv.ParseFloat(pm[1], 64)
				if v < 0 || v == 2 {
					continue
				}
				if int(v)%4 != 0 {
					t.Errorf("%s: %s: %s — %spx is off the 4px grid", f, prop, strings.TrimSpace(val), pm[1])
				}
			}
		}
	}
}
