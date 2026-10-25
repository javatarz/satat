// Package egress is the hostname-allowlist forward proxy of a lane's network (ADR 0025, ADR 0026,
// #169).
//
// A step on an internal lane network has no route out. It reaches the outside through this
// proxy, which tunnels (HTTP CONNECT) or forwards (plain HTTP) to a host only if the lane's
// allowlist names it. There is no TLS interception, no credential and no per-step identity: the
// proxy sees a host name and a port, and decides on those alone. It fails closed: anything the
// allowlist does not name, an address literal, a port the entry does not allow, and any name that
// resolves into a private, loopback, link-local or metadata range, is refused.
package egress

import (
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// DefaultPorts are the ports an entry without an explicit port allows.
var DefaultPorts = []int{443, 80}

// Rule is one allowlist entry: a host name, or "*." and a suffix for every sub-domain of it (not
// the suffix itself), and the ports it allows.
type Rule struct {
	// Host is lower case and has no trailing dot. For a wildcard it is the suffix after "*.".
	Host     string
	Wildcard bool
	Ports    []int
}

var hostRE = regexp.MustCompile(`^([a-z0-9]([a-z0-9_-]{0,61}[a-z0-9])?\.)+[a-z]([a-z0-9-]{0,61}[a-z0-9])?$`)

// IsName reports whether host is a plausible DNS name: at least two labels, and a last label that
// starts with a letter. That rules out every address literal, in any notation (1.1.1.1, 0x7f.1,
// 2130706433, ::1), without parsing them.
func IsName(host string) bool {
	return len(host) <= 253 && hostRE.MatchString(host)
}

// ParseRule reads "host", "host:port", "*.suffix" or "*.suffix:port".
func ParseRule(s string) (Rule, error) {
	var r Rule
	host, port, hasPort := strings.Cut(strings.ToLower(strings.TrimSpace(s)), ":")
	if hasPort {
		n, err := strconv.Atoi(port)
		if err != nil || n < 1 || n > 65535 {
			return r, fmt.Errorf("%q: %q is not a port", s, port)
		}
		r.Ports = []int{n}
	} else {
		r.Ports = DefaultPorts
	}
	if suffix, ok := strings.CutPrefix(host, "*."); ok {
		r.Wildcard, host = true, suffix
	}
	if !IsName(host) {
		return r, fmt.Errorf("%q: %q is not a host name (address literals are not allowed)", s, host)
	}
	r.Host = host
	return r, nil
}

// Allowlist is a set of rules. The zero value allows nothing.
type Allowlist struct {
	rules []Rule
}

// ParseAllowlist reads every entry, and reports every bad one.
func ParseAllowlist(entries []string) (Allowlist, error) {
	var a Allowlist
	var errs []error
	for _, e := range entries {
		r, err := ParseRule(e)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		a.rules = append(a.rules, r)
	}
	return a, errors.Join(errs...)
}

// Allows reports whether host:port may be reached. host is matched as sent, lower-cased and
// without a trailing dot; it is the name the client asked for, never an address.
func (a Allowlist) Allows(host string, port int) bool {
	host = strings.TrimSuffix(strings.ToLower(host), ".")
	if !IsName(host) {
		return false
	}
	for _, r := range a.rules {
		match := host == r.Host
		if r.Wildcard {
			match = strings.HasSuffix(host, "."+r.Host)
		}
		if !match {
			continue
		}
		for _, p := range r.Ports {
			if p == port {
				return true
			}
		}
	}
	return false
}
