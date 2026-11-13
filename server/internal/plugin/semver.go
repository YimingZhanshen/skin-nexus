package plugin

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"skin-nexus/server/internal/version"
)

var constraintRe = regexp.MustCompile(`^(>=|<=|>|<|=)?(.+)$`)

// semVersion is the shared version type. The grammar (x.y[.z] with an
// optional v prefix, nothing else) and the ordering live in internal/version
// so the plugin gate, the market formatter and the theme gate cannot drift
// apart again — they used to parse the same string three different ways.
// This alias keeps every call site below unchanged in shape while the rules
// have exactly one home.
type semVersion = version.Version

// checkMinServer evaluates a plugin minServer constraint against the host
// version. It supports the forms used by this repository: >=x.y[.z],
// <=x.y[.z], >x, <x, =x, and space/comma-separated conjunctions. Anything
// else (^, ~, prerelease, ranges with ||) is rejected so a typo cannot
// silently widen the gate.
func checkMinServer(constraint, hostVersion string) (bool, error) {
	constraint = strings.TrimSpace(constraint)
	if constraint == "" {
		return true, nil
	}
	host, err := version.Parse(hostVersion)
	if err != nil {
		return false, fmt.Errorf("host version: %w", err)
	}
	parts := strings.FieldsFunc(constraint, func(r rune) bool {
		return r == ',' || r == ' ' || r == '\t' || r == '\n'
	})
	if len(parts) == 0 {
		return false, fmt.Errorf("empty constraint")
	}
	for _, part := range parts {
		m := constraintRe.FindStringSubmatch(part)
		if m == nil {
			return false, fmt.Errorf("unsupported constraint %q", part)
		}
		op := m[1]
		if op == "" {
			op = "="
		}
		want, err := version.Parse(m[2])
		if err != nil {
			return false, fmt.Errorf("constraint %q: %w", part, err)
		}
		c := host.Compare(want)
		ok := false
		switch op {
		case ">=":
			ok = c >= 0
		case "<=":
			ok = c <= 0
		case ">":
			ok = c > 0
		case "<":
			ok = c < 0
		case "=":
			ok = c == 0
		}
		if !ok {
			return false, nil
		}
	}
	return true, nil
}

// satisfiesVersion implements the dependency-constraint subset used by
// manifests: comparators, ^, ~, *, x wildcards, and ||. Malformed constraints
// fail closed (error), never "allow everything".
func satisfiesVersion(ver, constraint string) (bool, error) {
	v, err := version.Parse(ver)
	if err != nil {
		return false, err
	}
	for _, group := range strings.Split(constraint, "||") {
		ok, err := satisfiesGroup(v, group)
		if err != nil {
			return false, err
		}
		if ok {
			return true, nil
		}
	}
	return false, nil
}

func satisfiesGroup(v semVersion, group string) (bool, error) {
	tokens := strings.FieldsFunc(group, func(r rune) bool {
		return r == ',' || r == ' ' || r == '\t'
	})
	if len(tokens) == 0 {
		return false, fmt.Errorf("empty constraint")
	}
	for _, tok := range tokens {
		ok, err := satisfiesToken(v, tok)
		if err != nil {
			return false, err
		}
		if !ok {
			return false, nil
		}
	}
	return true, nil
}

func satisfiesToken(v semVersion, tok string) (bool, error) {
	tok = strings.TrimSpace(tok)
	if tok == "" {
		return true, nil
	}
	if tok == "*" || strings.EqualFold(tok, "x") {
		return true, nil
	}
	// 1.2.x / 1.x: treat as a minor/major range.
	if strings.Contains(strings.ToLower(tok), "x") || strings.Contains(tok, "*") {
		parts := strings.Split(strings.TrimLeft(tok, "=vV"), ".")
		if len(parts) < 2 {
			return true, nil
		}
		base, err := strconv.Atoi(parts[0])
		if err != nil {
			return false, fmt.Errorf("invalid wildcard %q", tok)
		}
		if len(parts) >= 2 && parts[1] != "x" && parts[1] != "X" && parts[1] != "*" {
			minor, err := strconv.Atoi(parts[1])
			if err != nil {
				return false, fmt.Errorf("invalid wildcard %q", tok)
			}
			return v.Major == base && v.Minor == minor, nil
		}
		return v.Major == base, nil
	}
	op := ""
	for _, p := range []string{">=", "<=", ">", "<", "=", "^", "~"} {
		if strings.HasPrefix(tok, p) {
			op = p
			tok = strings.TrimSpace(strings.TrimPrefix(tok, p))
			break
		}
	}
	want, err := version.Parse(tok)
	if err != nil {
		return false, fmt.Errorf("constraint %q: %w", tok, err)
	}
	c := v.Compare(want)
	switch op {
	case "", "=":
		return c == 0, nil
	case ">=":
		return c >= 0, nil
	case "<=":
		return c <= 0, nil
	case ">":
		return c > 0, nil
	case "<":
		return c < 0, nil
	case "^":
		if c < 0 {
			return false, nil
		}
		if want.Major > 0 {
			return v.Major == want.Major, nil
		}
		if want.Minor > 0 {
			return v.Major == 0 && v.Minor == want.Minor, nil
		}
		return v.Major == 0 && v.Minor == 0 && v.Patch == want.Patch, nil
	case "~":
		if c < 0 {
			return false, nil
		}
		return v.Major == want.Major && v.Minor == want.Minor, nil
	}
	return false, fmt.Errorf("unsupported constraint %q", tok)
}

// CheckMinServer is the exported host-version gate used by the packaging CLI.
func CheckMinServer(constraint, hostVersion string) (bool, error) {
	return checkMinServer(constraint, hostVersion)
}
