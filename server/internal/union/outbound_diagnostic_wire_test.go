package union

// outbound_diagnostic_wire_test.go — 诊断模式是旁路记录，不是 wire 契约的一部分。
//
// `internal/union` 是本仓库的冻结区（字段名、头名、拼接顺序与 hub 互操作，AGENTS.md 铁律 6）。
// 5c 把明细采集接进了这里的 logOut（outbound.go 新增 Capture 字段与 outCapture），
// 于是「打开诊断窗口会不会改变成员站发给 hub 的请求」成了一个必须用测试回答的问题，
// 而不是一句「只影响日志」的口头保证。这个文件就是那条保证的证据：同一个出站调用在
// 窗口关与窗口开两种状态下，方法、路径、查询串、请求体与**全部**请求头必须逐字相同；
// 唯一允许不同的是 logger 里那条 Entry 是否带 Capture。
//
// 后半句同样重要：如果两种状态下 logger 收到的东西一模一样，这个测试就是空跑——
// 所以它同时断言「窗口开 ⇒ 确实带了 capture」「窗口关 ⇒ 确实没带」。

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"skin-nexus/server/internal/accesslog"
)

type wireCall struct {
	method string
	path   string
	query  string
	body   string
	head   map[string][]string
}

func TestDiagnosticWindowDoesNotAlterOutboundWire(t *testing.T) {
	at := time.Date(2026, 11, 8, 9, 0, 0, 0, time.UTC)

	// run 做一次 SyncProfileCreate，返回 hub 看到的请求与 logger 收到的 capture。
	run := func(t *testing.T, windowOpen bool) (wireCall, *accesslog.Capture) {
		t.Helper()
		var mu sync.Mutex
		var seen []wireCall
		hub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			b, _ := io.ReadAll(r.Body)
			h := make(map[string][]string, len(r.Header))
			for k, v := range r.Header {
				h[strings.ToLower(k)] = append([]string(nil), v...)
			}
			mu.Lock()
			seen = append(seen, wireCall{r.Method, r.URL.Path, r.URL.RawQuery, string(b), h})
			mu.Unlock()
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{"ok": true})
		}))
		t.Cleanup(hub.Close)

		var mu2 sync.Mutex
		var captured *accesslog.Capture
		logger := accesslog.New(func(_ context.Context, e accesslog.Entry) error {
			mu2.Lock()
			defer mu2.Unlock()
			if e.Capture != nil {
				captured = e.Capture
			}
			return nil
		})
		vals := map[string]string{}
		if windowOpen {
			vals["access_detail_until"] = at.Add(2 * time.Hour).Format(time.RFC3339)
		}
		logger.SetDiagnostic(&accesslog.Diagnostic{
			Option: func(_ context.Context, key string) (string, bool, error) {
				v, ok := vals[key]
				return v, ok, nil
			},
			Now: func() time.Time { return at },
		})

		o := newTestOutbound(t, hub.URL, "mk-1")
		o.SetAccess(logger)
		if err := o.SyncProfileCreate(context.Background(), "uuid-1", "Steve"); err != nil {
			t.Fatalf("SyncProfileCreate (window=%v): %v", windowOpen, err)
		}
		mu.Lock()
		defer mu.Unlock()
		if len(seen) != 1 {
			t.Fatalf("window=%v: hub saw %d calls, want exactly 1", windowOpen, len(seen))
		}
		return seen[0], captured
	}

	closedCall, closedCap := run(t, false)
	openCall, openCap := run(t, true)

	// 1) 窗口开着确实采到了东西，关着确实没有——否则下面那条相等断言毫无内容。
	if closedCap != nil {
		t.Errorf("closed window still produced a capture: %+v", closedCap)
	}
	if openCap == nil {
		t.Fatal("open window produced NO capture — the wire comparison below would be vacuous")
	}

	// 2) 发往 hub 的请求逐字相同。
	if closedCall.method != openCall.method || closedCall.path != openCall.path ||
		closedCall.query != openCall.query || closedCall.body != openCall.body {
		t.Errorf("diagnostic window changed the outbound request line/body:\n closed=%s %s?%s %q\n open  =%s %s?%s %q",
			closedCall.method, closedCall.path, closedCall.query, closedCall.body,
			openCall.method, openCall.path, openCall.query, openCall.body)
	}

	var names []string
	for k := range closedCall.head {
		names = append(names, k)
	}
	for k := range openCall.head {
		if _, ok := closedCall.head[k]; !ok {
			t.Errorf("open window added header %q to the hub request", k)
		}
	}
	sort.Strings(names)
	for _, k := range names {
		a, b := strings.Join(closedCall.head[k], "\x00"), strings.Join(openCall.head[k], "\x00")
		if a != b {
			t.Errorf("header %q differs between closed and open: %q vs %q", k, a, b)
		}
	}
	if len(openCall.head) != len(closedCall.head) {
		t.Errorf("header count differs: closed=%d open=%d", len(closedCall.head), len(openCall.head))
	}

	// 3) 采集的是 hub 的答案，不是我们发出去的东西：capture 里不该出现成员站密钥。
	if openCap != nil && strings.Contains(openCap.HeadersJSON(), "mk-1") {
		t.Errorf("member key leaked into the captured header dump: %s", openCap.HeadersJSON())
	}
}
