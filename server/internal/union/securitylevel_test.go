package union

// The security level is the hub's own verdict on this member site, published
// for players to read (yggdrasil-connect UnionController.php:30-42 and
// views/union.twig:177-190). It takes two hops: the member key is exchanged for
// this backend's short code, and the code addresses the level. The level body is
// a bare JSON number — on a live station the badge reads SL3, which only works
// if the number survives the decode instead of becoming zero.

import (
	"context"
	"net/http"
	"strings"
	"testing"
)

func TestSecurityLevelWalksBothHops(t *testing.T) {
	srv, calls := newRecordingHub(t, func(w http.ResponseWriter, c hubCall) {
		switch {
		case c.method == http.MethodPost && c.path == "/code":
			jsonRespond(w, http.StatusOK, map[string]string{"code": "WZBC"})
		case c.method == http.MethodGet && c.path == "/backend/WZBC/security/level":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte("3"))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	})
	o := newTestOutbound(t, srv.URL, "mk-1")

	level, err := o.SecurityLevelNow(context.Background())
	if err != nil {
		t.Fatalf("SecurityLevelNow: %v", err)
	}
	if level != 3 {
		t.Fatalf("level = %d, want 3", level)
	}
	if len(*calls) != 2 {
		t.Fatalf("hub calls = %d, want 2: %v", len(*calls), pathsOf(*calls))
	}
	first := (*calls)[0]
	if first.method != http.MethodPost || first.path != "/code" {
		t.Fatalf("hop 1 = %s %s, want POST /code", first.method, first.path)
	}
	// The member key travels as the body token on this hop, which is the whole
	// exchange: the code is public, the key is not.
	if !strings.Contains(first.body, `"token":"mk-1"`) {
		t.Fatalf("hop 1 body = %s, want the member key under token", first.body)
	}
	second := (*calls)[1]
	if second.method != http.MethodGet || second.path != "/backend/WZBC/security/level" {
		t.Fatalf("hop 2 = %s %s, want GET /backend/WZBC/security/level", second.method, second.path)
	}
}

// An empty code means the hub does not know this backend. Asking
// /backend//security/level would be a different, meaningless request — the
// reference guards the same thing with its null check.
func TestSecurityLevelStopsWhenTheHubGivesNoCode(t *testing.T) {
	srv, calls := newRecordingHub(t, func(w http.ResponseWriter, c hubCall) {
		jsonRespond(w, http.StatusOK, map[string]string{"code": ""})
	})
	o := newTestOutbound(t, srv.URL, "mk-1")

	if _, err := o.SecurityLevelNow(context.Background()); err == nil {
		t.Fatal("missing code accepted as a level")
	}
	if len(*calls) != 1 {
		t.Fatalf("hub calls = %d, want 1 (no second hop without a code): %v", len(*calls), pathsOf(*calls))
	}
}

// A level we cannot read must never reach the player as SL0: zero is a real
// rating ("this station is not on the blacklist"), so defaulting would accuse
// the site of the worst level on a parse error.
func TestSecurityLevelRefusesToInventALevel(t *testing.T) {
	for _, body := range []string{``, `null`, `SL3`, `{"level":3}`, `"three"`, `2.5`} {
		body := body
		t.Run("body="+body, func(t *testing.T) {
			srv, _ := newRecordingHub(t, func(w http.ResponseWriter, c hubCall) {
				if c.path == "/code" {
					jsonRespond(w, http.StatusOK, map[string]string{"code": "WZBC"})
					return
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(body))
			})
			o := newTestOutbound(t, srv.URL, "mk-1")
			if _, err := o.SecurityLevelNow(context.Background()); err == nil {
				t.Fatalf("hub body %q accepted as a level", body)
			}
		})
	}
}

// A hub that answers with an error is not a level of zero either.
func TestSecurityLevelPropagatesHubFailure(t *testing.T) {
	srv, _ := newRecordingHub(t, func(w http.ResponseWriter, c hubCall) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("boom"))
	})
	o := newTestOutbound(t, srv.URL, "mk-1")
	if _, err := o.SecurityLevelNow(context.Background()); err == nil {
		t.Fatal("hub 500 accepted")
	}
}

// The hub has answered this field both bare and quoted elsewhere (serverlist
// `version` tolerates number encoding, see ServerList.UnmarshalJSON), so a
// quoted number is the same answer.
func TestSecurityLevelAcceptsQuotedNumber(t *testing.T) {
	srv, _ := newRecordingHub(t, func(w http.ResponseWriter, c hubCall) {
		if c.path == "/code" {
			jsonRespond(w, http.StatusOK, map[string]string{"code": "WZBC"})
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`"1"`))
	})
	o := newTestOutbound(t, srv.URL, "mk-1")
	level, err := o.SecurityLevelNow(context.Background())
	if err != nil {
		t.Fatalf("quoted level rejected: %v", err)
	}
	if level != 1 {
		t.Fatalf("level = %d, want 1", level)
	}
}

func pathsOf(calls []hubCall) []string {
	out := make([]string, 0, len(calls))
	for _, c := range calls {
		out = append(out, c.method+" "+c.path)
	}
	return out
}
