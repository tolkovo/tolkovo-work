package api

import (
	"net/http/httptest"
	"net/netip"
	"testing"
)

func TestClientIPs(t *testing.T) {
	proxies := clientIPs{trusted: []netip.Prefix{
		netip.MustParsePrefix("10.0.0.0/8"),
		netip.MustParsePrefix("2001:db8:ffff::/48"),
	}}
	tests := []struct {
		name    string
		ips     clientIPs
		peer    string
		forward []string
		want    string
	}{
		{"no proxies: the peer, header ignored", clientIPs{}, "198.51.100.1:5000", []string{"203.0.113.7"}, "198.51.100.1"},
		{"untrusted peer cannot forge the header", proxies, "198.51.100.1:5000", []string{"203.0.113.7"}, "198.51.100.1"},
		{"trusted proxy names the client", proxies, "10.0.0.2:5000", []string{"203.0.113.7"}, "203.0.113.7"},
		{"forged entries left of the client are ignored", proxies, "10.0.0.2:5000", []string{"1.1.1.1, 203.0.113.7"}, "203.0.113.7"},
		{"chain of trusted proxies", proxies, "10.0.0.2:5000", []string{"203.0.113.7, 10.0.0.9"}, "203.0.113.7"},
		{"several header lines", proxies, "10.0.0.2:5000", []string{"1.1.1.1", "203.0.113.7"}, "203.0.113.7"},
		{"ipv6 client with port", proxies, "[2001:db8:ffff::1]:5000", []string{"[2001:db8::7]:4711"}, "2001:db8::7"},
		{"ipv4-mapped peer", proxies, "[::ffff:10.0.0.2]:5000", []string{"203.0.113.7"}, "203.0.113.7"},
		{"trusted proxy without header", proxies, "10.0.0.2:5000", nil, "10.0.0.2"},
		{"only trusted hops", proxies, "10.0.0.2:5000", []string{"10.0.0.3"}, "10.0.0.3"},
		{"garbage from the proxy stops the walk", proxies, "10.0.0.2:5000", []string{"203.0.113.7, unknown"}, "10.0.0.2"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := httptest.NewRequest("POST", "/api/v1/auth/login", nil)
			r.RemoteAddr = tt.peer
			for _, v := range tt.forward {
				r.Header.Add("X-Forwarded-For", v)
			}
			if got := tt.ips.of(r); got != tt.want {
				t.Fatalf("of() = %q, want %q", got, tt.want)
			}
		})
	}
}
