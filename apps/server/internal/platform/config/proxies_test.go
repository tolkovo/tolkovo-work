package config

import (
	"net/netip"
	"slices"
	"strings"
	"testing"
)

func TestLoadFrom_TrustedProxies(t *testing.T) {
	cfg, err := LoadFrom(map[string]string{
		"TOLKOVO_DATABASE_URL":    dsn,
		"TOLKOVO_TRUSTED_PROXIES": "10.0.0.0/8, 172.18.0.5,2001:db8::/32, 192.168.1.7/24",
	})
	if err != nil {
		t.Fatalf("LoadFrom: %v", err)
	}
	want := []netip.Prefix{
		netip.MustParsePrefix("10.0.0.0/8"),
		netip.MustParsePrefix("172.18.0.5/32"),
		netip.MustParsePrefix("2001:db8::/32"),
		netip.MustParsePrefix("192.168.1.0/24"),
	}
	if got := cfg.TrustedProxyPrefixes(); !slices.Equal(got, want) {
		t.Fatalf("prefixes = %v, want %v", got, want)
	}
}

func TestLoadFrom_RejectsInvalidTrustedProxies(t *testing.T) {
	for _, v := range []string{"proxy.local", "10.0.0.0/33", "0.0.0.0/0", "::/0"} {
		t.Run(v, func(t *testing.T) {
			_, err := LoadFrom(map[string]string{"TOLKOVO_DATABASE_URL": dsn, "TOLKOVO_TRUSTED_PROXIES": v})
			if err == nil || !strings.Contains(err.Error(), "TOLKOVO_TRUSTED_PROXIES") {
				t.Fatalf("want TOLKOVO_TRUSTED_PROXIES error, got %v", err)
			}
		})
	}
}
