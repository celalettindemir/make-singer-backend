// Package clientip resolves the real client address of a request that
// reached us through Cloudflare.
//
// Traffic path: Cloudflare -> Traefik -> pod. Traefik reports its TCP peer
// (the Cloudflare edge for proxied traffic) as the client address, so a
// limiter keyed on it shares one bucket between all visitors of that edge.
// Cloudflare puts the visitor address in CF-Connecting-IP, but nothing
// strips that header from direct requests, so it is only trustworthy when
// the peer is itself a Cloudflare address. Resolve enforces exactly that.
package clientip

import (
	"fmt"
	"net"
	"strings"
)

// DefaultCloudflareRanges are Cloudflare's published edge ranges
// (https://www.cloudflare.com/ips/).
var DefaultCloudflareRanges = []string{
	"173.245.48.0/20", "103.21.244.0/22", "103.22.200.0/22", "103.31.4.0/22",
	"141.101.64.0/18", "108.162.192.0/18", "190.93.240.0/20", "188.114.96.0/20",
	"197.234.240.0/22", "198.41.128.0/17", "162.158.0.0/15", "104.16.0.0/13",
	"104.24.0.0/14", "172.64.0.0/13", "131.0.72.0/22",
	"2400:cb00::/32", "2606:4700::/32", "2803:f800::/32", "2405:b500::/32",
	"2405:8100::/32", "2a06:98c0::/29", "2c0f:f248::/32",
}

// CIDRs is a parsed list of networks.
type CIDRs []*net.IPNet

// ParseCIDRs parses CIDR strings (surrounding whitespace is ignored). Any
// invalid entry is an error; an empty list yields empty CIDRs.
func ParseCIDRs(list []string) (CIDRs, error) {
	out := make(CIDRs, 0, len(list))
	for _, raw := range list {
		_, n, err := net.ParseCIDR(strings.TrimSpace(raw))
		if err != nil {
			return nil, fmt.Errorf("invalid CIDR %q: %w", raw, err)
		}
		out = append(out, n)
	}
	return out, nil
}

// Contains reports whether ip is inside any of the networks.
func (c CIDRs) Contains(ip net.IP) bool {
	if ip == nil {
		return false
	}
	for _, n := range c {
		if n.Contains(ip) {
			return true
		}
	}
	return false
}

// Resolve returns the client address to rate-limit on. When peer is an IP
// inside cloudflare and cfConnectingIP is a valid IP, the header value is
// returned; otherwise peer is returned unchanged (including when cloudflare
// is empty, which disables the feature).
func Resolve(peer string, cfConnectingIP string, cloudflare CIDRs) string {
	if len(cloudflare) == 0 {
		return peer
	}
	if !cloudflare.Contains(net.ParseIP(strings.TrimSpace(peer))) {
		return peer
	}
	cf := strings.TrimSpace(cfConnectingIP)
	if net.ParseIP(cf) == nil {
		return peer
	}
	return cf
}
