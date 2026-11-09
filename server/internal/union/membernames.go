package union

// Member site display names, read on the server.
//
// Why this exists at all: the hub's /serverlist returns {code,name,bs_root} with
// `name` empty on every entry (measured on the live hub with a member key,
// 2026-11-09), and the legacy plugin filled the column in the visitor's browser
// by fetching each peer's own {bs_root}/api/yggdrasil (servers.twig:22-33). Our
// pages set connect-src 'self', so a browser-side version of that is blocked
// before it leaves — the read has to happen here.
//
// Which makes this an outbound surface aimed at fifty-some third-party hosts
// that anybody in the alliance can edit rows for. It therefore does not use the
// hub client (a plain http.Client with a per-call timeout and no address
// policy): every read goes through the guarded client the plugin and theme
// markets already use — globally routable addresses only, IP literals refused,
// no redirects, ports 80/443, capped body, per-host rate limit. On top of that
// this file never fetches a caller-supplied URL: it takes the scheme and host
// out of bs_root and rebuilds the request against one fixed path, so a bs_root
// carrying a path, query, fragment or credentials cannot turn this into a
// reader for them.
//
// Reads are background work. A page open serves whatever is already known and
// schedules what is missing; it never waits on fifty peers.

import (
	"context"
	"encoding/json"
	"net"
	"net/url"
	"strings"
	"sync"
	"time"
	"unicode"

	"skin-nexus/server/internal/outbound"
)

// MemberNameFetcher is the one method the resolver needs from a guarded GET
// client. *outbound.Scoped satisfies it; tests substitute a recorder.
type MemberNameFetcher interface {
	Fetch(ctx context.Context, raw string) (outbound.FetchResult, error)
}

// MemberNamesOptions bounds one resolver. Zero values take the defaults below.
type MemberNamesOptions struct {
	TTL          time.Duration // how long a good name is kept
	FailTTL      time.Duration // how long a refusal is remembered before retrying
	Workers      int           // peers read at once
	SweepTimeout time.Duration // ceiling for one whole sweep
	Now          func() time.Time
}

const (
	defaultMemberNameTTL     = 24 * time.Hour
	defaultMemberNameFailTTL = 30 * time.Minute
	defaultMemberNameWorkers = 6
	defaultMemberNameSweep   = 90 * time.Second

	// memberNameMaxRunes caps third-party text that ends up inside our page. It
	// is rendered escaped, but a peer returning a paragraph should not become a
	// paragraph in someone's server list.
	memberNameMaxRunes = 120
)

// memberNamePath is the only path this resolver will ever request.
const memberNamePath = "/api/yggdrasil"

type nameEntry struct {
	name string
	at   time.Time
	ok   bool
}

// target is one peer to ask: its cache key and the single URL allowed for it.
type target struct{ key, url string }

// MemberNames caches peer names by host and refreshes misses in the background.
// The zero value is not usable; call NewMemberNames. Safe for concurrent use.
type MemberNames struct {
	fetch   MemberNameFetcher
	enabled func() bool // nil = always on
	ttl     time.Duration
	failTTL time.Duration
	workers int
	sweep   time.Duration
	now     func() time.Time

	mu      sync.Mutex
	entries map[string]nameEntry
	running bool
}

func NewMemberNames(fetch MemberNameFetcher, enabled func() bool, opt MemberNamesOptions) *MemberNames {
	if opt.TTL <= 0 {
		opt.TTL = defaultMemberNameTTL
	}
	if opt.FailTTL <= 0 {
		opt.FailTTL = defaultMemberNameFailTTL
	}
	if opt.Workers <= 0 {
		opt.Workers = defaultMemberNameWorkers
	}
	if opt.SweepTimeout <= 0 {
		opt.SweepTimeout = defaultMemberNameSweep
	}
	if opt.Now == nil {
		opt.Now = time.Now
	}
	return &MemberNames{
		fetch:   fetch,
		enabled: enabled,
		ttl:     opt.TTL,
		failTTL: opt.FailTTL,
		workers: opt.Workers,
		sweep:   opt.SweepTimeout,
		now:     opt.Now,
		entries: map[string]nameEntry{},
	}
}

func (m *MemberNames) on() bool {
	return m != nil && m.fetch != nil && (m.enabled == nil || m.enabled())
}

// Fill returns a copy of the hub's list with peer names added where they are
// known, and starts a background read for the ones that are not. Nothing here
// waits: the first page open after a boot may still show addresses, and the next
// one shows names.
//
// Turning the resolver off also stops serving names it already learned — the
// operator's decision is that this station does not keep third-party text.
func (m *MemberNames) Fill(list []ServerEntry) []ServerEntry {
	if m == nil || len(list) == 0 {
		return list
	}
	out := make([]ServerEntry, len(list))
	copy(out, list)
	if !m.on() {
		return out
	}

	var need []target
	seen := map[string]bool{}

	for i := range out {
		if out[i].Name != "" {
			continue // the hub started supplying one; that answer wins
		}
		key, raw, ok := memberNameTarget(out[i].BSRoot)
		if !ok {
			continue // not a host we are willing to ask (see memberNameTarget)
		}
		if name, usable := m.lookup(key); usable {
			if name != "" {
				out[i].Name = name
			}
			continue // known good, or refused inside its retry window
		}
		if !seen[key] {
			seen[key] = true
			need = append(need, target{key, raw})
		}
	}
	m.schedule(need)
	return out
}

// lookup reports the cached answer for a host. usable=false means "ask".
func (m *MemberNames) lookup(key string) (name string, usable bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	e, ok := m.entries[key]
	if !ok {
		return "", false
	}
	window := m.ttl
	if !e.ok {
		window = m.failTTL
	}
	if m.now().Sub(e.at) >= window {
		return "", false
	}
	return e.name, true
}

func (m *MemberNames) store(key, name string, ok bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.entries[key] = nameEntry{name: name, at: m.now(), ok: ok}
}

// schedule starts one sweep at a time. Skipping is deliberate: a page open
// during a running sweep adds nothing the sweep will not already cover, because
// the list it reads came from the same hub document.
func (m *MemberNames) schedule(need []target) {
	if m == nil || len(need) == 0 || !m.on() {
		return
	}
	m.mu.Lock()
	if m.running {
		m.mu.Unlock()
		return
	}
	m.running = true
	m.mu.Unlock()

	go m.runSweep(need)
}

func (m *MemberNames) runSweep(need []target) {
	defer func() {
		m.mu.Lock()
		m.running = false
		m.mu.Unlock()
	}()

	ctx, cancel := context.WithTimeout(context.Background(), m.sweep)
	defer cancel()

	jobs := make(chan target)
	var wg sync.WaitGroup
	for i := 0; i < m.workers && i < len(need); i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for t := range jobs {
				m.readOne(ctx, t)
			}
		}()
	}
	for _, t := range need {
		select {
		case jobs <- t:
		case <-ctx.Done():
			// The sweep ran out of its ceiling. The unfinished peers simply have
			// no entry, so the next page open asks for them again.
			close(jobs)
			wg.Wait()
			return
		}
	}
	close(jobs)
	wg.Wait()
}

func (m *MemberNames) readOne(ctx context.Context, t target) {
	res, err := m.fetch.Fetch(ctx, t.url)
	if err != nil || res.Status < 200 || res.Status > 299 {
		m.store(t.key, "", false) // refused: retried on the shorter clock
		return
	}
	name := parseMemberName(res.Body)
	if name == "" {
		m.store(t.key, "", false)
		return
	}
	m.store(t.key, name, true)
}

// idle reports whether a sweep is in flight (tests only).
func (m *MemberNames) idle() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return !m.running
}

// memberNameTarget turns a peer's bs_root into the one URL this resolver may
// fetch, plus its cache key. Anything it refuses is a peer whose name we simply
// never show, so the row keeps falling back to its address.
//
// Refused on purpose: non-http(s) schemes, credentials in the URL, and IP
// literals. The last one is a display loss (a member reachable only by raw
// address shows its address, as it does today) bought deliberately: this is a
// machine fetching on behalf of whoever edits the alliance list, and a numeric
// destination is where that reaches inward. The guarded client refuses it again
// at dial time — including a hostname that resolves to a private address — and
// that is the real block; refusing it here keeps the intent visible and testable
// instead of relying on a downstream error.
func memberNameTarget(bsRoot string) (key, raw string, ok bool) {
	u, err := url.Parse(strings.TrimSpace(bsRoot))
	if err != nil || u == nil {
		return "", "", false
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return "", "", false
	}
	if u.User != nil {
		return "", "", false
	}
	host := strings.ToLower(u.Hostname())
	if host == "" || host == "." || net.ParseIP(host) != nil {
		return "", "", false
	}
	authority := host
	if p := u.Port(); p != "" {
		authority = host + ":" + p
	}
	return authority, u.Scheme + "://" + authority + memberNamePath, true
}

// parseMemberName reads meta.serverName out of a peer's discovery document.
func parseMemberName(body []byte) string {
	var doc struct {
		Meta struct {
			ServerName string `json:"serverName"`
		} `json:"meta"`
	}
	if err := json.Unmarshal(body, &doc); err != nil {
		return ""
	}
	return sanitizePeerName(doc.Meta.ServerName)
}

// sanitizePeerName flattens peer text for a table cell: no control characters,
// no runs of whitespace, bounded length. Escaping is the template's job; this is
// about not letting one peer's return value dictate the shape of the page.
func sanitizePeerName(s string) string {
	var b strings.Builder
	space := false
	count := 0
	for _, r := range s {
		if r == '\n' || r == '\r' || r == '\t' || unicode.IsControl(r) {
			r = ' '
		}
		if r == ' ' {
			space = b.Len() > 0
			continue
		}
		if space {
			b.WriteByte(' ')
			space = false
			count++
		}
		b.WriteRune(r)
		count++
		if count >= memberNameMaxRunes {
			break
		}
	}
	return strings.TrimSpace(b.String())
}

// hostOf extracts the host of a URL for test assertions.
func hostOf(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	return strings.ToLower(u.Hostname())
}
