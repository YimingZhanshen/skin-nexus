package oidc

// The device form is the page a launcher opens when the reader cannot run the
// SPA. It has to carry the reader's language, and its labels are handed to the
// page as JS string literals — so the assertions below are about the quoting
// html/template produces in that context, not only about the words.

import (
	"strings"
	"testing"
)

func renderDeviceForm(t *testing.T, lang string) string {
	t.Helper()
	var sb strings.Builder
	if err := deviceFormTmpl.Execute(&sb, map[string]string{"Lang": lang}); err != nil {
		t.Fatalf("execute: %v", err)
	}
	return sb.String()
}

func TestDeviceFormChineseUnchanged(t *testing.T) {
	page := renderDeviceForm(t, "zh-CN")
	for _, want := range []string{
		`<html lang="zh-CN">`,
		"<title>设备授权</title>",
		`<div id="app">加载中…</div>`,
		"已处理，可返回启动器。",
		"失败：{err}",
		"未知错误",
		"提交失败：{err}",
		"授权 {client}",
		"用户码：",
		"请求权限：",
		"同意",
		"拒绝",
	} {
		if !strings.Contains(page, want) {
			t.Errorf("chinese device page missing %q", want)
		}
	}
}

func TestDeviceFormEnglish(t *testing.T) {
	page := renderDeviceForm(t, "en")
	for _, want := range []string{
		`<html lang="en">`,
		"<title>Device authorization</title>",
		"Done — you can return to your launcher.",
		"Approve",
		"Deny",
		"User code:",
	} {
		if !strings.Contains(page, want) {
			t.Errorf("english device page missing %q", want)
		}
	}
	// No Chinese copy may leak into the English page — including inside the
	// script, where a stray label would be invisible to a grep of the HTML.
	for i, r := range page {
		if r >= 0x4E00 && r <= 0x9FFF {
			end := i + 60
			if end > len(page) {
				end = len(page)
			}
			t.Fatalf("english device page contains CJK at %d: %q", i, page[i:end])
		}
	}
}

// The labels reach the script as JS values; an unquoted one would be a syntax
// error and the page would silently keep saying 加载中….
func TestDeviceFormLabelsAreValidJS(t *testing.T) {
	page := renderDeviceForm(t, "en")
	i := strings.Index(page, "const L = {")
	if i < 0 {
		t.Fatal("label map missing")
	}
	block := page[i : i+strings.Index(page[i:], "};")+1]
	for _, line := range strings.Split(block, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || !strings.Contains(line, ":") {
			continue
		}
		value := strings.TrimSpace(strings.SplitN(line, ":", 2)[1])
		value = strings.TrimSuffix(value, ",")
		if !strings.HasPrefix(value, "\"") || !strings.HasSuffix(value, "\"") {
			t.Errorf("label %q is not a quoted JS string: %q", line, value)
		}
	}
}

// The device page reads these answers through the same envelope helper as every
// other web call: `code` is translatable, the English text is only a fallback.
// Chinese literals in `error` used to mean every language but zh-CN read the
// server's Chinese, and an unknown code could not be translated at all.
func TestDeviceErrorsCarryTranslatableCodes(t *testing.T) {
	for _, tc := range []struct{ code, msg string }{
		{"device.code_invalid", "invalid user code"},
		{"device.code_expired", "user code expired"},
		{"device.approve_failed", "approval failed"},
	} {
		got := deviceErr(tc.code, tc.msg)
		if got["code"] != tc.code || got["error"] != tc.code {
			t.Errorf("deviceErr(%q) = %v, want code and error both set to the code", tc.code, got)
		}
		if got["message"] != tc.msg {
			t.Errorf("deviceErr(%q) message = %v, want an English fallback %q", tc.code, got["message"], tc.msg)
		}
	}
}
