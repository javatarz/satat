package egress

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"syscall"
	"time"
)

// blockedPrefixes are ranges a step must never reach through the proxy, besides what netip
// already classes as loopback, private, link-local, multicast or unspecified: the shared
// address space some clouds and Tailscale use, the cloud metadata and benchmarking ranges, the
// reserved ranges, and the IPv6 forms that embed an IPv4 address.
var blockedPrefixes = mustPrefixes(
	"0.0.0.0/8", "100.64.0.0/10", "192.0.0.0/24", "192.0.2.0/24", "198.18.0.0/15",
	"198.51.100.0/24", "203.0.113.0/24", "240.0.0.0/4", "255.255.255.255/32",
	"64:ff9b::/96", "64:ff9b:1::/48", "100::/64", "2001::/32", "2001:db8::/32", "2002::/16",
)

func mustPrefixes(s ...string) []netip.Prefix {
	var out []netip.Prefix
	for _, p := range s {
		out = append(out, netip.MustParsePrefix(p))
	}
	return out
}

// Blocked reports whether ip is in a range the proxy refuses to connect to: the host, the Docker
// networks, the Docker API proxy, the cloud metadata service and every other private address.
// An IPv4-mapped IPv6 address is judged as the IPv4 address it carries.
func Blocked(ip netip.Addr) bool {
	ip = ip.Unmap()
	if !ip.IsValid() || ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() ||
		ip.IsLinkLocalMulticast() || ip.IsInterfaceLocalMulticast() || ip.IsMulticast() || ip.IsUnspecified() {
		return true
	}
	for _, p := range blockedPrefixes {
		if p.Contains(ip) {
			return true
		}
	}
	return false
}

// ErrBlocked is returned for a name that resolves to an address the proxy refuses.
var ErrBlocked = errors.New("host resolves to a private or reserved address")

// Dialer connects to allowed hosts only. It resolves the name itself and refuses the host if any
// address it resolves to is blocked (a name with one public and one private address is a
// rebinding attempt, not a host to try the other address of). It then connects to the resolved
// address, never to the name again, and re-checks the address of the socket it is about to
// connect, so a resolver that answers differently the second time gets nothing.
type Dialer struct {
	Resolver *net.Resolver
	Timeout  time.Duration
}

// DialContext connects to host:port.
func (d Dialer) DialContext(ctx context.Context, host string, port int) (net.Conn, error) {
	r := d.Resolver
	if r == nil {
		r = net.DefaultResolver
	}
	addrs, err := r.LookupNetIP(ctx, "ip", host)
	if err != nil {
		return nil, fmt.Errorf("resolve %s: %w", host, err)
	}
	if len(addrs) == 0 {
		return nil, fmt.Errorf("resolve %s: no addresses", host)
	}
	for _, a := range addrs {
		if Blocked(a) {
			return nil, ErrBlocked
		}
	}
	nd := net.Dialer{
		Timeout: d.Timeout,
		Control: func(_, address string, _ syscall.RawConn) error {
			ap, err := netip.ParseAddrPort(address)
			if err != nil || Blocked(ap.Addr()) {
				return ErrBlocked
			}
			return nil
		},
	}
	var errs []error
	for _, a := range addrs {
		c, err := nd.DialContext(ctx, "tcp", netip.AddrPortFrom(a, uint16(port)).String())
		if err == nil {
			return c, nil
		}
		errs = append(errs, err)
	}
	return nil, errors.Join(errs...)
}
