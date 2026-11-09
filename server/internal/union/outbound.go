package union

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"skin-nexus/server/internal/accesslog"
)

// Outbound is the member -> hub client (analysis/yggdrasil-connect.md §4.2,
// field names frozen). Every call carries the X-Union-Member-Key header, is
// bounded by a timeout and returns wrapped errors. It is safe for concurrent
// use; the member key is swappable at runtime (updatebackendkey rotation).
type Outbound struct {
	root   atomic.Value // string: hub API root without trailing slash
	client *http.Client
	key    atomic.Value // string: union member key
	to     time.Duration
	enable atomic.Bool // hot-toggled by union_enabled (2026-10-24 拍板)
	// access logs every outbound hub call (union_out rows for the admin
	// panel); nil = off. Recording never affects the call itself.
	access *accesslog.Logger
}

// SetAccess installs the outbound traffic logger (main wires the
// access_logs sink once, before any sync/goroutine starts).
func (o *Outbound) SetAccess(l *accesslog.Logger) { o.access = l }

// logOut records one outbound call: outcome ok (2xx) / error (transport or
// decode) / fail (hub non-2xx); detail carries method, path, status and
// latency — the operator's "did the hub answer, and how fast" line.
//
// The detail capture is built here rather than from a request: this is the one
// logged surface that knows the upstream status and the transport error before
// anything is written, and it has no client headers to whitelist. Like every
// other capture it is dropped by accesslog.Logger unless the diagnostic window
// is open.
func (o *Outbound) logOut(method, path string, status int, dur time.Duration, err error) {
	if o.access == nil {
		return
	}
	outcome := accesslog.OutcomeOK
	switch {
	case err != nil && status == 0:
		outcome = accesslog.OutcomeError
	case err != nil:
		outcome = accesslog.OutcomeFail
	}
	o.access.Log(context.Background(), accesslog.Entry{
		Kind:    accesslog.UnionOut,
		Outcome: outcome,
		Who:     method + " " + path,
		Detail:  fmt.Sprintf("status=%d dur=%dms", status, dur.Milliseconds()),
		Capture: outCapture(status, dur, err),
	})
}

// outCapture shapes the union_out detail row. upstream_code keeps the hub's
// answer when there was one ("hub.504") and the transport failure when there
// was not ("transport"), because those are the two different debugging
// questions; the error text itself already lives in the caller's audit row.
func outCapture(status int, dur time.Duration, err error) *accesslog.Capture {
	c := &accesslog.Capture{StatusCode: status, DurationMS: dur.Milliseconds()}
	switch {
	case err != nil && status > 0:
		c.UpstreamCode = fmt.Sprintf("hub.%d", status)
	case err != nil:
		c.UpstreamCode = "transport"
	}
	return c
}

// keyStore semantics: the member key is read on every request and rewritten
// only by rotation, so an atomic value beats a mutex here.

// APIError is a non-2xx hub response (except proxy endpoints, which relay it).
type APIError struct {
	Method     string
	Path       string
	StatusCode int
	Headers    http.Header // response headers (diagnostic evidence)
	Body       json.RawMessage
}

func (e *APIError) Error() string {
	return fmt.Sprintf("union: %s %s: hub returned %d: %s", e.Method, e.Path, e.StatusCode, strings.TrimSpace(string(e.Body)))
}

// DiagnoseReport mirrors the legacy yggdrasil-connect triggerDiagnose
// response exactly (config.twig renders these rows verbatim):
//
//	{status:'ok',    data:<hub json>}                     — hub 2xx
//	{status:'error', data:{status_code,headers,body}}     — hub non-2xx
//	{status:'error', data:{exception}}                    — transport failure
//
// Cause is our addition and sits beside that shape, never inside it: `data`
// stays the legacy payload so anything already reading it keeps working, while
// the panel gets one token naming why leg one failed. See classifyError for
// the vocabulary. Probe is the same idea for this site's own side of the call
// (see diagnose_probe.go): what TLS we negotiated and how the hub clock compares
// to ours, neither of which the hub can report about itself.
type DiagnoseReport struct {
	Status string          `json:"status"` // "ok" | "error"
	Data   json.RawMessage `json:"data"`
	Cause  string          `json:"cause,omitempty"`
	Probe  *DiagnoseProbe  `json:"probe,omitempty"`
}

// Hub wire documents (§4.2). JSON tags are the hub field names, verbatim.
type (
	// ServerEntry is one member entry of the hub server list:
	// {code,name,bs_root}.
	ServerEntry struct {
		Code   string `json:"code"`
		Name   string `json:"name"`
		BSRoot string `json:"bs_root"`
	}

	// ServerList is the GET /serverlist response: {servers:[...],version}.
	ServerList struct {
		Servers []ServerEntry `json:"servers"`
		Version string        `json:"version"`
	}

	// PrivateKeyDoc is the GET /privatekey response:
	// {privateKey,privateKeyVersion}.
	PrivateKeyDoc struct {
		PrivateKey        string `json:"privateKey"`
		PrivateKeyVersion string `json:"privateKeyVersion"`
	}

	// BindToken is the POST /profile/bind response: {token} (2 minute TTL).
	BindToken struct {
		Token string `json:"token"`
	}

	// ServerCode is the POST /code response: {code}.
	ServerCode struct {
		Code string `json:"code"`
	}

	// OAuth2BackendKey is the GET /oauth2/backend response: {publicKey} (the
	// hub RSA encryption key used by the grant token).
	OAuth2BackendKey struct {
		PublicKey string `json:"publicKey"`
	}

	// ProfileDetail is GET /profile/detail/{uuid}. Fields whose hub type is
	// not yet pinned (hub-side source is closed, analysis §3 待验证) stay as
	// raw JSON so nothing breaks on wire drift; names are verbatim.
	ProfileDetail struct {
		InternalID     json.RawMessage   `json:"internal_id"`
		Bind           []json.RawMessage `json:"bind"`
		BindMappedName string            `json:"bind_mapped_name"`
		UUID           string            `json:"uuid"`
		MappedUUID     string            `json:"mapped_uuid"`
		BindStatus     json.RawMessage   `json:"bind_status"`
		Backend        json.RawMessage   `json:"backend"`
	}

	// UnmappedProfile is one entry of GET /profile/unmapped/byname/{name}
	// (name-conflict candidates; legacy keys the list by internal_id).
	UnmappedProfile struct {
		InternalID json.RawMessage `json:"internal_id"`
		UUID       string          `json:"uuid"`
		Name       string          `json:"name"`
	}

	// ProxyResult relays a blacklist hub response untouched (status + body).
	ProxyResult struct {
		StatusCode int
		Body       json.RawMessage
	}
)

// flexString tolerates the hub encoding a spec-"string" field as a bare JSON
// number (observed 2026-10-24 on the live hub: /serverlist `version` and
// /privatekey `privateKeyVersion` arrive as numbers, e.g. 3 not "3"). Wire
// field NAMES and public struct fields stay frozen (string) — this is purely
// additive decode tolerance, same trap family as MS1 `{id}` narrowing.
type flexString string

// UnmarshalJSON accepts "x", 3, 3.0, true, and null (→ "").
func (f *flexString) UnmarshalJSON(b []byte) error {
	b = bytes.TrimSpace(b)
	if len(b) == 0 || string(b) == "null" {
		*f = ""
		return nil
	}
	if b[0] == '"' {
		var s string
		if err := json.Unmarshal(b, &s); err != nil {
			return err
		}
		*f = flexString(s)
		return nil
	}
	*f = flexString(b) // bare number/bool literal as-is
	return nil
}

// UnmarshalJSON shadow-decodes ServerList so `version` tolerates number
// encoding while the exported field remains a plain string.
func (s *ServerList) UnmarshalJSON(b []byte) error {
	var raw struct {
		Servers []ServerEntry `json:"servers"`
		Version flexString    `json:"version"`
	}
	if err := json.Unmarshal(b, &raw); err != nil {
		return err
	}
	s.Servers = raw.Servers
	s.Version = string(raw.Version)
	return nil
}

// UnmarshalJSON shadow-decodes PrivateKeyDoc so `privateKeyVersion` tolerates
// number encoding while the exported field remains a plain string.
func (p *PrivateKeyDoc) UnmarshalJSON(b []byte) error {
	var raw struct {
		PrivateKey        flexString `json:"privateKey"`
		PrivateKeyVersion flexString `json:"privateKeyVersion"`
	}
	if err := json.Unmarshal(b, &raw); err != nil {
		return err
	}
	p.PrivateKey = string(raw.PrivateKey)
	p.PrivateKeyVersion = string(raw.PrivateKeyVersion)
	return nil
}

// NewOutbound builds the hub client. Disabled config yields an inert client
// (Enabled() == false, all calls return ErrDisabled) and never touches the
// network. client may be nil (default: plain http.Client, per-call timeouts).
func NewOutbound(cfg Config, client *http.Client) (*Outbound, error) {
	o := &Outbound{client: client, to: cfg.requestTimeout()}
	o.enable.Store(cfg.Enabled)
	if o.client == nil {
		o.client = &http.Client{}
	}
	if !cfg.Enabled {
		o.root.Store(cfg.apiRoot())
		o.key.Store("")
		return o, nil
	}
	root := cfg.apiRoot()
	if root == "" {
		return nil, errors.New("union: ApiRoot is required")
	}
	u, err := url.Parse(root)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return nil, errors.New("union: ApiRoot must be an absolute URL")
	}
	o.root.Store(root)
	o.key.Store(cfg.MemberKey)
	return o, nil
}

// Enabled reports whether the client performs real work (nil-safe).
func (o *Outbound) Enabled() bool { return o != nil && o.enable.Load() }

// SetEnabled hot-toggles the client (union_enabled option, 2026-10-24 拍板:
// UI 开关即改即生效). Off = every call fails fast with ErrDisabled.
func (o *Outbound) SetEnabled(on bool) {
	if o != nil {
		o.enable.Store(on)
	}
}

// SetAPIRoot hot-swaps the hub root (union_api_root option). Same validation
// as the constructor; the old root stays on rejection.
func (o *Outbound) SetAPIRoot(root string) error {
	if o == nil {
		return ErrDisabled
	}
	root = strings.TrimRight(strings.TrimSpace(root), "/")
	u, err := url.Parse(root)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return errors.New("union: ApiRoot must be an absolute URL")
	}
	o.root.Store(root)
	return nil
}

// APIRoot reads the current hub root (hot-swappable via SetAPIRoot).
func (o *Outbound) APIRoot() string {
	if o == nil {
		return ""
	}
	s, _ := o.root.Load().(string)
	return s
}

// apiRoot is the internal spelling.
func (o *Outbound) apiRoot() string { return o.APIRoot() }

// SetMemberKey swaps the rotating member key (updatebackendkey channel).
func (o *Outbound) SetMemberKey(key string) {
	if o != nil {
		o.key.Store(key)
	}
}

// MemberKey returns the current member key.
func (o *Outbound) MemberKey() string {
	if o == nil {
		return ""
	}
	s, _ := o.key.Load().(string)
	return s
}

// ---- profile events (§4.2) ----

// SyncProfileCreate reports a new profile: POST /profile {id,name}. Note the
// hub field is "id" (not "uuid") — frozen.
func (o *Outbound) SyncProfileCreate(ctx context.Context, uuid, name string) error {
	body := map[string]string{"id": uuid, "name": name}
	_, _, err := o.doJSON(ctx, http.MethodPost, "/profile", body, nil)
	return err
}

// SyncProfileRename reports a rename: PUT /profile/{uuid} {name}.
func (o *Outbound) SyncProfileRename(ctx context.Context, uuid, name string) error {
	body := map[string]string{"name": name}
	_, _, err := o.doJSON(ctx, http.MethodPut, "/profile/"+url.PathEscape(uuid), body, nil)
	return err
}

// SyncProfileDelete reports a deletion: DELETE /profile/{uuid}.
func (o *Outbound) SyncProfileDelete(ctx context.Context, uuid string) error {
	_, _, err := o.doJSON(ctx, http.MethodDelete, "/profile/"+url.PathEscape(uuid), nil, nil)
	return err
}

// Sync uploads the local profile roster: POST /sync {profileList:{uuid:name}}.
func (o *Outbound) Sync(ctx context.Context, profileList map[string]string) error {
	body := map[string]any{"profileList": profileList}
	_, _, err := o.doJSON(ctx, http.MethodPost, "/sync", body, nil)
	return err
}

// ---- hub data pulls ----

// ServerList pulls GET /serverlist -> {servers:[{code,name,bs_root}],version}.
func (o *Outbound) ServerList(ctx context.Context) (*ServerList, error) {
	var doc ServerList
	if _, _, err := o.doJSON(ctx, http.MethodGet, "/serverlist", nil, &doc); err != nil {
		return nil, err
	}
	return &doc, nil
}

// Privatekey pulls GET /privatekey -> {privateKey,privateKeyVersion}. Name
// spelling follows the spec/hub field ("privatekey"); the hub-issued key
// replaces the shared texture-signing key (§7.1-5).
func (o *Outbound) Privatekey(ctx context.Context) (*PrivateKeyDoc, error) {
	var doc PrivateKeyDoc
	if _, _, err := o.doJSON(ctx, http.MethodGet, "/privatekey", nil, &doc); err != nil {
		return nil, err
	}
	return &doc, nil
}

// HostPublicKey pulls the hub message-signing key from the hub root JSON
// (GET {api_root} -> union_host_signature_public_key), the exact source
// UnionHostVerify.php:67 uses per request. Callers cache; wire untouched.
func (o *Outbound) HostPublicKey(ctx context.Context) (string, error) {
	var doc struct {
		Key string `json:"union_host_signature_public_key"`
	}
	if _, _, err := o.doJSON(ctx, http.MethodGet, "", nil, &doc); err != nil {
		return "", err
	}
	if strings.TrimSpace(doc.Key) == "" {
		return "", errors.New("union: hub root has no union_host_signature_public_key")
	}
	return doc.Key, nil
}

// Diagnose runs the hub connectivity check (POST /diagnose) and reports in
// the legacy triggerDiagnose shape so the admin UI can render 1:1 rows and
// export the report. Every outcome IS a result: hub non-2xx and transport
// failures come back as {status:"error", data:{...evidence}}; the error
// return is reserved for the module-disabled gate.
//
// Timeout pairing is a contract, not a detail: this 10s budget must stay
// under the panel's own 15s fetch (web/src/api.js) and well under the server's
// 300s WriteTimeout. If the frontend gave up first, the operator would read a
// transport cause for a hub that answered fine.
func (o *Outbound) Diagnose(ctx context.Context) (*DiagnoseReport, error) {
	if !o.Enabled() {
		return nil, ErrDisabled
	}
	payload, err := json.Marshal(map[string]any{})
	if err != nil {
		return nil, fmt.Errorf("union: encode diagnose body: %w", err)
	}
	start := time.Now()
	resp, raw, err := o.roundTrip(ctx, http.MethodPost, "/diagnose", payload, diagnoseTimeout)
	st := 0
	if resp != nil {
		st = resp.StatusCode
	}
	o.logOut(http.MethodPost, "/diagnose", st, time.Since(start), err)
	// roundTrip reports transport outcomes only; a hub that answered 401 did
	// not error at the socket level. Turning a non-2xx into *APIError is what
	// doJSONInner normally does, and Diagnose needs the same rule or a rejected
	// member key would be filed as a successful leg.
	if err == nil && (st < 200 || st > 299) {
		err = &APIError{Method: http.MethodPost, Path: "/diagnose", StatusCode: st, Headers: resp.Header, Body: raw}
	}
	// The observation is of the connection that just happened, so it is
	// reported even when the hub answered 403 — a rejected key on a healthy
	// handshake is exactly the distinction the panel is for.
	probe := buildProbe(resp, time.Now())
	if err != nil {
		var apiErr *APIError
		if errors.As(err, &apiErr) {
			// Hub answered non-2xx: status_code/headers/body are the report.
			data, _ := json.Marshal(map[string]any{
				"status_code": apiErr.StatusCode,
				"headers":     apiErr.Headers,
				"body":        string(apiErr.Body),
			})
			return &DiagnoseReport{Status: "error", Data: data, Cause: classifyError(err), Probe: probe}, nil
		}
		data, _ := json.Marshal(map[string]any{"exception": err.Error()})
		return &DiagnoseReport{Status: "error", Data: data, Cause: classifyError(err), Probe: probe}, nil
	}
	return &DiagnoseReport{Status: "ok", Data: raw, Probe: probe}, nil
}

// ServerCode exchanges the member key for the public server code:
// POST /code {token} -> {code} (§4.2 安全级别).
func (o *Outbound) ServerCode(ctx context.Context, token string) (*ServerCode, error) {
	body := map[string]string{"token": token}
	var doc ServerCode
	if _, _, err := o.doJSON(ctx, http.MethodPost, "/code", body, &doc); err != nil {
		return nil, err
	}
	return &doc, nil
}

// SecurityLevelNow reads the hub's rating for this backend. It walks the same
// two hops the reference does on every page view (UnionController.php:30-42):
// the member key buys the short code that names us at the hub, and the code
// addresses the level.
//
// Every failure returns an error rather than a number. Zero is a real rating
// ("not on the union blacklist"), so answering 0 for "the hub would not say"
// would accuse the station of the worst level it can have.
func (o *Outbound) SecurityLevelNow(ctx context.Context) (int, error) {
	doc, err := o.ServerCode(ctx, o.MemberKey())
	if err != nil {
		return 0, err
	}
	code := strings.TrimSpace(doc.Code)
	if code == "" {
		return 0, fmt.Errorf("union: hub returned no backend code for this member key")
	}
	return o.SecurityLevel(ctx, code)
}

// SecurityLevel reads GET /backend/{code}/security/level — the response body is
// the number itself, not an envelope.
func (o *Outbound) SecurityLevel(ctx context.Context, code string) (int, error) {
	_, raw, err := o.doJSON(ctx, http.MethodGet,
		"/backend/"+url.PathEscape(code)+"/security/level", nil, nil)
	if err != nil {
		return 0, err
	}
	return parseSecurityLevel(raw)
}

// parseSecurityLevel accepts the bare number and its quoted twin. The hub has
// answered version-bearing fields both ways elsewhere (ServerList.UnmarshalJSON
// tolerates a numeric `version`), and a level we cannot read is an error, never
// a default.
func parseSecurityLevel(raw []byte) (int, error) {
	text := strings.TrimSpace(string(raw))
	if text == "" {
		return 0, errors.New("union: security level: hub sent an empty body")
	}
	var v any
	if err := json.Unmarshal([]byte(text), &v); err != nil {
		return 0, fmt.Errorf("union: security level: hub sent %q, not a number", text)
	}
	switch t := v.(type) {
	case float64:
		if t != float64(int(t)) || t < 0 {
			return 0, fmt.Errorf("union: security level: hub sent %q, not a level", text)
		}
		return int(t), nil
	case string:
		n, err := strconv.Atoi(strings.TrimSpace(t))
		if err != nil || n < 0 {
			return 0, fmt.Errorf("union: security level: hub sent %q, not a level", text)
		}
		return n, nil
	default:
		return 0, fmt.Errorf("union: security level: hub sent %q, not a level", text)
	}
}

// OAuth2BackendKey pulls the hub RSA encryption public key:
// GET /oauth2/backend -> {publicKey}.
func (o *Outbound) OAuth2BackendKey(ctx context.Context) (*OAuth2BackendKey, error) {
	var doc OAuth2BackendKey
	if _, _, err := o.doJSON(ctx, http.MethodGet, "/oauth2/backend", nil, &doc); err != nil {
		return nil, err
	}
	return &doc, nil
}

// ---- binding family ----

// Bind starts binding: POST /profile/bind {uuid} -> {token} (2 min validity).
func (o *Outbound) Bind(ctx context.Context, uuid string) (*BindToken, error) {
	body := map[string]string{"uuid": uuid}
	var doc BindToken
	if _, _, err := o.doJSON(ctx, http.MethodPost, "/profile/bind", body, &doc); err != nil {
		return nil, err
	}
	return &doc, nil
}

// BindTo binds this profile to a primary profile token:
// POST /profile/bindto {uuid,token}.
func (o *Outbound) BindTo(ctx context.Context, uuid, token string) error {
	body := map[string]string{"uuid": uuid, "token": token}
	_, _, err := o.doJSON(ctx, http.MethodPost, "/profile/bindto", body, nil)
	return err
}

// Unbind unbinds: POST /profile/unbind {uuid}.
func (o *Outbound) Unbind(ctx context.Context, uuid string) error {
	body := map[string]string{"uuid": uuid}
	_, _, err := o.doJSON(ctx, http.MethodPost, "/profile/unbind", body, nil)
	return err
}

// RemapUUID requests a UUID remap across the union:
// POST /profile/remapuuid {me,target}.
func (o *Outbound) RemapUUID(ctx context.Context, me, target string) error {
	body := map[string]string{"me": me, "target": target}
	_, _, err := o.doJSON(ctx, http.MethodPost, "/profile/remapuuid", body, nil)
	return err
}

// UnmappedByName lists name-conflict candidates:
// GET /profile/unmapped/byname/{name}.
func (o *Outbound) UnmappedByName(ctx context.Context, name string) ([]UnmappedProfile, error) {
	var doc []UnmappedProfile
	path := "/profile/unmapped/byname/" + url.PathEscape(name)
	if _, _, err := o.doJSON(ctx, http.MethodGet, path, nil, &doc); err != nil {
		return nil, err
	}
	return doc, nil
}

// DetailByUUID fetches hub-side binding detail: GET /profile/detail/{uuid}.
func (o *Outbound) DetailByUUID(ctx context.Context, uuid string) (*ProfileDetail, error) {
	var doc ProfileDetail
	path := "/profile/detail/" + url.PathEscape(uuid)
	if _, _, err := o.doJSON(ctx, http.MethodGet, path, nil, &doc); err != nil {
		return nil, err
	}
	return &doc, nil
}

// ---- blacklist proxy (原样透传) ----

// BlacklistQuery proxies GET /blacklist/query?<query>.
func (o *Outbound) BlacklistQuery(ctx context.Context, query url.Values) (*ProxyResult, error) {
	path := "/blacklist/query"
	if len(query) > 0 {
		path += "?" + query.Encode()
	}
	return o.doProxy(ctx, http.MethodGet, path, nil)
}

// BlacklistCreate proxies POST /blacklist/restful with the raw JSON body.
func (o *Outbound) BlacklistCreate(ctx context.Context, body json.RawMessage) (*ProxyResult, error) {
	return o.doProxy(ctx, http.MethodPost, "/blacklist/restful", body)
}

// BlacklistInvalidate proxies PUT /blacklist/invalidate/{id}.
func (o *Outbound) BlacklistInvalidate(ctx context.Context, id string) (*ProxyResult, error) {
	return o.doProxy(ctx, http.MethodPut, "/blacklist/invalidate/"+url.PathEscape(id), nil)
}

// BlacklistDelete proxies DELETE /blacklist/restful/{id}.
func (o *Outbound) BlacklistDelete(ctx context.Context, id string) (*ProxyResult, error) {
	return o.doProxy(ctx, http.MethodDelete, "/blacklist/restful/"+url.PathEscape(id), nil)
}

// ---- transport ----

// maxResponseBytes bounds how much of a hub response is buffered. The cap is
// deliberately generous: the relay route passes an opaque hub answer straight
// through to the member's panel, so a tight ceiling would corrupt real data
// rather than protect anything. Its only job is to stop "the hub keeps
// talking" from becoming "the member runs out of memory" — the per-call context
// bounds how long a call may take, and five seconds of a fast pipe is hundreds
// of megabytes. Same shape as internal/outbound: check the declared length,
// read one byte past the cap, and fail if the extra byte exists.
const maxResponseBytes = 8 << 20

func readCapped(resp *http.Response) ([]byte, error) {
	if resp.ContentLength > maxResponseBytes {
		return nil, fmt.Errorf("union: hub response exceeds %d bytes", maxResponseBytes)
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes+1))
	if err != nil {
		return nil, err
	}
	if len(raw) > maxResponseBytes {
		return nil, fmt.Errorf("union: hub response exceeds %d bytes", maxResponseBytes)
	}
	return raw, nil
}

// doJSON performs one authenticated JSON call. Non-2xx becomes *APIError; a
// nil out skips response decoding.
// doJSON is the instrumented wrapper: one timing/logging point for every
// hub call (the inner function keeps the original contract verbatim).
func (o *Outbound) doJSON(ctx context.Context, method, path string, body any, out any) (int, json.RawMessage, error) {
	start := time.Now()
	st, raw, err := o.doJSONInner(ctx, method, path, body, out)
	o.logOut(method, path, st, time.Since(start), err)
	return st, raw, err
}

func (o *Outbound) doJSONInner(ctx context.Context, method, path string, body any, out any) (int, json.RawMessage, error) {
	if !o.Enabled() {
		return 0, nil, ErrDisabled
	}
	var payload []byte
	switch b := body.(type) {
	case nil:
	case json.RawMessage:
		payload = b
	default:
		enc, err := json.Marshal(b)
		if err != nil {
			return 0, nil, fmt.Errorf("union: encode %s %s body: %w", method, path, err)
		}
		payload = enc
	}

	resp, raw, err := o.roundTrip(ctx, method, path, payload, o.to)
	st := 0
	if resp != nil {
		st = resp.StatusCode
	}
	if err != nil {
		return st, nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return st, raw, &APIError{Method: method, Path: path, StatusCode: resp.StatusCode, Headers: resp.Header, Body: raw}
	}
	if out != nil && len(raw) > 0 {
		if err := json.Unmarshal(raw, out); err != nil {
			return st, raw, fmt.Errorf("union: %s %s: decode response: %w", method, path, err)
		}
	}
	return st, raw, nil
}

// roundTrip is the single place the hub wire shape is built: method, URL,
// member-key header, content type, deadline, capped read. Every caller goes
// through it — the JSON calls, the relay and the diagnose observation — so an
// instrumented path can never drift from the request path.
//
// The timeout is a parameter, not a constant of this function, because the
// legacy client gives diagnose ten seconds while everything else gets five.
// Nesting a second WithTimeout inside here would silently clamp that ten to
// five; the deadline is set once, here.
//
// The response body is always consumed and closed before returning; what
// callers get back is the status, headers and TLS state, which stay readable
// after the body is gone.
func (o *Outbound) roundTrip(ctx context.Context, method, path string, payload []byte, timeout time.Duration) (*http.Response, []byte, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	var reader io.Reader
	if payload != nil {
		reader = bytes.NewReader(payload)
	}
	req, err := http.NewRequestWithContext(ctx, method, o.apiRoot()+path, reader)
	if err != nil {
		return nil, nil, fmt.Errorf("union: build %s %s: %w", method, path, err)
	}
	req.Header.Set(HeaderMemberKey, o.MemberKey())
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := o.client.Do(req)
	if err != nil {
		return nil, nil, fmt.Errorf("union: %s %s: %w", method, path, err)
	}
	defer resp.Body.Close()
	raw, err := readCapped(resp)
	if err != nil {
		return resp, nil, fmt.Errorf("union: %s %s: read response: %w", method, path, err)
	}
	return resp, raw, nil
}

// doProxy is the instrumented wrapper (see doJSON).
func (o *Outbound) doProxy(ctx context.Context, method, path string, body json.RawMessage) (*ProxyResult, error) {
	start := time.Now()
	res, err := o.doProxyInner(ctx, method, path, body)
	st := 0
	if res != nil {
		st = res.StatusCode
	}
	o.logOut(method, path, st, time.Since(start), err)
	return res, err
}

// doProxyInner relays a blacklist call untouched: transport errors are
// returned, hub non-2xx answers are passed through in ProxyResult (legacy
// behavior of UnionBlacklistController: response(body, status)).
func (o *Outbound) doProxyInner(ctx context.Context, method, path string, body json.RawMessage) (*ProxyResult, error) {
	if !o.Enabled() {
		return nil, ErrDisabled
	}
	ctx, cancel := context.WithTimeout(ctx, o.to)
	defer cancel()

	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, o.apiRoot()+path, reader)
	if err != nil {
		return nil, fmt.Errorf("union: build %s %s: %w", method, path, err)
	}
	req.Header.Set(HeaderMemberKey, o.MemberKey())
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := o.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("union: %s %s: %w", method, path, err)
	}
	defer resp.Body.Close()
	raw, err := readCapped(resp)
	if err != nil {
		return nil, fmt.Errorf("union: %s %s: read response: %w", method, path, err)
	}
	return &ProxyResult{StatusCode: resp.StatusCode, Body: raw}, nil
}
