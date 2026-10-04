package config

import (
	"fmt"
	"net/netip"
	"strings"
)

// TrustedProxyPrefixes returns TrustedProxies as prefixes; a bare address
// is a single-address prefix. Invalid entries were rejected by validate.
func (c Config) TrustedProxyPrefixes() []netip.Prefix {
	var out []netip.Prefix
	for _, s := range c.TrustedProxies {
		if p, err := parseProxy(s); err == nil {
			out = append(out, p)
		}
	}
	return out
}

func parseProxy(s string) (netip.Prefix, error) {
	s = strings.TrimSpace(s)
	if strings.Contains(s, "/") {
		p, err := netip.ParsePrefix(s)
		return p.Masked(), err
	}
	a, err := netip.ParseAddr(s)
	if err != nil {
		return netip.Prefix{}, err
	}
	a = a.Unmap()
	return netip.PrefixFrom(a, a.BitLen()), nil
}

func (c Config) validateTrustedProxies() []error {
	var errs []error
	for _, s := range c.TrustedProxies {
		p, err := parseProxy(s)
		switch {
		case err != nil:
			errs = append(errs, fmt.Errorf("%sTRUSTED_PROXIES: %q is not an IP or CIDR like 10.0.0.0/8", Prefix, s))
		case p.Bits() == 0:
			// Trusting every address lets any client pick its own IP.
			errs = append(errs, fmt.Errorf("%sTRUSTED_PROXIES: %q trusts every address; list your proxies only", Prefix, s))
		}
	}
	return errs
}
