package plugin

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// Hard ceilings keep a hostile or buggy plugin directory from exhausting the
// host. They are load-time only; runtime behaviour is bounded by timeouts.
const (
	maxManifestBytes   = 64 << 10
	maxScriptBytes     = 1 << 20
	maxPlugins         = 64
	maxRoutesPerPlugin = 32
	maxRoutesTotal     = 64

	maxTitleLen       = 128
	maxAuthorLen      = 64
	maxHomepageLen    = 256
	maxDescriptionLen = 512
	maxVersionLen     = 64
)

var (
	nameRe  = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{1,63}$`)
	entryRe = regexp.MustCompile(`^[A-Za-z0-9._-]+\.js$`)
)

// manifestFile is the on-disk plugin.json shape. Optional metadata fields
// (title/author/homepage) are additive: older plugins omit them and still
// load, while the admin surface and a future marketplace can display them.
type manifestFile struct {
	Name        string   `json:"name"`
	Version     string   `json:"version"`
	HookAPI     int      `json:"hookApi"`
	Entrypoint  string   `json:"entrypoint"`
	Permissions []string `json:"permissions"`
	MinServer   string   `json:"minServer,omitempty"`

	Runtime   string        `json:"runtime,omitempty"`
	Config    *ConfigSpec   `json:"config,omitempty"`
	Store     *StoreSpec    `json:"store,omitempty"`
	Routes    []RouteSpec   `json:"routes,omitempty"`
	UI        *UISpec       `json:"ui,omitempty"`
	Net       *NetSpec      `json:"net,omitempty"`
	Media     *MediaSpec    `json:"media,omitempty"`
	Requires  *RequiresSpec `json:"requires,omitempty"`
	Conflicts []string      `json:"conflicts,omitempty"`

	Title       string `json:"title,omitempty"`
	Author      string `json:"author,omitempty"`
	Homepage    string `json:"homepage,omitempty"`
	Description string `json:"description,omitempty"`
}

// Info is a validated manifest plus the resolved on-disk locations the host
// needs to start a runtime.
type Info struct {
	Manifest
	Title    string
	Author   string
	Homepage string

	Dir        string
	ScriptPath string
	ScriptSize int64
}

// knownPermissions mirrors the contract's declarative permission strings.
var knownPermissions = map[string]bool{
	PermReadProfiles:      true,
	PermReadTextures:      true,
	PermEditTextures:      true,
	PermRegisterRoute:     true,
	PermAuthHooks:         true,
	PermConfig:            true,
	PermStoreKV:           true,
	PermStoreDocs:         true,
	PermUIPage:            true,
	PermUISlot:            true,
	PermUIAssets:          true,
	PermUIForms:           true,
	PermHTTPRoutesSession: true,
	PermHTTPRoutesAdmin:   true,
	PermHTTPRoutesSuper:   true,
	PermHTTPRoutesBearer:  true,
	PermNetHTTP:           true,
	PermMailSend:          true,
	PermScheduleJobs:      true,
	PermCLICommands:       true,
	PermPluginsRead:       true,
	PermPluginsInvoke:     true,
	PermAuditWrite:        true,
	PermEventsSubscribe:   true,
	PermAuthGuards:        true,
	PermMediaAvatar:       true,
	PermAccountRead:       true,
	PermPlayersRead:       true,
	PermPlayersWrite:      true,
}

// loadManifest reads and validates <dir>/plugin.json. dirName is the parent
// directory basename and must equal the declared name.
func loadManifest(dir, dirName string) (Info, error) {
	path := filepath.Join(dir, "plugin.json")
	f, err := os.Open(path)
	if err != nil {
		return Info{}, fmt.Errorf("plugin.json: %w", err)
	}
	defer f.Close()

	st, err := f.Stat()
	if err != nil {
		return Info{}, fmt.Errorf("plugin.json: %w", err)
	}
	if st.Size() > maxManifestBytes {
		return Info{}, fmt.Errorf("plugin.json exceeds %d bytes", maxManifestBytes)
	}

	var raw manifestFile
	dec := json.NewDecoder(f)
	if err := dec.Decode(&raw); err != nil {
		return Info{}, fmt.Errorf("plugin.json: %w", err)
	}
	return validate(raw, dir, dirName)
}

func validate(raw manifestFile, dir, dirName string) (Info, error) {
	if raw.Name != dirName {
		return Info{}, fmt.Errorf("manifest name %q != directory %q", raw.Name, dirName)
	}
	if !nameRe.MatchString(raw.Name) {
		return Info{}, fmt.Errorf("invalid plugin name %q", raw.Name)
	}
	if raw.Version == "" || len(raw.Version) > maxVersionLen {
		return Info{}, errors.New("version must be a non-empty string <= 64 chars")
	}
	if raw.HookAPI != HookAPIVersion {
		return Info{}, fmt.Errorf("hookApi %d unsupported (host supports %d)", raw.HookAPI, HookAPIVersion)
	}
	if !entryRe.MatchString(raw.Entrypoint) {
		return Info{}, fmt.Errorf("invalid entrypoint %q", raw.Entrypoint)
	}

	perms := make([]string, 0, len(raw.Permissions))
	seen := map[string]bool{}
	for _, p := range raw.Permissions {
		if !knownPermissions[p] {
			return Info{}, fmt.Errorf("unknown permission %q", p)
		}
		if !seen[p] {
			seen[p] = true
			perms = append(perms, p)
		}
	}

	if len(raw.Title) > maxTitleLen || len(raw.Author) > maxAuthorLen ||
		len(raw.Homepage) > maxHomepageLen || len(raw.Description) > maxDescriptionLen {
		return Info{}, errors.New("metadata field exceeds length limit")
	}
	if raw.Homepage != "" && !strings.HasPrefix(raw.Homepage, "http://") && !strings.HasPrefix(raw.Homepage, "https://") {
		return Info{}, errors.New("homepage must be an http(s) URL or empty")
	}

	if raw.Runtime == "" {
		raw.Runtime = "goja"
	}

	script := filepath.Join(dir, raw.Entrypoint)
	// Containment: the entrypoint must stay inside the plugin directory and
	// must be a regular file (no symlink escape).
	if rel, err := filepath.Rel(dir, script); err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return Info{}, fmt.Errorf("entrypoint escapes plugin dir: %q", raw.Entrypoint)
	}
	lst, err := os.Lstat(script)
	if err != nil {
		return Info{}, fmt.Errorf("entrypoint: %w", err)
	}
	if lst.Mode()&os.ModeSymlink != 0 {
		return Info{}, errors.New("entrypoint must not be a symlink")
	}
	if !lst.Mode().IsRegular() {
		return Info{}, errors.New("entrypoint must be a regular file")
	}
	if lst.Size() > maxScriptBytes {
		return Info{}, fmt.Errorf("entrypoint exceeds %d bytes", maxScriptBytes)
	}

	info := Info{
		Manifest: Manifest{
			Name:        raw.Name,
			Version:     raw.Version,
			HookAPI:     raw.HookAPI,
			Permissions: perms,
			Entrypoint:  raw.Entrypoint,
			MinServer:   raw.MinServer,
			Description: raw.Description,
			Runtime:     raw.Runtime,
			Config:      raw.Config,
			Store:       raw.Store,
			Routes:      raw.Routes,
			UI:          raw.UI,
			Media:       raw.Media,
			Requires:    raw.Requires,
			Conflicts:   raw.Conflicts,
		},
		Title:      raw.Title,
		Author:     raw.Author,
		Homepage:   raw.Homepage,
		Dir:        dir,
		ScriptPath: script,
		ScriptSize: lst.Size(),
	}
	if err := validateV2Manifest(&info.Manifest, dir); err != nil {
		return Info{}, err
	}
	return info, nil
}

func (i Info) configSchemaVersion() int {
	if i.Store == nil {
		return 0
	}
	return i.Store.SchemaVersion
}

// CheckMinServer reports whether this plugin is allowed on hostVersion.
func (i Info) CheckMinServer(hostVersion string) error {
	ok, err := checkMinServer(i.MinServer, hostVersion)
	if err != nil {
		return fmt.Errorf("minServer: %w", err)
	}
	if !ok {
		return fmt.Errorf("minServer %q not satisfied by host %s", i.MinServer, hostVersion)
	}
	return nil
}

// hasPermission reports whether the manifest declared a capability.
func (i Info) hasPermission(p string) bool { return i.Manifest.hasPermission(p) }

func (m Manifest) hasPermission(p string) bool {
	for _, x := range m.Permissions {
		if x == p {
			return true
		}
	}
	return false
}

// InspectDir validates a plugin directory exactly as the host loader does.
// CLI packers use it so they cannot emit a bundle the host would reject.
func InspectDir(dir string) (Info, error) {
	return loadManifest(dir, filepath.Base(filepath.Clean(dir)))
}
