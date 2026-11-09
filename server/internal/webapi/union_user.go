// union_user.go is the session-authenticated user surface of the MUA Union
// member module: server list, profile detail, bind/bindto/unbind, UUID remap.
//
// It mounts ONLY when the Union module is enabled (SKIN_UNION_ENABLED) —
// disabled deployments keep a zero surface (routes simply do not exist).
// All mutating calls enforce ownership: the target uuid must belong to the
// logged-in user, so a stolen session cannot bind other players' profiles.
package webapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"sync"
	"time"

	"skin-nexus/server/internal/account"
	"skin-nexus/server/internal/i18n"
	"skin-nexus/server/internal/store"
	"skin-nexus/server/internal/union"
)

// MountUnion registers /api/v1/union/* on the web mux when outbound is live.
// names is the peer-name resolver (nil = never fill a name, the list shows
// addresses exactly as before).
func (b *Backend) MountUnion(out *union.Outbound, names *union.MemberNames) {
	b.union = out
	b.unionSrv = &unionServerCache{}
	b.unionLevel = &unionLevelCache{}
	b.memberNames = names
}

// unionServerCache caches the hub server list for 60s (the list changes on
// hub-side rotation, not per request; the UI shows it on every page open).
type unionServerCache struct {
	mu        sync.Mutex
	doc       *union.ServerList
	fetchedAt time.Time
}

func (c *unionServerCache) get(ctx context.Context, out *union.Outbound) (*union.ServerList, error) {
	c.mu.Lock()
	if c.doc != nil && time.Since(c.fetchedAt) < time.Minute {
		doc := c.doc
		c.mu.Unlock()
		return doc, nil
	}
	c.mu.Unlock()
	doc, err := out.ServerList(ctx)
	if err != nil {
		return nil, err
	}
	c.mu.Lock()
	c.doc, c.fetchedAt = doc, time.Now()
	c.mu.Unlock()
	return doc, nil
}

// ownedPlayer resolves uuid -> player and enforces uid ownership. Accepts
// dashed or undashed hex; returns the canonical stored uuid.
func (b *Backend) ownedPlayer(ctx context.Context, uid int64, uuid string) (string, error) {
	norm := strings.ReplaceAll(strings.ToLower(uuid), "-", "")
	if norm == "" {
		return "", errUnionBadInput
	}
	p, err := b.Store.PlayerByUUID(ctx, norm)
	if err != nil {
		return "", err
	}
	if p.UID != uid {
		return "", errUnionNotOwned
	}
	return p.UUID, nil
}

var (
	errUnionBadInput = errors.New("bad uuid")
	errUnionNotOwned = errors.New("player not owned")
)

// writeUnionUpstreamErr answers an outbound Union failure: hub errors are 502
// (the UI says "Union service is unavailable"), a disabled module 404. One writer rather
// than a status/message/code triple each handler forwards, because a code that
// travels through three variables is a code no gate can check.
func writeUnionUpstreamErr(w http.ResponseWriter, err error) {
	var ae *union.APIError
	switch {
	case errors.Is(err, union.ErrDisabled):
		// Reachable only in the window between the hot gate and the call.
		writeErrCode(w, http.StatusNotFound, "Union is not enabled", i18n.MsgUnavailable)
	case errors.As(err, &ae):
		writeErrCode(w, http.StatusBadGateway, "Union service is unavailable", i18n.MsgUnionUpstream)
	default:
		writeErrCode(w, http.StatusBadGateway, "Union service is unavailable", i18n.MsgUnionUpstream)
	}
}

func (b *Backend) unionUser(w http.ResponseWriter, r *http.Request) (int64, bool) {
	// Hot gate FIRST (2026-10-24 拍板「关闭即全 404」): a disabled module must
	// be indistinguishable from an absent one even before authentication.
	if b.union == nil || !b.union.Enabled() {
		http.NotFound(w, r)
		return 0, false
	}
	uid, err := b.currentUser(r)
	if err != nil {
		writeErrCode(w, http.StatusUnauthorized, "not logged in", i18n.MsgUnauthorized)
		return 0, false
	}
	return uid, true
}

// GET /api/v1/union/servers — hub server list (60s cache).
func (b *Backend) handleUnionServers(w http.ResponseWriter, r *http.Request) {
	if _, ok := b.unionUser(w, r); !ok {
		return
	}
	doc, err := b.unionSrv.get(r.Context(), b.union)
	if err != nil {
		writeUnionUpstreamErr(w, err)
		return
	}
	// Names come from the peers themselves — the hub stores none (measured:
	// 53/53 entries arrive with name empty). Fill serves what is already known
	// and schedules reads for the rest; it never waits on them here.
	servers := b.memberNames.Fill(doc.Servers)
	writeJSON(w, http.StatusOK, map[string]any{"servers": servers, "version": doc.Version})
}

// unionLevelCache holds the hub's security level for this station.
//
// The reference re-reads both hops on every page view (UnionController.php:30).
// That is two hub calls per player per visit, forever, for a number that moves
// when an operator joins the union blacklist — so this caches the good answer
// for ten minutes and a refused read for one, which keeps a hub outage from
// turning every page open into a fresh timeout while still picking up a real
// change inside one session.
type unionLevelCache struct {
	mu     sync.Mutex
	level  int
	err    error
	loaded bool
	at     time.Time
}

const (
	unionLevelTTL       = 10 * time.Minute
	unionLevelRetryTTL  = time.Minute
	unionLevelFetchTime = 12 * time.Second // both hops, plus margin over the 5s bound
)

func (c *unionLevelCache) get(ctx context.Context, out *union.Outbound) (int, error) {
	c.mu.Lock()
	if c.loaded {
		ttl := unionLevelTTL
		if c.err != nil {
			ttl = unionLevelRetryTTL
		}
		if time.Since(c.at) < ttl {
			level, err := c.level, c.err
			c.mu.Unlock()
			return level, err
		}
	}
	c.mu.Unlock()

	// Detached from the request: the answer is the same for everyone who asks in
	// the next ten minutes, and a browser closing its tab must not leave the
	// cache holding a cancelled-read error that every other player then inherits.
	fctx, cancel := context.WithTimeout(context.Background(), unionLevelFetchTime)
	defer cancel()
	level, err := out.SecurityLevelNow(fctx)

	c.mu.Lock()
	c.level, c.err, c.loaded, c.at = level, err, true, time.Now()
	c.mu.Unlock()
	return level, err
}

// GET /api/v1/union/security-level — the hub's rating of this station (SL3/1/0).
// A level is only ever a number the hub actually gave: unreadable upstream is a
// 502 here, never a zero, because zero means "this station is not on the union
// blacklist" and would be an accusation.
func (b *Backend) handleUnionSecurityLevel(w http.ResponseWriter, r *http.Request) {
	if _, ok := b.unionUser(w, r); !ok {
		return
	}
	level, err := b.unionLevel.get(r.Context(), b.union)
	if err != nil {
		writeUnionUpstreamErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"level": level})
}

// GET /api/v1/union/detail?uuid= — hub-side profile detail (raw fields kept).
func (b *Backend) handleUnionDetail(w http.ResponseWriter, r *http.Request) {
	uid, ok := b.unionUser(w, r)
	if !ok {
		return
	}
	canon, err := b.ownedPlayer(r.Context(), uid, r.URL.Query().Get("uuid"))
	if err != nil {
		b.unionOwnErr(w, err)
		return
	}
	doc, err := b.union.DetailByUUID(r.Context(), canon)
	if err != nil {
		writeUnionUpstreamErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, doc)
}

// GET /api/v1/union/unmapped?name= — name-conflict candidates for bindto.
func (b *Backend) handleUnionUnmapped(w http.ResponseWriter, r *http.Request) {
	if _, ok := b.unionUser(w, r); !ok {
		return
	}
	name := r.URL.Query().Get("name")
	if name == "" {
		writeErrCode(w, http.StatusBadRequest, "missing name", i18n.MsgMissingField)
		return
	}
	list, err := b.union.UnmappedByName(r.Context(), name)
	if err != nil {
		writeUnionUpstreamErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"profiles": list})
}

// POST /api/v1/union/bind {uuid} -> {token} (hub 2-minute TTL).
func (b *Backend) handleUnionBind(w http.ResponseWriter, r *http.Request) {
	uid, ok := b.unionUser(w, r)
	if !ok {
		return
	}
	var body struct {
		UUID string `json:"uuid"`
	}
	if err := jsonDecode(r, &body); err != nil {
		writeErrCode(w, http.StatusBadRequest, "bad body", i18n.MsgBadBody)
		return
	}
	canon, err := b.ownedPlayer(r.Context(), uid, body.UUID)
	if err != nil {
		b.unionOwnErr(w, err)
		return
	}
	tok, err := b.union.Bind(r.Context(), canon)
	if err != nil {
		writeUnionUpstreamErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"token": tok.Token})
}

// POST /api/v1/union/bindto {uuid, token} — bind via a hub-side code token.
func (b *Backend) handleUnionBindTo(w http.ResponseWriter, r *http.Request) {
	uid, ok := b.unionUser(w, r)
	if !ok {
		return
	}
	var body struct {
		UUID  string `json:"uuid"`
		Token string `json:"token"`
	}
	if err := jsonDecode(r, &body); err != nil || body.Token == "" {
		writeErrCode(w, http.StatusBadRequest, "bad body", i18n.MsgBadBody)
		return
	}
	canon, err := b.ownedPlayer(r.Context(), uid, body.UUID)
	if err != nil {
		b.unionOwnErr(w, err)
		return
	}
	if err := b.union.BindTo(r.Context(), canon, body.Token); err != nil {
		writeUnionUpstreamErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// POST /api/v1/union/unbind {uuid}
func (b *Backend) handleUnionUnbind(w http.ResponseWriter, r *http.Request) {
	uid, ok := b.unionUser(w, r)
	if !ok {
		return
	}
	var body struct {
		UUID string `json:"uuid"`
	}
	if err := jsonDecode(r, &body); err != nil {
		writeErrCode(w, http.StatusBadRequest, "bad body", i18n.MsgBadBody)
		return
	}
	canon, err := b.ownedPlayer(r.Context(), uid, body.UUID)
	if err != nil {
		b.unionOwnErr(w, err)
		return
	}
	if err := b.union.Unbind(r.Context(), canon); err != nil {
		writeUnionUpstreamErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// POST /api/v1/union/remapuuid {me, target} — both profiles must be owned.
func (b *Backend) handleUnionRemapUUID(w http.ResponseWriter, r *http.Request) {
	uid, ok := b.unionUser(w, r)
	if !ok {
		return
	}
	var body struct {
		Me     string `json:"me"`
		Target string `json:"target"`
	}
	if err := jsonDecode(r, &body); err != nil {
		writeErrCode(w, http.StatusBadRequest, "bad body", i18n.MsgBadBody)
		return
	}
	me, err := b.ownedPlayer(r.Context(), uid, body.Me)
	if err != nil {
		b.unionOwnErr(w, err)
		return
	}
	// target is the union-MAPPED uuid — frequently ANOTHER site's uuid for
	// the same profile (MUA review P1: requiring local ownership made the
	// cross-site 1.19+ remap impossible). Only shape is validated here.
	target := strings.ToLower(strings.ReplaceAll(strings.TrimSpace(body.Target), "-", ""))
	if !isHex32(target) {
		writeErrCode(w, http.StatusBadRequest, "target UUID is not valid", i18n.MsgPlayerUUIDBad)
		return
	}
	if err := b.union.RemapUUID(r.Context(), me, target); err != nil {
		writeUnionUpstreamErr(w, err)
		return
	}
	// Converge our own row BEFORE saying ok (MUA review P1: returning ok
	// while the local table kept the stale uuid left this site as the only
	// one not converged — the signed-profile breakage persisted).
	if err := b.Store.ConvergeUUID(r.Context(), me, target); err != nil {
		b.Store.InsertAudit(r.Context(), uid, "union_remapuuid_local_conflict",
			map[string]any{"me": me, "target": target, "error": err.Error()})
		if errors.Is(err, store.ErrUUIDConflict) {
			writeErrCode(w, http.StatusConflict, "the hub accepted it, but that UUID is taken locally and must be merged by an operator", i18n.MsgUnionUUIDConflict)
			return
		}
		writeLogged(r, w, http.StatusInternalServerError, i18n.MsgUnionConvergeFailed, "the hub accepted it but local convergence failed, retry", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// isHex32 validates the canonical uuid_map identity shape.
func isHex32(s string) bool {
	if len(s) != 32 {
		return false
	}
	for _, r := range s {
		if (r < '0' || r > '9') && (r < 'a' || r > 'f') {
			return false
		}
	}
	return true
}

func (b *Backend) unionOwnErr(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, errUnionNotOwned):
		writeErrCode(w, http.StatusForbidden, "this character belongs to another account", i18n.MsgPlayerNotOwner)
	case errors.Is(err, errUnionBadInput):
		writeErrCode(w, http.StatusBadRequest, "bad uuid", i18n.MsgPlayerUUIDBad)
	default:
		writeErrCode(w, http.StatusNotFound, "player not found", account.CodeNotFound)
	}
}

// jsonDecode is a one-shot JSON body decoder shared by union handlers.
func jsonDecode(r *http.Request, v any) error {
	return json.NewDecoder(r.Body).Decode(v)
}
