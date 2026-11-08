package seo

import (
	"context"
	"strings"
	"testing"
)

type memOpts struct{ kv map[string]string }

func (m *memOpts) GetOption(_ context.Context, k string) (string, bool, error) {
	v, ok := m.kv[k]
	return v, ok, nil
}
func (m *memOpts) SetOption(_ context.Context, k, v string) error {
	if m.kv == nil {
		m.kv = map[string]string{}
	}
	m.kv[k] = v
	return nil
}

// The rendered head must always carry <title>/canonical — a live 2026-10-25
// regression probe found the block missing in production landing output.
func TestRenderHeadWithAlwaysEmitsTitle(t *testing.T) {
	e := NewEngine(&memOpts{}, Defaults{SiteName: "示例站", BaseURL: "https://skin.example.com"})
	out := e.RenderHeadWith(context.Background(), "<html><head><!--skin-seo--></head></html>", PageMeta{Description: "desc"})
	if !strings.Contains(out, "<title>"+defaultTitle("示例站", zhCopy)+"</title>") {
		t.Fatalf("title missing:\n%s", out)
	}
	if !strings.Contains(out, `<link rel="canonical" href="https://skin.example.com/">`) {
		t.Fatalf("canonical missing:\n%s", out)
	}
	out2 := e.RenderHeadWith(context.Background(), "<html><head><!--skin-seo--></head></html>", PageMeta{TitleSuffix: "Steve", Path: "/profile/Steve"})
	if !strings.Contains(out2, "<title>Steve - "+defaultTitle("示例站", zhCopy)+"</title>") {
		t.Fatalf("per-page title missing:\n%s", out2)
	}
}
