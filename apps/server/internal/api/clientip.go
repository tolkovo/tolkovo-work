package api

import (
	"net"
	"net/http"
	"net/netip"
	"strings"
)

// clientIPs finds the address of the client for rate limits. Only the TCP
// peer is known for sure; X-Forwarded-For is read only when the peer is a
// trusted proxy, because anyone else could forge it to dodge the limits.
type clientIPs struct {
	trusted []netip.Prefix
}

// of walks X-Forwarded-For from the right: each trusted proxy appended the
// address it received the request from, so the first untrusted address is
// the client. Entries left of it come from the client and are ignored.
func (c clientIPs) of(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	addr, err := netip.ParseAddr(host)
	if err != nil {
		return host
	}
	addr = addr.Unmap()
	if !c.isTrusted(addr) {
		return addr.String()
	}
	hops := strings.Split(strings.Join(r.Header.Values("X-Forwarded-For"), ","), ",")
	for i := len(hops) - 1; i >= 0; i-- {
		hop, ok := parseHop(hops[i])
		if !ok {
			// A trusted proxy wrote something we cannot read: stop at the
			// last address it vouched for.
			return addr.String()
		}
		if !c.isTrusted(hop) {
			return hop.String()
		}
		addr = hop
	}
	return addr.String()
}

func (c clientIPs) isTrusted(addr netip.Addr) bool {
	for _, p := range c.trusted {
		if p.Contains(addr) {
			return true
		}
	}
	return false
}

// parseHop reads one X-Forwarded-For entry: an address, optionally with a
// port ("203.0.113.7:4711", "[2001:db8::1]:4711").
func parseHop(s string) (netip.Addr, bool) {
	s = strings.TrimSpace(s)
	if a, err := netip.ParseAddr(s); err == nil {
		return a.Unmap(), true
	}
	if ap, err := netip.ParseAddrPort(s); err == nil {
		return ap.Addr().Unmap(), true
	}
	return netip.Addr{}, false
}
