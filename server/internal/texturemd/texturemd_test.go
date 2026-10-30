package texturemd

import (
	"strings"
	"testing"
)

func TestRenderStripsDangerousContent(t *testing.T) {
	cases := []string{
		"<script>alert(1)</script>",
		`<img src=x onerror="alert(1)">`,
		"[x](javascript:alert(1))",
		`<iframe src="https://evil.example"></iframe>`,
		`<div style="background:url(javascript:alert(1))">x</div>`,
	}
	for _, in := range cases {
		out := strings.ToLower(Render(in))
		for _, bad := range []string{"<script", "onerror", "javascript:", "<iframe", "style=", "<img"} {
			if strings.Contains(out, bad) {
				t.Errorf("input %q still contains %q: %s", in, bad, out)
			}
		}
	}
}

func TestRenderKeepsSafeMarkdown(t *testing.T) {
	out := Render("# 标题\n\n**粗体** 与 [链接](https://example.com)\n\n- a\n- b")
	for _, want := range []string{"<h1", "<strong", "https://example.com", "<li", "nofollow", "noreferrer"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in %s", want, out)
		}
	}
}

func TestExcerptPlainText(t *testing.T) {
	got := Excerpt("**粗体**\n\n<script>x</script>正文", 8)
	if strings.ContainsAny(got, "<>") || !strings.Contains(got, "正文") {
		t.Fatalf("excerpt = %q", got)
	}
}
