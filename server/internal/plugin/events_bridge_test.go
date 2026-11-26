package plugin

import (
	"context"
	"errors"
	"testing"
	"time"
)

const eventsManifest = `{
  "name": "events",
  "version": "1.0.0",
  "hookApi": 2,
  "entrypoint": "index.js",
  "permissions": ["store:kv", "events:subscribe", "auth:guards"]
}`

const eventsScript = `
skin.events.on("player.added", function (e) {
  skin.store.kv.set("last-player", e.playerName);
});
skin.guards.on("user.registration.check", function (e) {
  if (e.data && e.data.email === "blocked@example.com") {
    return {allow: false, message: "email blocked by test guard"};
  }
  return {allow: true};
}, {onError: "deny"});
skin.guards.on("auth.login.check", function (e) {
  if (e.playerName === "timeout") { while (true) {} }
  return true;
}, {onError: "deny", timeoutMS: 10});
`

func newEventsHost(t *testing.T) (*Manager, *memPlatformStore) {
	t.Helper()
	root := t.TempDir()
	writePlugin(t, root, "events", eventsManifest, eventsScript)
	st := newMemPlatformStore()
	host := NewHost(RuntimeConfig{Dir: root, HostVersion: "0.4.0", Store: st})
	t.Cleanup(func() { _ = host.Close() })
	if err := host.Load(root); err != nil {
		t.Fatal(err)
	}
	if err := host.Enable(context.Background(), "events"); err != nil {
		t.Fatal(err)
	}
	return host, st
}

func TestGenericEventDelivered(t *testing.T) {
	host, st := newEventsHost(t)
	host.EmitEvent(context.Background(), "player.added", EventPayload{PlayerName: "Steve"})
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		value, _, ok, err := st.GetPluginKV(context.Background(), "events", "", "last-player")
		if err != nil {
			t.Fatal(err)
		}
		if ok && value == "Steve" {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("event not delivered")
}

func TestGuardDenyAllowAndTimeout(t *testing.T) {
	host, _ := newEventsHost(t)
	err := host.CheckGuard(context.Background(), "user.registration.check", EventPayload{
		Data: map[string]any{"email": "blocked@example.com"},
	})
	var ge *GuardError
	if !errors.As(err, &ge) || ge.Message != "email blocked by test guard" {
		t.Fatalf("deny = %v", err)
	}
	if err := host.CheckGuard(context.Background(), "user.registration.check", EventPayload{
		Data: map[string]any{"email": "ok@example.com"},
	}); err != nil {
		t.Fatalf("allow = %v", err)
	}
	if err := host.CheckGuard(context.Background(), "auth.login.check", EventPayload{PlayerName: "timeout"}); err == nil {
		t.Fatal("timeout guard should deny")
	} else if ge, ok := err.(*GuardError); !ok || ge.Code != "plugin.guard_unavailable" {
		t.Fatalf("timeout = %v", err)
	}
}

func TestGuardBreakGlass(t *testing.T) {
	host, _ := newEventsHost(t)
	if err := host.SetGuardsBypassed(context.Background(), true); err != nil {
		t.Fatal(err)
	}
	if err := host.CheckGuard(context.Background(), "user.registration.check", EventPayload{
		Data: map[string]any{"email": "blocked@example.com"},
	}); err != nil {
		t.Fatalf("bypass = %v", err)
	}
	if err := host.SetGuardsBypassed(context.Background(), false); err != nil {
		t.Fatal(err)
	}
	if err := host.CheckGuard(context.Background(), "user.registration.check", EventPayload{
		Data: map[string]any{"email": "blocked@example.com"},
	}); err == nil {
		t.Fatal("guard still bypassed")
	}
}
