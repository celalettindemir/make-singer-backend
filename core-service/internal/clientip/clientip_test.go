package clientip

import (
	"net"
	"strings"
	"testing"
)

func mustCIDRs(t *testing.T, list ...string) CIDRs {
	t.Helper()
	c, err := ParseCIDRs(list)
	if err != nil {
		t.Fatalf("ParseCIDRs: %v", err)
	}
	return c
}

func TestParseCIDRs(t *testing.T) {
	if c, err := ParseCIDRs(nil); err != nil || len(c) != 0 {
		t.Fatalf("nil list = %v, %v; want empty, nil", c, err)
	}
	if _, err := ParseCIDRs([]string{"10.0.0.0/8", "not-a-cidr"}); err == nil {
		t.Fatal("an invalid entry must be rejected")
	}
	if _, err := ParseCIDRs([]string{"1.2.3.4"}); err == nil {
		t.Fatal("a bare IP (no prefix) must be rejected")
	}
	c, err := ParseCIDRs([]string{" 10.0.0.0/8 ", "2606:4700::/32"})
	if err != nil || len(c) != 2 {
		t.Fatalf("got %v, %v; want 2 entries (whitespace trimmed)", c, err)
	}
}

func TestDefaultCloudflareRangesParse(t *testing.T) {
	c, err := ParseCIDRs(DefaultCloudflareRanges)
	if err != nil {
		t.Fatalf("default ranges must parse: %v", err)
	}
	if len(c) != 22 {
		t.Fatalf("default range count = %d, want 22", len(c))
	}
	if !c.Contains(net.ParseIP("162.159.113.105")) {
		t.Error("the live edge IP 162.159.113.105 must be inside the defaults")
	}
}

func TestContains(t *testing.T) {
	c := mustCIDRs(t, "162.158.0.0/15", "2606:4700::/32")
	if c.Contains(nil) {
		t.Error("nil IP must not match")
	}
	if CIDRs(nil).Contains(net.ParseIP("162.158.0.1")) {
		t.Error("empty ranges must match nothing")
	}
}

func TestResolve(t *testing.T) {
	cf := mustCIDRs(t, strings.Split(strings.Join(DefaultCloudflareRanges, ","), ",")...)
	tests := []struct {
		name, peer, header string
		cloudflare         CIDRs
		want               string
	}{
		{"ipv4 CF peer uses header", "162.159.113.105", "198.51.100.7", cf, "198.51.100.7"},
		{"ipv6 CF peer uses header", "2606:4700::1", "198.51.100.7", cf, "198.51.100.7"},
		{"ipv4 CF peer with ipv6 client", "162.159.113.105", "2001:db8::5", cf, "2001:db8::5"},
		{"ipv4-mapped CF peer", "::ffff:162.159.113.105", "198.51.100.7", cf, "198.51.100.7"},
		{"header is trimmed", "162.159.113.105", "  198.51.100.7 ", cf, "198.51.100.7"},
		{"garbage header falls back to peer", "162.159.113.105", "not-an-ip", cf, "162.159.113.105"},
		{"empty header falls back to peer", "162.159.113.105", "", cf, "162.159.113.105"},
		{"list in header is not an IP", "162.159.113.105", "1.1.1.1, 2.2.2.2", cf, "162.159.113.105"},
		{"non-CF peer ignores forged header", "203.0.113.9", "198.51.100.7", cf, "203.0.113.9"},
		{"garbage peer ignores header", "garbage", "198.51.100.7", cf, "garbage"},
		{"empty ranges disable", "162.159.113.105", "198.51.100.7", nil, "162.159.113.105"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := Resolve(tc.peer, tc.header, tc.cloudflare); got != tc.want {
				t.Errorf("Resolve(%q, %q) = %q, want %q", tc.peer, tc.header, got, tc.want)
			}
		})
	}
}
