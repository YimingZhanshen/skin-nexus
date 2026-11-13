package logx

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"
)

// Fatal is the only way this package touches the process, so it is the only
// thing worth pinning: the line must be recorded at error level with its
// key/value pairs intact, and the exit status must be 1 — the same contract
// log.Fatal had.
func TestFatalLogsAtErrorLevelThenExitsWithOne(t *testing.T) {
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo})))
	defer slog.SetDefault(prev)

	code := -1
	oldExit := exit
	exit = func(c int) { code = c }
	defer func() { exit = oldExit }()

	Fatal("boot failed", "error", "boom")

	if code != 1 {
		t.Fatalf("exit status = %d, want 1 — log.Fatal's contract", code)
	}
	out := buf.String()
	for _, want := range []string{`level=ERROR`, `msg="boot failed"`, `error=boom`} {
		if !strings.Contains(out, want) {
			t.Fatalf("fatal line missing %q:\n%s", want, out)
		}
	}
}
