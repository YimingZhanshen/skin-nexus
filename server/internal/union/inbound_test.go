package union

import (
	"context"
	"crypto/rsa"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// fakeSigner is the fallback ygg.Signer used before a hub key arrives.
type fakeSigner struct{ fp string }

func (f *fakeSigner) SignValue(v string) (string, error) { return "signed:" + v, nil }
func (f *fakeSigner) PublicKeyPEM() string               { return "FALLBACK-PEM" }
func (f *fakeSigner) KeyFingerprint() string             { return f.fp }

// inboundFixture wires API + Outbound + UnionSigner around a mock hub.
type inboundFixture struct {
	api    *API
	out    *Outbound
	signer *UnionSigner
	hubKey *rsa.PrivateKey
	log    *auditLog
	nonce  atomic.Int64
}

func newInboundFixture(t *testing.T, mutate func(cfg *Config, hooks *Hooks), respond func(w http.ResponseWriter, call hubCall)) *inboundFixture {
	t.Helper()
	hub, _ := newRecordingHub(t, respond)
	hubKey := genRSA(t, 2048)
	log := &auditLog{}

	cfg := Config{
		Enabled:          true,
		ApiRoot:          hub.URL,
		MemberKey:        "mk-in",
		HostPublicKeyPEM: pubPEM(t, &hubKey.PublicKey),
	}
	hooks := Hooks{}
	if mutate != nil {
		mutate(&cfg, &hooks)
	}
	out, err := NewOutbound(cfg, nil)
	if err != nil {
		t.Fatalf("NewOutbound: %v", err)
	}
	signer := NewUnionSigner(cfg, out, &fakeSigner{fp: "SHA256:local"}, log.fn())
	api, err := NewAPI(cfg, out, signer, nil, hooks, log.fn())
	if err != nil {
		t.Fatalf("NewAPI: %v", err)
	}
	return &inboundFixture{api: api, out: out, signer: signer, hubKey: hubKey, log: log}
}

// call sends one signed request through the route table.
func (f *inboundFixture) call(t *testing.T, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	nonce := "n-" + strconv.FormatInt(f.nonce.Add(1), 10)
	req := signedRequest(t, f.hubKey, method, "http://member.test"+path, body, time.Now(), nonce)
	rec := httptest.NewRecorder()
	f.api.Handler().ServeHTTP(rec, req)
	return rec
}

func (f *inboundFixture) callUnsigned(t *testing.T, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	f.api.Handler().ServeHTTP(rec, newRequest(t, method, "http://member.test"+path, body))
	return rec
}

func TestInboundRequiresSignedMessage(t *testing.T) {
	f := newInboundFixture(t, nil, nil)
	rec := f.callUnsigned(t, http.MethodPost, "/api/union/member/updatelist", "")
	if rec.Code != http.StatusForbidden {
		t.Fatalf("unsigned request = %d, want 403", rec.Code)
	}
	if f.log.count("union.verify.missing_header") != 1 {
		t.Fatalf("audit missing: %+v", f.log.events)
	}
}

func TestInboundUpdateList(t *testing.T) {
	var gotList any
	var gotVersion string
	f := newInboundFixture(t, func(cfg *Config, hooks *Hooks) {
		hooks.OnServerList = func(list any, version string) {
			gotList, gotVersion = list, version
		}
	}, func(w http.ResponseWriter, call hubCall) {
		jsonRespond(w, 200, map[string]any{
			"servers": []map[string]any{{"code": "A", "name": "Alpha", "bs_root": "https://a/"}},
			"version": "12",
		})
	})

	rec := f.call(t, http.MethodPost, "/api/union/member/updatelist", "")
	if rec.Code != http.StatusOK || rec.Body.Len() != 0 {
		t.Fatalf("updatelist = %d %q, want empty 200", rec.Code, rec.Body)
	}
	if gotVersion != "12" {
		t.Fatalf("OnServerList version = %q", gotVersion)
	}
	if f.api.ServerListVersion() != "12" {
		t.Fatalf("cached serverListVersion = %q", f.api.ServerListVersion())
	}
	servers, ok := gotList.([]ServerEntry)
	if !ok || len(servers) != 1 || servers[0].Code != "A" || servers[0].Name != "Alpha" || servers[0].BSRoot != "https://a/" {
		t.Fatalf("OnServerList list = %#v", gotList)
	}
	if f.log.count("union.updatelist.updated") != 1 {
		t.Fatalf("audit missing: %+v", f.log.events)
	}
}

func TestInboundUpdatePrivateKey(t *testing.T) {
	hubKey := genRSA(t, 2048)
	f := newInboundFixture(t, nil, func(w http.ResponseWriter, call hubCall) {
		jsonRespond(w, 200, map[string]any{"privateKey": privPEM(t, hubKey), "privateKeyVersion": "5"})
	})

	rec := f.call(t, http.MethodPost, "/api/union/member/updateprivatekey", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("updateprivatekey = %d %s", rec.Code, rec.Body)
	}
	if f.signer.Mode() != ModeUnionManaged {
		t.Fatalf("signer mode = %s, want union-managed", f.signer.Mode())
	}
	if f.signer.PrivateKeyVersion() != "5" || f.api.PrivateKeyVersionString() != "5" {
		t.Fatalf("version not recorded: signer=%q api=%q", f.signer.PrivateKeyVersion(), f.api.PrivateKeyVersionString())
	}
	if _, ok := f.log.last("union.updateprivatekey.updated"); !ok {
		t.Fatalf("audit missing: %+v", f.log.events)
	}
	// New signatures must use the hub key.
	sig, err := f.signer.SignValue("textures-b64")
	if err != nil || sig == "" {
		t.Fatalf("SignValue after rotation: %v", err)
	}
}

func TestInboundUpdateBackendKeyDefaultDenied(t *testing.T) {
	rotated := ""
	f := newInboundFixture(t, func(cfg *Config, hooks *Hooks) {
		hooks.OnMemberKeyRotated = func(k string) { rotated = k }
	}, nil)

	rec := f.call(t, http.MethodPost, "/api/union/member/updatebackendkey", `{"key":"new-secret"}`)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("updatebackendkey default = %d, want 403 (AllowUpdateBackendKey=false)", rec.Code)
	}
	if rotated != "" {
		t.Fatal("rotation callback must not fire when denied")
	}
	if f.out.MemberKey() != "mk-in" {
		t.Fatal("member key must not change when denied")
	}
	if _, ok := f.log.last("union.updatebackendkey.denied"); !ok {
		t.Fatalf("audit missing: %+v", f.log.events)
	}
}

func TestInboundUpdateBackendKeyAllowed(t *testing.T) {
	rotated := ""
	f := newInboundFixture(t, func(cfg *Config, hooks *Hooks) {
		cfg.AllowUpdateBackendKey = true
		hooks.OnMemberKeyRotated = func(k string) { rotated = k }
	}, nil)

	rec := f.call(t, http.MethodPost, "/api/union/member/updatebackendkey", `{"key":"new-secret"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("updatebackendkey allowed = %d %s", rec.Code, rec.Body)
	}
	if rotated != "new-secret" {
		t.Fatalf("rotation callback = %q", rotated)
	}
	if f.out.MemberKey() != "new-secret" {
		t.Fatal("outbound key must follow the rotation")
	}
	if _, ok := f.log.last("union.updatebackendkey.rotated"); !ok {
		t.Fatalf("audit missing: %+v", f.log.events)
	}
	// Malformed body is a 400 even when rotation is allowed.
	if rec := f.call(t, http.MethodPost, "/api/union/member/updatebackendkey", `{}`); rec.Code != http.StatusBadRequest {
		t.Fatalf("empty key = %d, want 400", rec.Code)
	}
}

func TestInboundSync(t *testing.T) {
	var hubBody string
	f := newInboundFixture(t, func(cfg *Config, hooks *Hooks) {
		hooks.ProfileListFunc = func() (map[string]string, error) {
			return map[string]string{"uuid-1": "Steve"}, nil
		}
	}, func(w http.ResponseWriter, call hubCall) {
		hubBody = call.body
		w.WriteHeader(http.StatusOK)
	})

	rec := f.call(t, http.MethodPost, "/api/union/member/sync", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("sync = %d %s", rec.Code, rec.Body)
	}
	var doc struct {
		ProfileList map[string]string `json:"profileList"`
	}
	if err := json.Unmarshal([]byte(hubBody), &doc); err != nil {
		t.Fatalf("hub body not JSON: %v (%s)", err, hubBody)
	}
	if doc.ProfileList["uuid-1"] != "Steve" {
		t.Fatalf("profileList = %v", doc.ProfileList)
	}
}

func TestInboundRemapUUID(t *testing.T) {
	var got map[string]string
	f := newInboundFixture(t, func(cfg *Config, hooks *Hooks) {
		hooks.OnRemapUUID = func(m map[string]string) error {
			got = m
			return nil
		}
	}, nil)

	rec := f.call(t, http.MethodPost, "/api/union/member/remapuuid", `{"remapped_uuid":{"old-1":"new-1","old-2":"new-2"}}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("remapuuid = %d %s", rec.Code, rec.Body)
	}
	if len(got) != 2 || got["old-1"] != "new-1" || got["old-2"] != "new-2" {
		t.Fatalf("OnRemapUUID got %v", got)
	}
	if rec := f.call(t, http.MethodPost, "/api/union/member/remapuuid", `{"remapped_uuid":{}}`); rec.Code != http.StatusBadRequest {
		t.Fatalf("empty remap = %d, want 400", rec.Code)
	}
}

func TestInboundDiagnoseEcho(t *testing.T) {
	f := newInboundFixture(t, nil, nil)
	base := time.Unix(1_800_000_000, 123456000)
	f.api.now = func() time.Time { return base }

	rec := f.call(t, http.MethodPost, "/api/union/member/diagnose", `{"nonce":"diag-7"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("diagnose = %d %s", rec.Code, rec.Body)
	}
	var echo map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &echo); err != nil {
		t.Fatalf("diagnose body not JSON: %v", err)
	}
	// Legacy shape: {"nonce":..., "timestamp": microtime(true)} — a single
	// object with exactly these two keys, float seconds.
	if len(echo) != 2 {
		t.Fatalf("diagnose body must have exactly nonce+timestamp: %v", echo)
	}
	if echo["nonce"] != "diag-7" {
		t.Fatalf("nonce echo = %v", echo["nonce"])
	}
	ts, ok := echo["timestamp"].(float64)
	if !ok {
		t.Fatalf("timestamp must be a JSON number: %T", echo["timestamp"])
	}
	if diff := ts - float64(base.UnixMicro())/1e6; diff > 0.001 || diff < -0.001 {
		t.Fatalf("timestamp = %v, want ~%v", ts, float64(base.UnixMicro())/1e6)
	}
}

func TestInboundQueryEmailMatrix(t *testing.T) {
	t.Run("full/found", func(t *testing.T) {
		f := newInboundFixture(t, func(cfg *Config, hooks *Hooks) {
			cfg.QueryEmailMode = QueryEmailModeFull
			hooks.QueryEmailFunc = func(username string) (string, bool, error) {
				if username != "Steve" {
					t.Fatalf("username = %q", username)
				}
				return "steve@example.com", true, nil
			}
		}, nil)
		rec := f.call(t, http.MethodGet, "/api/union/member/queryemail?username=Steve", "")
		if rec.Code != http.StatusOK {
			t.Fatalf("queryemail = %d %s", rec.Code, rec.Body)
		}
		var body map[string]string
		_ = json.Unmarshal(rec.Body.Bytes(), &body)
		if body["email"] != "steve@example.com" {
			t.Fatalf("body = %v", body)
		}
		ev, ok := f.log.last("union.queryemail")
		if !ok || ev.detail["result"] != "email" {
			t.Fatalf("audit = %+v", f.log.events)
		}
	})
	t.Run("full/not-found", func(t *testing.T) {
		f := newInboundFixture(t, func(cfg *Config, hooks *Hooks) {
			cfg.QueryEmailMode = QueryEmailModeFull
			hooks.QueryEmailFunc = func(string) (string, bool, error) { return "", false, nil }
		}, nil)
		rec := f.call(t, http.MethodGet, "/api/union/member/queryemail?username=Nobody", "")
		if rec.Code != http.StatusNoContent {
			t.Fatalf("queryemail not found = %d, want 204", rec.Code)
		}
		if ev, ok := f.log.last("union.queryemail"); !ok || ev.detail["result"] != "not_found" {
			t.Fatalf("audit = %+v", f.log.events)
		}
	})
	t.Run("full/error", func(t *testing.T) {
		f := newInboundFixture(t, func(cfg *Config, hooks *Hooks) {
			cfg.QueryEmailMode = QueryEmailModeFull
			hooks.QueryEmailFunc = func(string) (string, bool, error) { return "", false, errTest }
		}, nil)
		if rec := f.call(t, http.MethodGet, "/api/union/member/queryemail?username=Steve", ""); rec.Code != http.StatusInternalServerError {
			t.Fatalf("queryemail error = %d, want 500", rec.Code)
		}
	})
	t.Run("off", func(t *testing.T) {
		f := newInboundFixture(t, func(cfg *Config, hooks *Hooks) {
			cfg.QueryEmailMode = QueryEmailModeOff
			hooks.QueryEmailFunc = func(string) (string, bool, error) {
				t.Fatal("QueryEmailFunc must not run in off mode")
				return "", false, nil
			}
		}, nil)
		rec := f.call(t, http.MethodGet, "/api/union/member/queryemail?username=Steve", "")
		if rec.Code != http.StatusForbidden {
			t.Fatalf("queryemail off = %d, want 403", rec.Code)
		}
		ev, ok := f.log.last("union.queryemail")
		if !ok || ev.detail["result"] != "denied" {
			t.Fatalf("audit = %+v", f.log.events)
		}
	})
	t.Run("unknown mode fails closed", func(t *testing.T) {
		f := newInboundFixture(t, func(cfg *Config, hooks *Hooks) {
			cfg.QueryEmailMode = "everything"
		}, nil)
		if rec := f.call(t, http.MethodGet, "/api/union/member/queryemail?username=Steve", ""); rec.Code != http.StatusForbidden {
			t.Fatalf("unknown mode = %d, want 403", rec.Code)
		}
	})
}

var errTest = errors.New("boom")

// The two optional capability flags are read at request time, because the
// reference reads them at request time too (UnionController.php:19-20 goes
// through option() on every hello): the hub decides what it can ask this member
// site to do from this list. A boot-time snapshot would keep advertising
// "emailVerification" after the owner switched the gate off, and the hub has no
// way to tell that the answer is stale.
func TestInboundFeaturesAreLiveReads(t *testing.T) {
	on := false
	f := newInboundFixture(t, func(cfg *Config, hooks *Hooks) {
		cfg.EmailVerification = func(context.Context) bool { return on }
		cfg.InvitationCodesForUnion = func(context.Context) bool { return on }
	}, nil)

	feat := func() string {
		rec := f.call(t, http.MethodGet, "/api/union/member/", "")
		var doc struct {
			EnabledFeatures []string `json:"enabledFeatures"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &doc); err != nil {
			t.Fatalf("decode: %v (%s)", err, rec.Body)
		}
		return strings.Join(doc.EnabledFeatures, ",")
	}

	if got := feat(); got != "unionBlacklist" {
		t.Fatalf("features with both gates off = %s, want unionBlacklist only", got)
	}
	on = true
	want := "unionBlacklist,emailVerification,invitationCodesForUnion"
	if got := feat(); got != want {
		t.Fatalf("features after both gates opened = %s, want %s", got, want)
	}
}

// A station that has no reader for these options (nil resolvers) still answers
// the hello document — it just advertises the one feature it always has.
func TestInboundFeaturesWithoutResolvers(t *testing.T) {
	f := newInboundFixture(t, nil, nil)
	rec := f.call(t, http.MethodGet, "/api/union/member/", "")
	if !strings.Contains(rec.Body.String(), `"enabledFeatures":["unionBlacklist"]`) {
		t.Fatalf("body = %s, want unionBlacklist only", rec.Body.String())
	}
}

func TestInboundUpdatePluginNeverImplemented(t *testing.T) {
	f := newInboundFixture(t, nil, nil)
	rec := f.call(t, http.MethodPost, "/api/union/member/updateplugin", `{"url":"https://evil.example/p.zip"}`)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("updateplugin = %d, want 403", rec.Code)
	}
	var body map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil || body["error"] == "" || body["errorMessage"] == "" {
		t.Fatalf("error body = %s", rec.Body)
	}
	if _, ok := f.log.last("union.updateplugin.denied"); !ok {
		t.Fatalf("audit missing: %+v", f.log.events)
	}
}

func TestInboundSelfDescriptionShape(t *testing.T) {
	f := newInboundFixture(t, func(cfg *Config, hooks *Hooks) {
		cfg.YggdrasilApiVersion = "1.2.3"
		cfg.InvitationCodesForUnion = func(context.Context) bool { return true }
		cfg.UnionOAuth2 = true
	}, nil)
	f.api.SetServerListVersion("9")
	f.api.SetPrivateKeyVersion("4")

	rec := f.call(t, http.MethodGet, "/api/union/member/", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("self-description = %d", rec.Code)
	}
	if rec.Header().Get("Access-Control-Allow-Origin") != "*" {
		t.Fatal("legacy hello() sets ACAO:*")
	}
	want := `{"yggdrasilApiVersion":"1.2.3","serverListVersion":"9","privateKeyVersion":"4","enabledFeatures":["unionBlacklist","invitationCodesForUnion","unionOAuth2"]}`
	if got := strings.TrimSpace(rec.Body.String()); got != want {
		t.Fatalf("self-description shape drifted:\n got: %s\nwant: %s", got, want)
	}
}

func TestInboundDisabledZeroSurface(t *testing.T) {
	api, err := NewAPI(Config{Enabled: false}, nil, nil, nil, Hooks{}, nil)
	if err != nil {
		t.Fatalf("disabled API must construct: %v", err)
	}
	if api.Enabled() {
		t.Fatal("disabled API must report Enabled()==false")
	}
	h := api.Handler()
	for _, tc := range []struct{ method, path, body string }{
		{http.MethodPost, "/api/union/member/updatelist", ""},
		{http.MethodPost, "/api/union/member/updateprivatekey", ""},
		{http.MethodPost, "/api/union/member/updatebackendkey", `{"key":"x"}`},
		{http.MethodPost, "/api/union/member/sync", ""},
		{http.MethodPost, "/api/union/member/remapuuid", `{"remapped_uuid":{"a":"b"}}`},
		{http.MethodPost, "/api/union/member/diagnose", `{"nonce":"n"}`},
		{http.MethodGet, "/api/union/member/queryemail?username=x", ""},
		{http.MethodPost, "/api/union/member/updateplugin", ""},
		{http.MethodGet, "/api/union/member/", ""},
	} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, newRequest(t, tc.method, "http://member.test"+tc.path, tc.body))
		if rec.Code != http.StatusNotFound {
			t.Fatalf("%s %s on disabled API = %d, want 404", tc.method, tc.path, rec.Code)
		}
	}
}
