// Mail copy and layout. The three letters the site sends (password reset,
// email verification, panel test-send) are built here so one catalog owns both
// languages and the HTML stays a constant skeleton with escaped insertions.
//
// Why the body never follows the theme's dark surface: mail clients strip
// <style>, force their own background and apply unpredictable dark-mode
// inversion. theme-base.css is a DARK source (--bg:#101825); carrying it into
// a letter yields white-on-white. Only the header band and the button take the
// theme accent; the reading surface is a constant light paper with dark ink.
// The default accent below is an independent constant, not a reference to the
// theme source: changing the site palette must not silently repaint old mail.
package webi18n

import (
	"html/template"
	"math"
	"strconv"
	"strings"
)

// MailPalette is the only theme-derived part of a letter.
type MailPalette struct {
	Accent    string // header band + button background
	ButtonInk string // button label, derived from Accent luminance
}

const (
	mailPaper    = "#f7f5f2"
	mailInk      = "#1f2733"
	mailMuted    = "#5a6472"
	mailLine     = "#e2ddd6"
	mailAccent   = "#b0453a"
	mailInkLight = "#fdf3ec"
)

// DefaultMailPalette is the fallback when no theme is active or its accent is
// not a color a mail client can render.
func DefaultMailPalette() MailPalette {
	return MailPalette{Accent: mailAccent, ButtonInk: mailInkLight}
}

// PaletteFromTokens bakes concrete values out of a theme token map. Anything
// missing or unusable falls back to the default; a light accent gets dark
// button ink so the label stays readable (contrast guard, not decoration).
func PaletteFromTokens(tok map[string]string) MailPalette {
	p := DefaultMailPalette()
	if a := usableMailColor(tok["--accent"]); a != "" {
		p.Accent = a
		if relativeLuminance(a) > 0.45 {
			p.ButtonInk = mailInk
		}
	}
	return p
}

// TestMailConfig is the credential-free summary the test letter carries so an
// operator can screenshot one mail and describe the whole configuration.
type TestMailConfig struct {
	SiteName, Host string
	Port           int
	Sender         string // envelope sender, bare address
	Lang, ThemeID  string // ThemeID "" renders as a dash
}

// ResetMail builds the password-reset letter. ttlMinutes is localized here so
// the duration keys stay referenced inside this package (the locale gate's
// dead-copy rule only sees references in the scanned trees).
func ResetMail(lang, siteName, link string, ttlMinutes int, p MailPalette) (subject, text, html string) {
	ttl := mailTTL(lang, ttlMinutes)
	subject = F(lang, "mail.resetSubject", "site", siteName)
	lead := F(lang, "mail.resetLead", "site", siteName)
	action := F(lang, "mail.resetAction", "ttl", ttl)
	button := T(lang, "mail.resetButton")
	ignore := T(lang, "mail.resetIgnore")
	text = mailText(lead, action, link, ignore, lang, siteName)
	html = mailHTML(mailPage{
		Lang: lang, Lead: lead, Action: action, Button: button, Link: link,
		Ignore: ignore, Footer: F(lang, "mail.footerNoReply", "site", siteName),
		Palette: p,
	})
	return subject, text, html
}

// VerifyMail builds the email-verification letter.
func VerifyMail(lang, siteName, link string, ttlMinutes int, p MailPalette) (subject, text, html string) {
	ttl := mailTTL(lang, ttlMinutes)
	subject = F(lang, "mail.verifySubject", "site", siteName)
	lead := F(lang, "mail.verifyLead", "site", siteName)
	action := F(lang, "mail.verifyAction", "ttl", ttl)
	button := T(lang, "mail.verifyButton")
	ignore := T(lang, "mail.verifyIgnore")
	text = mailText(lead, action, link, ignore, lang, siteName)
	html = mailHTML(mailPage{
		Lang: lang, Lead: lead, Action: action, Button: button, Link: link,
		Ignore: ignore, Footer: F(lang, "mail.footerNoReply", "site", siteName),
		Palette: p,
	})
	return subject, text, html
}

// TestMail builds the panel test letter, including what it proves and what it
// deliberately does not prove, plus the credential-free configuration summary.
func TestMail(lang string, c TestMailConfig, p MailPalette) (subject, text, html string) {
	subject = F(lang, "mail.testSubject", "site", c.SiteName)
	lead := F(lang, "mail.testLead", "site", c.SiteName)
	proves := T(lang, "mail.testProves")
	notProves := T(lang, "mail.testNotProves")
	theme := c.ThemeID
	if theme == "" {
		theme = "—"
	}
	summary := []string{
		T(lang, "mail.testSummaryTitle"),
		F(lang, "mail.testSummaryHost", "host", c.Host, "port", strconv.Itoa(c.Port)),
		F(lang, "mail.testSummarySender", "sender", c.Sender),
		F(lang, "mail.testSummaryLang", "lang", c.Lang),
		F(lang, "mail.testSummaryTheme", "theme", theme),
	}
	spam := T(lang, "mail.testSpam")
	footer := F(lang, "mail.footerNoReply", "site", c.SiteName)
	text = lead + "\n\n" + proves + "\n" + notProves + "\n\n" +
		strings.Join(summary, "\n") + "\n\n" + spam + "\n\n" + footer
	html = mailHTML(mailPage{
		Lang: lang, Lead: lead, Action: proves + " " + notProves, Button: "", Link: "",
		Ignore: spam, Footer: footer, Palette: p, Summary: summary,
	})
	return subject, text, html
}

func mailTTL(lang string, minutes int) string {
	if minutes < 60 {
		return F(lang, "mail.ttlMinutes", "n", strconv.Itoa(minutes))
	}
	return F(lang, "mail.ttlHours", "n", strconv.Itoa(minutes/60))
}

func mailText(lead, action, link, ignore, lang, site string) string {
	return lead + "\n\n" + action + "\n" + link + "\n\n" +
		T(lang, "mail.linkFallback") + "\n" + link + "\n\n" + ignore + "\n\n" +
		F(lang, "mail.footerNoReply", "site", site)
}

type mailPage struct {
	Lang, Lead, Action, Button, Link, Ignore, Footer string
	Palette                                          MailPalette
	Summary                                          []string
}

// mailSkeleton is a table layout with bgcolor attributes: the Outlook Word
// engine ignores most CSS but honours those. No <html>, no <style>, no remote
// image or font — clients block remote content by default and a tracking
// pixel would be a privacy regression.
const mailSkeleton = `<table role="presentation" width="100%" cellpadding="0" cellspacing="0" border="0" bgcolor="#f7f5f2">
<tr><td align="center" style="padding:24px 12px;">
<table role="presentation" width="560" cellpadding="0" cellspacing="0" border="0" bgcolor="#ffffff" style="max-width:560px;width:100%;">
<tr><td bgcolor="{{.Palette.Accent}}" style="padding:18px 24px;color:{{.Palette.ButtonInk}};font:600 16px/1.4 -apple-system,Segoe UI,Roboto,Helvetica,Arial,sans-serif;">{{.Lead}}</td></tr>
<tr><td style="padding:24px;color:#1f2733;font:14px/1.7 -apple-system,Segoe UI,Roboto,Helvetica,Arial,sans-serif;">
<p style="margin:0 0 16px;">{{.Action}}</p>
{{if .Button}}<table role="presentation" cellpadding="0" cellspacing="0" border="0"><tr><td bgcolor="{{.Palette.Accent}}" style="border-radius:4px;">
<a href="{{.Link}}" style="display:inline-block;padding:12px 22px;color:{{.Palette.ButtonInk}};text-decoration:none;font:600 14px/1 -apple-system,Segoe UI,Roboto,Helvetica,Arial,sans-serif;">{{.Button}}</a>
</td></tr></table>
<p style="margin:18px 0 6px;color:#5a6472;font-size:12px;">{{.LinkFallback}}</p>
<p style="margin:0;word-break:break-all;color:#5a6472;font-size:12px;">{{.Link}}</p>{{end}}
{{if .Summary}}<table role="presentation" width="100%" cellpadding="0" cellspacing="0" border="0" style="margin:18px 0 0;">
{{range .Summary}}<tr><td style="padding:4px 0;color:#5a6472;font-size:12px;border-top:1px solid #e2ddd6;">{{.}}</td></tr>
{{end}}</table>{{end}}
<p style="margin:18px 0 0;color:#5a6472;font-size:12px;">{{.Ignore}}</p>
</td></tr>
<tr><td bgcolor="#f7f5f2" style="padding:14px 24px;color:#5a6472;font-size:12px;border-top:1px solid #e2ddd6;">{{.Footer}}</td></tr>
</table>
</td></tr>
</table>`

var mailTmpl = template.Must(template.New("mail").Parse(mailSkeleton))

func mailHTML(p mailPage) string {
	var sb strings.Builder
	// LinkFallback is copy, not a field on the page struct in the skeleton's
	// callers; fill it here so every letter shares one wording.
	type pageWithFallback struct {
		mailPage
		LinkFallback string
	}
	_ = mailTmpl.Execute(&sb, pageWithFallback{p, T(p.Lang, "mail.linkFallback")})
	return sb.String()
}

// usableMailColor accepts only what mail clients render everywhere: 3/6-digit
// hex and integer rgb(). Alpha, modern color syntax and named colors are
// dropped so a theme cannot ship a transparent or unsupported band.
func usableMailColor(v string) string {
	v = strings.TrimSpace(v)
	if strings.HasPrefix(v, "#") {
		h := v[1:]
		if len(h) == 3 || len(h) == 6 {
			for _, c := range h {
				if !strings.ContainsRune("0123456789abcdefABCDEF", c) {
					return ""
				}
			}
			return strings.ToLower(v)
		}
		return ""
	}
	if strings.HasPrefix(v, "rgb(") && strings.HasSuffix(v, ")") {
		parts := strings.Split(v[4:len(v)-1], ",")
		if len(parts) != 3 {
			return ""
		}
		var nums [3]int
		for i, s := range parts {
			n, err := strconv.Atoi(strings.TrimSpace(s))
			if err != nil || n < 0 || n > 255 {
				return ""
			}
			nums[i] = n
		}
		return "rgb(" + strconv.Itoa(nums[0]) + "," + strconv.Itoa(nums[1]) + "," + strconv.Itoa(nums[2]) + ")"
	}
	return ""
}

// relativeLuminance is the WCAG linearized luminance of a usable color.
func relativeLuminance(v string) float64 {
	var r, g, b float64
	if strings.HasPrefix(v, "#") {
		h := v[1:]
		if len(h) == 3 {
			h = string([]byte{h[0], h[0], h[1], h[1], h[2], h[2]})
		}
		rv, _ := strconv.ParseUint(h[0:2], 16, 8)
		gv, _ := strconv.ParseUint(h[2:4], 16, 8)
		bv, _ := strconv.ParseUint(h[4:6], 16, 8)
		r, g, b = float64(rv)/255, float64(gv)/255, float64(bv)/255
	} else {
		parts := strings.Split(v[4:len(v)-1], ",")
		rv, _ := strconv.Atoi(parts[0])
		gv, _ := strconv.Atoi(parts[1])
		bv, _ := strconv.Atoi(parts[2])
		r, g, b = float64(rv)/255, float64(gv)/255, float64(bv)/255
	}
	lin := func(c float64) float64 {
		if c <= 0.03928 {
			return c / 12.92
		}
		return math.Pow((c+0.055)/1.055, 2.4)
	}
	return 0.2126*lin(r) + 0.7152*lin(g) + 0.0722*lin(b)
}
