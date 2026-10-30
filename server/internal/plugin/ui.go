package plugin

import (
	"html"
	"net/url"
	"strings"
)

// UI is the enabled-plugin UI metadata consumed by the SPA (nav/pages/slots)
// and by the SSR skinlib page (iframe slots only). No secrets are included.
type UI struct {
	Plugin  string                       `json:"plugin"`
	Version string                       `json:"version,omitempty"`
	Title   string                       `json:"title,omitempty"`
	Pages   []PageSpec                   `json:"pages,omitempty"`
	Slots   []SlotSpec                   `json:"slots,omitempty"`
	Forms   map[string]FormSpec          `json:"forms,omitempty"`
	I18n    map[string]map[string]string `json:"i18n,omitempty"`
}

// UI returns UI metadata for every enabled plugin, in name order.
func (m *Manager) UI() []UI {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]UI, 0, len(m.order))
	for _, name := range m.order {
		inst := m.instances[name]
		if !inst.enabled || inst.info.UI == nil {
			continue
		}
		u := UI{Plugin: name, Version: inst.info.Version, Title: inst.info.Title, I18n: inst.info.UI.I18n, Forms: inst.info.UI.Forms}
		for _, p := range inst.info.UI.Pages {
			p.Plugin = name
			u.Pages = append(u.Pages, p)
		}
		for _, s := range inst.info.UI.Slots {
			s.Plugin = name
			u.Slots = append(u.Slots, s)
		}
		out = append(out, u)
	}
	return out
}

// HasUIPath reports whether an enabled plugin owns this /ext path.
func (m *Manager) HasUIPath(p string) bool {
	p = "/" + strings.TrimPrefix(p, "/")
	m.mu.RLock()
	defer m.mu.RUnlock()
	for _, name := range m.order {
		inst := m.instances[name]
		if !inst.enabled || inst.info.UI == nil {
			continue
		}
		for _, page := range inst.info.UI.Pages {
			if page.Path == p {
				return true
			}
		}
	}
	return false
}

// SlotHTML renders the SSR-safe subset of a slot: iframe slots only. Schema
// slots are SPA-only in P1 (they need the Vue form renderer).
func (m *Manager) SlotHTML(slot string) string { return m.SlotHTMLContext(slot, nil) }

// SlotHTMLContext appends host-supplied query values (for example the current
// texture hash) to every rendered iframe slot.
func (m *Manager) SlotHTMLContext(slot string, vars map[string]string) string {
	query := ""
	if len(vars) > 0 {
		values := url.Values{}
		for k, v := range vars {
			if v != "" {
				values.Set(k, v)
			}
		}
		if enc := values.Encode(); enc != "" {
			query = "?" + enc
		}
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	var b strings.Builder
	for _, name := range m.order {
		inst := m.instances[name]
		if !inst.enabled || inst.info.UI == nil || inst.info.UI.Assets == "" {
			continue
		}
		for _, s := range inst.info.UI.Slots {
			if s.ID != slot || s.Render.Type != "iframe" {
				continue
			}
			src := "/plugin-assets/" + name + "/" + s.Render.Src + query
			if inst.info.Version != "" {
				sep := "?"
				if strings.Contains(src, "?") {
					sep = "&"
				}
				src += sep + "v=" + url.QueryEscape(inst.info.Version)
			}
			h := s.Height
			if h <= 0 {
				h = 180
			}
			if h < 80 {
				h = 80
			}
			if h > 600 {
				h = 600
			}
			b.WriteString(`<div class="plugin-slot" data-skin-slot="`)
			b.WriteString(html.EscapeString(slot))
			b.WriteString(`"><iframe src="`)
			b.WriteString(html.EscapeString(src))
			b.WriteString(`" loading="lazy" sandbox="allow-scripts allow-forms" style="width:100%;border:0;min-height:`)
			b.WriteString(itoa(h))
			b.WriteString("px")
			b.WriteString(`"></iframe></div>`)
		}
	}
	return b.String()
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [12]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}
