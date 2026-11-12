package plugin

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"skin-nexus/server/internal/i18n"
)

// maxRouteResponse caps a plugin route payload so a runaway plugin cannot
// stream unbounded bodies through the host.
const maxRouteResponse = 1 << 20

const (
	defaultBodyLimit   = 64 << 10
	maxBodyLimit       = 1 << 20
	maxBinaryBodyLimit = 4 << 20
	csrfCookieName     = "skin_csrf"
	csrfHeaderName     = "X-CSRF-Token"
)

// Handler serves /api/plugins/<name>/<path> for enabled plugins. Routes are
// looked up per request from the live registry, so enable/disable takes
// effect immediately without touching the host ServeMux.
func (m *Manager) Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rest := strings.TrimPrefix(r.URL.Path, "/api/plugins/")
		name, sub, _ := strings.Cut(rest, "/")
		subPath := "/" + sub

		m.mu.RLock()
		inst, ok := m.instances[name]
		var rt *runtime
		if ok && inst.enabled {
			rt = inst.rt
		}
		m.mu.RUnlock()
		if rt == nil {
			m.writePluginError(w, r, http.StatusNotFound, "plugin.not_found", "plugin not found", name, "", start)
			return
		}

		if subPath == "/_csrf" {
			m.handleCSRF(w, r, name, start)
			return
		}

		entry, params, found, pathMatched := lookupRoute(rt, r.Method, subPath)
		if !found {
			if pathMatched {
				w.Header().Set("Allow", allowedMethods(rt, subPath))
				m.writePluginError(w, r, http.StatusMethodNotAllowed, "plugin.method_not_allowed", "method not allowed", name, "", start)
			} else {
				m.writePluginError(w, r, http.StatusNotFound, "plugin.route_not_found", "route not found", name, "", start)
			}
			return
		}

		var identity *SessionIdentity
		if entry.tier != RouteTierPublic {
			if entry.tier == RouteTierBearer {
				id, berr := m.resolveBearer(r)
				if berr != nil {
					m.writeBearerError(w, r, name, start, berr)
					return
				}
				identity = id
			} else {
				identity, ok = m.resolveSession(r)
				if !ok {
					m.writePluginError(w, r, http.StatusUnauthorized, "plugin.unauthorized", "authentication required", name, string(entry.tier), start)
					return
				}
				switch entry.tier {
				case RouteTierAdmin, RouteTierSuperAdmin:
					if identity.Permission <= 0 {
						m.writePluginError(w, r, http.StatusForbidden, "plugin.forbidden", "admin permission required", name, string(entry.tier), start)
						return
					}
				}
				if entry.tier == RouteTierSuperAdmin && identity.Permission != 2 {
					m.writePluginError(w, r, http.StatusForbidden, "plugin.forbidden", "super-admin required", name, string(entry.tier), start)
					return
				}
				if r.Method != http.MethodGet && r.Method != http.MethodHead && !csrfOK(r) {
					m.writePluginError(w, r, http.StatusForbidden, "plugin.csrf_failed", "csrf validation failed", name, string(entry.tier), start)
					return
				}
				// The account bridge is tier-agnostic, so a session-tier plugin
				// route can read/write players and textures just like /api/v1/.
				// Apply the same verdict here, before the VM runs; public and
				// bearer never reach this branch.
				if vg := m.verifyGate(); vg != nil && vg(r.Context(), identity.ID) {
					m.writePluginError(w, r, http.StatusForbidden, i18n.MsgEmailVerifyRequired, "email verification required", name, string(entry.tier), start)
					return
				}
			}
		}

		if !m.allowRoute(r, entry, name, identity) {
			_, window, _ := parseRateLimit(entry.spec.RateLimit)
			if window > 0 {
				w.Header().Set("Retry-After", strconv.Itoa(int(window.Seconds())))
			}
			m.writePluginError(w, r, http.StatusTooManyRequests, "plugin.rate_limited", "rate limit exceeded", name, string(entry.tier), start)
			return
		}

		body, readErr := readRouteBody(w, r, entry)
		if readErr != nil {
			m.writePluginError(w, r, http.StatusRequestEntityTooLarge, "plugin.body_too_large", readErr.Error(), name, string(entry.tier), start)
			return
		}

		req := buildRouteRequest(r, subPath, params, identity, body, entry.spec.BodyEncoding, m.cfg.ClientIP)
		code, out := rt.callRoute(r.Context(), entry, req, identity)
		if code < 100 || code > 599 {
			code = http.StatusInternalServerError
		}
		raw, err := json.Marshal(out)
		if err != nil || len(raw) > maxRouteResponse {
			m.writePluginError(w, r, http.StatusInsufficientStorage, "plugin.response_too_large", "plugin response too large", name, string(entry.tier), start)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.WriteHeader(code)
		if r.Method != http.MethodHead {
			_, _ = w.Write(raw)
		}
		m.logAccess(r, name, r.Method, subPath, code, entry.tier, identity, start)
	})
}

func (m *Manager) writePluginError(w http.ResponseWriter, r *http.Request, status int, code, msg, name, tier string, start time.Time) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"error": code, "message": msg, "code": code})
	m.logAccess(r, name, r.Method, r.URL.Path, status, RouteTier(tier), nil, start)
}

// resolveBearer authenticates an Authorization: Bearer credential. The
// resolver is supplied by main and backed by the Yggdrasil token ledger.
func (m *Manager) resolveBearer(r *http.Request) (*SessionIdentity, error) {
	m.mu.RLock()
	resolver := m.cfg.BearerResolver
	m.mu.RUnlock()
	if resolver == nil {
		return nil, &BearerError{Status: http.StatusServiceUnavailable, Code: "auth.unavailable", Message: "bearer authentication is not configured"}
	}
	raw := strings.TrimSpace(r.Header.Get("Authorization"))
	if raw == "" {
		return nil, &BearerError{Status: http.StatusUnauthorized, Code: "auth.required", Message: "bearer token required"}
	}
	scheme, token, ok := strings.Cut(raw, " ")
	token = strings.TrimSpace(token)
	if !ok || !strings.EqualFold(scheme, "Bearer") || token == "" {
		return nil, &BearerError{Status: http.StatusUnauthorized, Code: "auth.invalid_token", Message: "invalid authorization header"}
	}
	if len(token) > 4096 {
		return nil, &BearerError{Status: http.StatusUnauthorized, Code: "auth.invalid_token", Message: "token too long"}
	}
	id, err := resolver.ResolveBearer(r.Context(), token)
	if err != nil {
		var be *BearerError
		if errors.As(err, &be) {
			return nil, be
		}
		return nil, &BearerError{Status: http.StatusUnauthorized, Code: "auth.invalid_token", Message: "invalid or expired token"}
	}
	if id == nil || id.ID == 0 {
		return nil, &BearerError{Status: http.StatusUnauthorized, Code: "auth.invalid_token", Message: "invalid token identity"}
	}
	return id, nil
}

func (m *Manager) writeBearerError(w http.ResponseWriter, r *http.Request, name string, start time.Time, err error) {
	be := &BearerError{Status: http.StatusUnauthorized, Code: "auth.invalid_token", Message: "invalid token"}
	var explicit *BearerError
	if errors.As(err, &explicit) {
		be = explicit
	}
	if be.Status == http.StatusUnauthorized {
		if be.Code == "auth.required" {
			w.Header().Set("WWW-Authenticate", `Bearer realm="skin-nexus"`)
		} else {
			w.Header().Set("WWW-Authenticate", `Bearer realm="skin-nexus", error="invalid_token"`)
		}
	}
	m.writePluginError(w, r, be.Status, be.Code, be.Message, name, string(RouteTierBearer), start)
}

func (m *Manager) logAccess(r *http.Request, name, method, path string, status int, tier RouteTier, id *SessionIdentity, start time.Time) {
	if m.cfg.AccessLog == nil {
		return
	}
	var uid int64
	if id != nil {
		uid = id.ID
	}
	m.cfg.AccessLog(r.Context(), name, method, path, requestIP(r, m.cfg.ClientIP), int64(status), time.Since(start).Milliseconds(), string(tier), uid)
}

func lookupRoute(rt *runtime, method, actual string) (routeEntry, map[string]string, bool, bool) {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	var (
		best      routeEntry
		bestParam map[string]string
		bestScore = -1
		pathHit   bool
	)
	for _, e := range rt.routes {
		params, ok := matchRoute(e.path, actual)
		if !ok {
			continue
		}
		pathHit = true
		if e.method != method {
			continue
		}
		score := staticSegments(e.path)
		if score > bestScore {
			best, bestParam, bestScore = e, params, score
		}
	}
	return best, bestParam, bestScore >= 0, pathHit
}

func allowedMethods(rt *runtime, actual string) string {
	seen := map[string]bool{}
	for _, e := range rt.routes {
		if _, ok := matchRoute(e.path, actual); ok {
			seen[e.method] = true
		}
	}
	out := make([]string, 0, len(seen))
	for _, m := range []string{"GET", "HEAD", "POST", "PUT", "PATCH", "DELETE"} {
		if seen[m] {
			out = append(out, m)
		}
	}
	return strings.Join(out, ", ")
}

func matchRoute(pattern, actual string) (map[string]string, bool) {
	if pattern == actual {
		return map[string]string{}, true
	}
	ps := splitPath(pattern)
	as := splitPath(actual)
	params := map[string]string{}
	for i, seg := range ps {
		if strings.HasPrefix(seg, "{") && strings.HasSuffix(seg, "...}") {
			if i != len(ps)-1 {
				return nil, false
			}
			name := strings.TrimSuffix(strings.TrimPrefix(seg, "{"), "...}")
			if name == "" || i >= len(as) {
				return nil, false
			}
			params[name] = strings.Join(as[i:], "/")
			return params, true
		}
		if strings.HasPrefix(seg, "{") && strings.HasSuffix(seg, "}") {
			if i >= len(as) {
				return nil, false
			}
			params[strings.Trim(seg, "{}")] = as[i]
			continue
		}
		if i >= len(as) || seg != as[i] {
			return nil, false
		}
	}
	return params, len(ps) == len(as)
}

func splitPath(p string) []string {
	p = strings.Trim(p, "/")
	if p == "" {
		return nil
	}
	return strings.Split(p, "/")
}

func staticSegments(p string) int {
	n := 0
	for _, s := range splitPath(p) {
		if !strings.HasPrefix(s, "{") {
			n++
		}
	}
	return n
}

func (m *Manager) resolveSession(r *http.Request) (*SessionIdentity, bool) {
	if m.cfg.SessionResolver == nil {
		return nil, false
	}
	c, err := r.Cookie(m.cfg.SessionCookie)
	if err != nil || c.Value == "" {
		return nil, false
	}
	id, err := m.cfg.SessionResolver.ResolveSession(r.Context(), c.Value)
	if err != nil || id == nil || id.ID <= 0 {
		return nil, false
	}
	return id, true
}

func (m *Manager) allowRoute(r *http.Request, e routeEntry, name string, id *SessionIdentity) bool {
	if e.spec.RateLimit == "" {
		return true
	}
	max, window, ok := parseRateLimit(e.spec.RateLimit)
	if !ok {
		return true
	}
	subject := "ip:" + requestIP(r, m.cfg.ClientIP)
	if id != nil {
		subject = "uid:" + strconv.FormatInt(id.ID, 10)
	}
	return m.limits.allow(name+":"+e.method+" "+e.path+":"+subject, max, window)
}

func readRouteBody(w http.ResponseWriter, r *http.Request, e routeEntry) (any, error) {
	if r.Method == http.MethodGet || r.Method == http.MethodHead || r.Body == nil {
		return nil, nil
	}
	limit := e.spec.BodyLimit
	if limit <= 0 {
		limit = defaultBodyLimit
	}
	maxLimit := maxBodyLimit
	if e.spec.BodyEncoding == "base64" {
		maxLimit = maxBinaryBodyLimit
	}
	if limit > maxLimit {
		limit = maxLimit
	}
	r.Body = http.MaxBytesReader(w, r.Body, int64(limit))
	raw, err := io.ReadAll(r.Body)
	if err != nil {
		return nil, fmt.Errorf("body exceeds %d bytes", limit)
	}
	if len(raw) > limit {
		return nil, fmt.Errorf("body exceeds %d bytes", limit)
	}
	if len(raw) == 0 {
		return nil, nil
	}
	ct := strings.ToLower(r.Header.Get("Content-Type"))
	if strings.Contains(ct, "application/json") {
		var v any
		dec := json.NewDecoder(strings.NewReader(string(raw)))
		dec.UseNumber()
		if err := dec.Decode(&v); err != nil {
			return nil, fmt.Errorf("invalid JSON body")
		}
		return normalizeJSONNumber(v), nil
	}
	if e.spec.BodyEncoding == "base64" {
		return base64.StdEncoding.EncodeToString(raw), nil
	}
	return string(raw), nil
}

func buildRouteRequest(r *http.Request, subPath string, params map[string]string, id *SessionIdentity, body any, bodyEncoding string, clientIP func(*http.Request) string) map[string]interface{} {
	query := map[string]interface{}{}
	for k, vs := range r.URL.Query() {
		if len(vs) > 0 {
			query[k] = vs[0]
		}
	}
	req := map[string]interface{}{
		"method": r.Method,
		"path":   subPath,
		"params": params,
		"query":  query,
		"ip":     requestIP(r, clientIP),
		"headers": map[string]string{
			"content-type":    r.Header.Get("Content-Type"),
			"accept-language": r.Header.Get("Accept-Language"),
			"user-agent":      r.Header.Get("User-Agent"),
			"origin":          r.Header.Get("Origin"),
			"x-request-id":    r.Header.Get("X-Request-ID"),
		},
	}
	if body != nil {
		req["body"] = body
	}
	if bodyEncoding != "" {
		req["bodyEncoding"] = bodyEncoding
	}
	if id != nil {
		req["user"] = map[string]interface{}{
			"id": id.ID, "nickname": id.Nickname, "permission": id.Permission,
			"admin": id.Permission > 0,
		}
	}
	return req
}

func requestIP(r *http.Request, clientIP func(*http.Request) string) string {
	if clientIP != nil {
		if ip := clientIP(r); ip != "" {
			return ip
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err == nil {
		return host
	}
	return r.RemoteAddr
}

func (m *Manager) handleCSRF(w http.ResponseWriter, r *http.Request, name string, start time.Time) {
	if r.Method != http.MethodGet {
		m.writePluginError(w, r, http.StatusMethodNotAllowed, "plugin.method_not_allowed", "method not allowed", name, "", start)
		return
	}
	token := ""
	if c, err := r.Cookie(csrfCookieName); err == nil && isCSRFToken(c.Value) {
		// Re-issue the value the browser already holds. There is one cookie for
		// the whole site (Path=/) but every plugin page keeps its own copy of the
		// token, so a fresh value per request makes the last page loaded win and
		// silently breaks the writes of every other mounted page. The pairing
		// cookie-vs-header is the protection; rotating it per request adds nothing.
		token = c.Value
	} else {
		buf := make([]byte, 32)
		if _, err := rand.Read(buf); err != nil {
			m.writePluginError(w, r, http.StatusInternalServerError, "plugin.internal", "cannot issue csrf token", name, "", start)
			return
		}
		token = hex.EncodeToString(buf)
	}
	http.SetCookie(w, &http.Cookie{
		Name: csrfCookieName, Value: token, Path: "/", HttpOnly: true,
		SameSite: http.SameSiteLaxMode, Secure: isHTTPS(r), MaxAge: int((12 * time.Hour).Seconds()),
	})
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{"token": token})
	m.logAccess(r, name, r.Method, "/_csrf", http.StatusOK, RouteTierPublic, nil, start)
}

// isCSRFToken accepts only a value this handler issued: a 64-character lowercase
// hex string. Anything else in the jar (an old format, a hand-written cookie on
// a subdomain that shares the name) is replaced rather than adopted.
func isCSRFToken(v string) bool {
	if len(v) != 64 {
		return false
	}
	for _, ch := range v {
		if !(ch >= '0' && ch <= '9' || ch >= 'a' && ch <= 'f') {
			return false
		}
	}
	return true
}

func csrfOK(r *http.Request) bool {
	if origin := r.Header.Get("Origin"); origin != "" && origin != "null" {
		u, err := url.Parse(origin)
		if err != nil || !strings.EqualFold(u.Host, r.Host) {
			return false
		}
	}
	c, err := r.Cookie(csrfCookieName)
	if err != nil || c.Value == "" {
		return false
	}
	h := r.Header.Get(csrfHeaderName)
	if h == "" || len(h) != len(c.Value) {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(h), []byte(c.Value)) == 1
}

func isHTTPS(r *http.Request) bool {
	return r.TLS != nil || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https")
}
