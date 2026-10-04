package config

import (
	"fmt"
	"net/netip"
	"strings"
)

// EgressAllowPrivatePrefixes returns TOLKOVO_EGRESS_ALLOW_PRIVATE parsed;
// validate guarantees every entry parses. A bare IP means that one address.
func (c Config) EgressAllowPrivatePrefixes() []netip.Prefix {
	out := make([]netip.Prefix, 0, len(c.EgressAllowPrivate))
	for _, s := range c.EgressAllowPrivate {
		if p, ok := parsePrefixOrAddr(s); ok {
			out = append(out, p)
		}
	}
	return out
}

func (c Config) validateEgress() []error {
	name := Prefix + "EGRESS_ALLOW_PRIVATE"
	if len(c.EgressAllowPrivate) > 0 && c.Mode == ModeSaaS {
		return []error{fmt.Errorf("%s is a self-host exception and is not allowed with %sMODE=saas", name, Prefix)}
	}
	var errs []error
	for _, s := range c.EgressAllowPrivate {
		p, ok := parsePrefixOrAddr(s)
		switch {
		case !ok:
			errs = append(errs, fmt.Errorf("%s: %q is not an IP or CIDR like 127.0.0.1/32", name, s))
		case p.Bits() == 0:
			errs = append(errs, fmt.Errorf("%s: %q opens every address; list the hosts of your local models", name, s))
		case p.Addr().IsLinkLocalUnicast() || p.Contains(netip.MustParseAddr("169.254.169.254")):
			errs = append(errs, fmt.Errorf("%s: %q includes link-local addresses (cloud metadata), which stay blocked", name, s))
		}
	}
	return errs
}

func parsePrefixOrAddr(s string) (netip.Prefix, bool) {
	s = strings.TrimSpace(s)
	if p, err := netip.ParsePrefix(s); err == nil {
		return p.Masked(), true
	}
	if a, err := netip.ParseAddr(s); err == nil {
		return netip.PrefixFrom(a, a.BitLen()), true
	}
	return netip.Prefix{}, false
}
