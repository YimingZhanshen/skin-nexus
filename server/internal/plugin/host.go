package plugin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"skin-nexus/server/internal/jobs"
	"skin-nexus/server/internal/signedpkg"
)

// StateStore is the minimal options K/V surface the host needs to persist
// enable/disable state. *store.Mysql satisfies it.
type StateStore interface {
	GetOption(ctx context.Context, k string) (string, bool, error)
	SetOption(ctx context.Context, k, v string) error
}

// RuntimeConfig tunes the host. Zero durations fall back to safe defaults.
type RuntimeConfig struct {
	Dir         string
	HostVersion string
	State       StateStore // v1 fallback; Store wins when set
	Store       PlatformStore
	Audit       func(ctx context.Context, action string, detail map[string]any)

	SessionResolver SessionResolver
	SessionCookie   string
	BearerResolver  BearerResolver // Authorization: Bearer → identity (machine clients)
	Account         AccountService // host-mediated player/texture bridge
	SecretBox       *SecretBox
	ClientIP        func(*http.Request) string
	AccessLog       func(ctx context.Context, pluginName, method, path, ip string, status, ms int64, tier string, uid int64)

	StateDir  string            // default <Dir>/.state
	Keyring   signedpkg.Keyring // required for signed bundle install
	MarketURL string            // signed static registry URL (empty = market off)
	UploadMax int64             // admin bundle upload cap (default 64 MiB)
	Jobs      *jobs.Store       // durable plugin job queue (nil = jobs disabled)

	HookTimeout  time.Duration // default 100ms
	AuthTimeout  time.Duration // default 50ms
	RouteTimeout time.Duration // default 1s
	LoadTimeout  time.Duration // default 2s (entrypoint execution)
}

func (c *RuntimeConfig) withDefaults() {
	if c.HookTimeout <= 0 {
		c.HookTimeout = 100 * time.Millisecond
	}
	if c.AuthTimeout <= 0 {
		c.AuthTimeout = 50 * time.Millisecond
	}
	if c.RouteTimeout <= 0 {
		c.RouteTimeout = time.Second
	}
	if c.LoadTimeout <= 0 {
		c.LoadTimeout = 2 * time.Second
	}
	if c.UploadMax <= 0 {
		c.UploadMax = 64 << 20
	}
	if c.SessionCookie == "" {
		c.SessionCookie = "skin_session"
	}
	if c.StateDir == "" && c.Dir != "" {
		c.StateDir = filepath.Join(c.Dir, ".state")
	}
}

func (c *RuntimeConfig) timeouts() timeouts {
	return timeouts{hook: c.HookTimeout, auth: c.AuthTimeout, route: c.RouteTimeout, load: c.LoadTimeout}
}

// goRoute is a host-level (Go) plugin route registered via Manager.Bus.
type goRoute struct {
	method  string
	path    string
	handler func(ctx context.Context, req map[string]interface{}) (int, interface{})
	timeout time.Duration
}

type instance struct {
	info         Info
	rt           *runtime
	jobRt        *runtime
	enabled      bool
	state        string // loaded | disabled | error
	health       string // ok | degraded | error | disabled
	lastErr      string
	loadedAt     time.Time
	installedAt  time.Time
	updatedAt    time.Time
	source       string // market | upload | manual
	trust        string // verified | unknown-key | unsigned | invalid | ""
	signer       string // bundle key id
	checksum     string // zip SHA-256
	configSchema *ConfigSchema
	config       *configState
	jobSem       chan struct{} // one job step at a time per plugin
}

// Manager scans a plugin directory, owns per-plugin runtimes and dispatches
// hooks. It implements the frozen Host interface.
type Manager struct {
	cfg RuntimeConfig

	mu          sync.RWMutex
	instances   map[string]*instance
	order       []string
	extra       hookSet
	extraRoutes []goRoute

	events    chan eventItem
	done      chan struct{}
	closeOnce sync.Once
	limits    *routeLimits
	hostInfo  atomic.Pointer[HostInfo]

	marketMu      sync.Mutex
	marketCache   *MarketRegistry
	marketFetched time.Time
}

type eventItem struct {
	kind    int // eventUpload | eventLifecycle | eventAuth
	payload any
}

// event kinds routed through the bounded queue.
const (
	eventUpload = iota
	eventLifecycle
	eventAuth
	eventGeneric
)

// ErrPluginNotFound is returned by Enable/Disable/Reload for unknown names.
var ErrPluginNotFound = errors.New("plugin: not found")

// NewHost builds a Manager and starts its event worker.
func NewHost(cfg RuntimeConfig) *Manager {
	cfg.withDefaults()
	m := &Manager{
		cfg:       cfg,
		instances: map[string]*instance{},
		events:    make(chan eventItem, 256),
		done:      make(chan struct{}),
		limits:    newRouteLimits(),
	}
	go m.eventWorker()
	if cfg.Jobs != nil {
		go m.jobWorker("worker-a")
		go m.jobWorker("worker-b")
	}
	return m
}

// Bus exposes host-level hook registration (tests / first-party integrations).
func (m *Manager) Bus() HookBus { return goBus{m} }

// SetBearerResolver wires machine-client authentication after the Yggdrasil
// service exists (main constructs it later than the plugin host). It must be
// called before the HTTP server starts serving requests.
func (m *Manager) SetBearerResolver(r BearerResolver) {
	m.mu.Lock()
	m.cfg.BearerResolver = r
	m.mu.Unlock()
}

// SetAccountService wires the player/texture bridge after the web backend
// exists (main constructs it later than the plugin host).
func (m *Manager) SetAccountService(s AccountService) {
	m.mu.Lock()
	m.cfg.Account = s
	m.mu.Unlock()
}

func (m *Manager) accountService() AccountService {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.cfg.Account
}

// PluginSigner returns the bundle signer id recorded for a plugin, when known.
func (m *Manager) PluginSigner(name string) string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if inst := m.instances[name]; inst != nil {
		return inst.signer
	}
	return ""
}

func (m *Manager) logSink(line string) { log.Print(line) }

func (m *Manager) audit(ctx context.Context, action string, detail map[string]any) {
	if m.cfg.Audit != nil {
		m.cfg.Audit(ctx, action, detail)
	}
}

func optionKey(name string) string { return "plugin_enabled:" + name }

// Load scans the plugin root. Invalid manifests are recorded as errored
// entries; only previously-enabled plugins start. A missing root is not an
// error so fresh deployments boot cleanly. Script execution happens outside
// Manager.mu so one slow entrypoint cannot block List/Handler.
func (m *Manager) Load(dir string) error {
	m.cfg.Dir = dir
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	ctx := context.Background()
	count := 0
	for _, e := range entries {
		if !e.IsDir() || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		if count >= maxPlugins {
			log.Printf("plugin: ignoring %s (max %d plugins)", e.Name(), maxPlugins)
			continue
		}
		count++
		m.loadOne(ctx, e.Name())
	}
	m.mu.Lock()
	sort.Strings(m.order)
	m.refreshHostInfoLocked()
	m.mu.Unlock()
	return nil
}

func (m *Manager) loadOne(ctx context.Context, name string) {
	dir := filepath.Join(m.cfg.Dir, name)
	info, err := loadManifest(dir, name)
	if err != nil {
		inst := &instance{info: Info{Manifest: Manifest{Name: name}}, state: "error", health: "error", lastErr: err.Error(), jobSem: make(chan struct{}, 1)}
		m.insertInstance(name, inst)
		m.audit(ctx, "plugin_load_failed", map[string]any{"plugin": name, "error": err.Error()})
		m.persistMeta(ctx, inst)
		return
	}
	inst, prepErr := m.prepareInstance(ctx, name, info)
	if prepErr == nil && m.enabledOption(ctx, name) {
		if rerr := m.startInstance(ctx, inst); rerr != nil {
			inst.state = "error"
			inst.health = "error"
			inst.lastErr = rerr.Error()
			m.audit(ctx, "plugin_load_failed", map[string]any{"plugin": name, "version": info.Version, "error": rerr.Error()})
		} else {
			m.audit(ctx, "plugin_loaded", map[string]any{"plugin": name, "version": info.Version, "permissions": info.Permissions})
		}
	}
	if prepErr != nil {
		m.audit(ctx, "plugin_load_failed", map[string]any{"plugin": name, "version": info.Version, "error": prepErr.Error()})
	}
	m.insertInstance(name, inst)
	m.persistMeta(ctx, inst)
}

func (m *Manager) insertInstance(name string, inst *instance) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if old, ok := m.instances[name]; ok {
		if inst.installedAt.IsZero() {
			inst.installedAt = old.installedAt
		}
		if old.rt != nil && old.rt != inst.rt {
			old.rt.close()
		}
		m.instances[name] = inst
		m.refreshHostInfoLocked()
		return
	}
	if inst.installedAt.IsZero() {
		inst.installedAt = time.Now()
	}
	inst.updatedAt = time.Now()
	m.instances[name] = inst
	m.order = append(m.order, name)
	m.refreshHostInfoLocked()
}

// prepareInstance parses the manifest config (and minServer) without starting
// a VM. A config/minServer failure still leaves the schema on the instance so
// the admin can fix config and retry.
func (m *Manager) prepareInstance(ctx context.Context, name string, info Info) (*instance, error) {
	inst := &instance{info: info, state: "disabled", health: "disabled", config: newConfigState(nil), jobSem: make(chan struct{}, 1)}
	if m.cfg.HostVersion != "" {
		if err := info.CheckMinServer(m.cfg.HostVersion); err != nil {
			inst.state = "error"
			inst.health = "error"
			inst.lastErr = err.Error()
			return inst, err
		}
	}
	if st := m.platformStore(); st != nil {
		if meta, ok, err := st.GetPluginMeta(ctx, name); err == nil && ok {
			inst.source, inst.trust, inst.signer, inst.checksum = meta.Source, meta.Trust, meta.Signer, meta.Checksum
			if !meta.InstalledAt.IsZero() {
				inst.installedAt = meta.InstalledAt
			}
		}
	}
	if info.Config != nil {
		schema, cfg, err := m.loadConfig(ctx, name, info)
		inst.configSchema = schema
		if cfg != nil {
			inst.config = cfg
		}
		if err != nil {
			inst.state = "error"
			inst.health = "error"
			inst.lastErr = err.Error()
			return inst, err
		}
	}
	return inst, nil
}

func (m *Manager) startInstance(ctx context.Context, inst *instance) error {
	st := m.platformStore()
	var storeSvc *storeService
	if st != nil && (inst.info.hasPermission(PermStoreKV) || inst.info.hasPermission(PermStoreDocs)) {
		storeSvc = newStoreService(st, inst.info.Name, inst.info.Store, inst.info.hasPermission(PermStoreKV), inst.info.hasPermission(PermStoreDocs))
	}
	rt, err := newRuntime(inst.info, m.cfg.timeouts(), m.logSink, inst.config, storeSvc, m.HostInfo, m.accountService, m.cfg.Jobs)
	if err != nil {
		return err
	}
	if inst.info.Store != nil && inst.info.Store.SchemaVersion > 0 {
		if st == nil {
			rt.close()
			return ErrPluginNoStore
		}
		latest, err := st.PluginMigrationLatest(ctx, inst.info.Name)
		if err != nil {
			rt.close()
			return err
		}
		to := inst.info.Store.SchemaVersion
		if latest > to {
			rt.close()
			return fmt.Errorf("plugin schema version %d is newer than manifest %d", latest, to)
		}
		if latest < to {
			if err := rt.runMigrations(ctx, latest, to); err != nil {
				rt.close()
				return fmt.Errorf("plugin migration %d->%d: %w", latest, to, err)
			}
			if err := st.RecordPluginMigration(ctx, inst.info.Name, to); err != nil {
				rt.close()
				return err
			}
		}
	}
	inst.rt = rt
	var jobRt *runtime
	if m.cfg.Jobs != nil && inst.info.hasPermission(PermScheduleJobs) && len(inst.info.Jobs) > 0 {
		jobRt, err = newRuntime(inst.info, m.cfg.timeouts(), m.logSink, inst.config, storeSvc, m.HostInfo, m.accountService, m.cfg.Jobs)
		if err != nil {
			rt.close()
			return err
		}
	}
	inst.jobRt = jobRt
	inst.enabled = true
	inst.state = "loaded"
	inst.health = "ok"
	inst.lastErr = ""
	inst.loadedAt = time.Now()
	inst.updatedAt = time.Now()
	return nil
}

func (m *Manager) persistMeta(ctx context.Context, inst *instance) {
	st := m.platformStore()
	if st == nil || inst.info.Name == "" {
		return
	}
	manifest, _ := json.Marshal(inst.info.Manifest)
	var cfg []byte
	if inst.config != nil {
		cfg, _ = json.Marshal(inst.config.all())
	}
	state := inst.state
	if inst.enabled {
		state = "enabled"
	}
	source := inst.source
	if source == "" {
		source = "manual"
	}
	if err := st.UpsertPluginMeta(ctx, PluginMetaRecord{
		Name: inst.info.Name, Version: inst.info.Version, HookAPI: inst.info.HookAPI,
		Runtime: inst.info.Runtime, Manifest: manifest, Config: cfg,
		SchemaVersion: inst.info.configSchemaVersion(), Source: source,
		Trust: inst.trust, Signer: inst.signer, Checksum: inst.checksum,
		State: state, LastError: inst.lastErr, InstalledAt: inst.installedAt, UpdatedAt: inst.updatedAt,
	}); err != nil {
		log.Printf("plugin: persist meta %s: %v", inst.info.Name, err)
	}
}

// setProvenance records how the current version arrived (market/upload/manual)
// and its signature trust level, then mirrors it to plugin_meta.
func (m *Manager) setProvenance(ctx context.Context, name, source, trust, signer, checksum string) {
	m.mu.Lock()
	inst := m.instances[name]
	if inst != nil {
		inst.source, inst.trust, inst.signer, inst.checksum = source, trust, signer, checksum
	}
	m.mu.Unlock()
	if inst != nil {
		m.persistMeta(ctx, inst)
	}
}

// Enable validates, persists and starts a plugin.
func (m *Manager) Enable(ctx context.Context, name string) error {
	m.mu.RLock()
	inst, ok := m.instances[name]
	if !ok {
		m.mu.RUnlock()
		return ErrPluginNotFound
	}
	if inst.enabled {
		m.mu.RUnlock()
		return nil
	}
	m.mu.RUnlock()
	info, err := loadManifest(filepath.Join(m.cfg.Dir, name), name)
	if err != nil {
		m.setInstanceState(name, "error", "error", err.Error())
		return err
	}
	next, prepErr := m.prepareInstance(ctx, name, info)
	if prepErr != nil {
		m.setInstanceState(name, "error", "error", prepErr.Error())
		m.persistMeta(ctx, next)
		return prepErr
	}
	if err := m.startInstance(ctx, next); err != nil {
		m.setInstanceState(name, "error", "error", err.Error())
		m.persistMeta(ctx, next)
		return err
	}
	if err := m.setEnabledOption(ctx, name, true); err != nil {
		next.rt.close()
		return err
	}
	m.replaceInstance(name, next)
	if m.cfg.Jobs != nil {
		_ = m.cfg.Jobs.ResumePausedByPlugin(context.Background(), name)
	}
	m.audit(ctx, "plugin_enable", map[string]any{"plugin": name, "version": info.Version, "permissions": info.Permissions})
	m.persistMeta(ctx, next)
	return nil
}

// Disable removes a plugin from dispatch and closes its VM.
func (m *Manager) Disable(ctx context.Context, name string) error {
	m.mu.Lock()
	inst, ok := m.instances[name]
	if !ok {
		m.mu.Unlock()
		return ErrPluginNotFound
	}
	if !inst.enabled {
		m.mu.Unlock()
		return nil
	}
	if err := m.setEnabledOption(ctx, name, false); err != nil {
		m.mu.Unlock()
		return err
	}
	rt := inst.rt
	inst.rt = nil
	jobRt := inst.jobRt
	inst.jobRt = nil
	inst.enabled = false
	inst.state = "disabled"
	inst.health = "disabled"
	inst.updatedAt = time.Now()
	m.refreshHostInfoLocked()
	m.mu.Unlock()
	if jobRt != nil {
		jobRt.close()
	}
	if m.cfg.Jobs != nil {
		_ = m.cfg.Jobs.PausePlugin(context.Background(), name)
	}
	if rt != nil {
		rt.close()
	}
	m.audit(ctx, "plugin_disable", map[string]any{"plugin": name, "version": inst.info.Version})
	m.persistMeta(ctx, inst)
	return nil
}

// Reload re-reads a plugin's manifest and restarts its VM in place. The new
// VM is built before the old one is swapped out; a failed reload keeps the old
// runtime alive and marks the instance degraded.
func (m *Manager) Reload(ctx context.Context, name string) error {
	m.mu.RLock()
	old, ok := m.instances[name]
	if !ok {
		m.mu.RUnlock()
		return ErrPluginNotFound
	}
	wasEnabled := old.enabled
	m.mu.RUnlock()
	info, err := loadManifest(filepath.Join(m.cfg.Dir, name), name)
	if err != nil {
		m.markDegraded(name, err.Error())
		return err
	}
	next, prepErr := m.prepareInstance(ctx, name, info)
	if prepErr != nil {
		m.markDegraded(name, prepErr.Error())
		m.persistMeta(ctx, next)
		return prepErr
	}
	if wasEnabled {
		if err := m.startInstance(ctx, next); err != nil {
			m.markDegraded(name, err.Error())
			m.persistMeta(ctx, next)
			return err
		}
	}
	if !wasEnabled {
		next.state = "disabled"
		next.health = "disabled"
		next.enabled = false
	}
	m.replaceInstance(name, next)
	m.audit(ctx, "plugin_reload", map[string]any{"plugin": name, "version": info.Version})
	m.persistMeta(ctx, next)
	return nil
}

func (m *Manager) setInstanceState(name, state, health, lastErr string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if inst, ok := m.instances[name]; ok {
		inst.state = state
		inst.health = health
		inst.lastErr = lastErr
		inst.updatedAt = time.Now()
	}
}

func (m *Manager) markDegraded(name, lastErr string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if inst, ok := m.instances[name]; ok {
		inst.health = "degraded"
		inst.lastErr = lastErr
		inst.updatedAt = time.Now()
	}
}

func (m *Manager) replaceInstance(name string, next *instance) {
	m.mu.Lock()
	old := m.instances[name]
	if old != nil {
		next.installedAt = old.installedAt
	} else {
		next.installedAt = time.Now()
	}
	next.updatedAt = time.Now()
	m.instances[name] = next
	m.refreshHostInfoLocked()
	m.mu.Unlock()
	if old != nil && old.rt != nil && old.rt != next.rt {
		old.rt.close()
	}
	if old != nil && old.jobRt != nil && old.jobRt != next.jobRt {
		old.jobRt.close()
	}
}

// Close stops the event worker and releases every VM.
func (m *Manager) Close() error {
	m.closeOnce.Do(func() {
		close(m.done)
		if m.limits != nil {
			m.limits.close()
		}
		m.mu.Lock()
		defer m.mu.Unlock()
		for _, inst := range m.instances {
			if inst.rt != nil {
				inst.rt.close()
				inst.rt = nil
			}
			if inst.jobRt != nil {
				inst.jobRt.close()
				inst.jobRt = nil
			}
		}
	})
	return nil
}

// eventWorker drains the bounded event queue off the request path.
func (m *Manager) eventWorker() {
	for {
		select {
		case <-m.done:
			return
		case item := <-m.events:
			m.dispatchEvent(item)
		}
	}
}

func (m *Manager) emit(kind int, payload any) {
	select {
	case m.events <- eventItem{kind, payload}:
	default:
		// queue saturated: drop rather than block the request path
	}
}

func (m *Manager) dispatchEvent(item eventItem) {
	ctx := context.Background()
	m.mu.RLock()
	switch item.kind {
	case eventUpload:
		e := item.payload.(TextureUploadedEvent)
		for _, h := range m.extra.upload {
			h := h
			runGuarded(ctx, h.timeout, func(context.Context) error { return h.fn(ctx, e) })
		}
	case eventLifecycle:
		e := item.payload.(UserLifecycleEvent)
		for _, h := range m.extra.lifecycle {
			h := h
			runGuarded(ctx, h.timeout, func(context.Context) error { return h.fn(ctx, e) })
		}
	case eventAuth:
		e := item.payload.(AuthPhaseEvent)
		for _, h := range m.extra.auth {
			h := h
			runGuarded(ctx, h.timeout, func(context.Context) error { return h.fn(ctx, e) })
		}
	case eventGeneric:
		// Generic events are plugin-to-plugin (no host-level Go subscribers yet).
	}

	type target struct {
		fn func(context.Context) error
	}
	var targets []target
	for _, name := range m.order {
		inst := m.instances[name]
		if !inst.enabled || inst.rt == nil {
			continue
		}
		rt := inst.rt
		switch item.kind {
		case eventUpload:
			e := item.payload.(TextureUploadedEvent)
			for _, h := range rt.hooks.upload {
				h := h
				targets = append(targets, target{func(context.Context) error { return h.fn(ctx, e) }})
			}
		case eventLifecycle:
			e := item.payload.(UserLifecycleEvent)
			for _, h := range rt.hooks.lifecycle {
				h := h
				targets = append(targets, target{func(context.Context) error { return h.fn(ctx, e) }})
			}
		case eventAuth:
			e := item.payload.(AuthPhaseEvent)
			for _, h := range rt.hooks.auth {
				h := h
				targets = append(targets, target{func(context.Context) error { return h.fn(ctx, e) }})
			}
		case eventGeneric:
			e := item.payload.(EventPayload)
			for _, h := range rt.hooks.events[e.Kind] {
				h := h
				targets = append(targets, target{func(context.Context) error { return h.fn(ctx, e) }})
			}
		}
	}
	m.mu.RUnlock()

	for _, t := range targets {
		_ = t.fn(ctx) // JS hooks self-timeout inside the VM
	}
}

// runGuarded bounds a host-level (Go) hook so a misbehaving callback cannot
// stall the dispatcher.
func runGuarded(ctx context.Context, d time.Duration, fn func(context.Context) error) {
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = fn(ctx)
	}()
	select {
	case <-done:
	case <-time.After(d):
	}
}

// ProfileFilter chains host-level then plugin profile filters (name order).
// Failures are fail-open: the payload simply keeps its prior value.
func (m *Manager) ProfileFilter(ctx context.Context, p *UserProfileFilter) {
	m.mu.RLock()
	extra := append([]timed[profileHookFn](nil), m.extra.profile...)
	var pluginFns []profileHookFn
	for _, name := range m.order {
		inst := m.instances[name]
		if inst.enabled && inst.rt != nil {
			for _, h := range inst.rt.hooks.profile {
				pluginFns = append(pluginFns, h.fn)
			}
		}
	}
	m.mu.RUnlock()

	for _, h := range extra {
		runGuarded(ctx, h.timeout, func(context.Context) error { return h.fn(ctx, p) })
	}
	for _, fn := range pluginFns {
		_ = fn(ctx, p)
	}
}

// TextureURL applies URL filters and returns the (possibly rewritten) URL.
func (m *Manager) TextureURL(ctx context.Context, hash, base, typ string) string {
	f := TextureURLFilter{Hash: hash, URL: base, Type: typ}
	m.mu.RLock()
	extra := append([]timed[textureHookFn](nil), m.extra.texture...)
	var pluginFns []textureHookFn
	for _, name := range m.order {
		inst := m.instances[name]
		if inst.enabled && inst.rt != nil {
			for _, h := range inst.rt.hooks.texture {
				pluginFns = append(pluginFns, h.fn)
			}
		}
	}
	m.mu.RUnlock()

	for _, h := range extra {
		runGuarded(ctx, h.timeout, func(context.Context) error { return h.fn(ctx, &f) })
	}
	for _, fn := range pluginFns {
		_ = fn(ctx, &f)
	}
	return f.URL
}

// Emit* enqueue fire-and-forget events; they never block the request path.
func (m *Manager) EmitTextureUploaded(_ context.Context, e TextureUploadedEvent) {
	m.emit(eventUpload, e)
}

func (m *Manager) EmitUserLifecycle(_ context.Context, e UserLifecycleEvent) {
	m.emit(eventLifecycle, e)
}

func (m *Manager) EmitAuthPhase(_ context.Context, e AuthPhaseEvent) {
	m.emit(eventAuth, e)
}

// UploadMax returns the configured admin bundle size cap.
func (m *Manager) UploadMax() int64 {
	if m.cfg.UploadMax > 0 {
		return m.cfg.UploadMax
	}
	return 64 << 20
}
