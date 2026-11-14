package httptransport

import (
	"net/http"
	"testing"
)

func TestParseTrustedProxies(t *testing.T) {
	networks := parseTrustedProxies([]string{
		"10.0.0.0/8",
		"192.168.1.10",
		"  ",
		"not-a-cidr",
		"::1",
	})
	if len(networks) != 3 {
		t.Fatalf("got %d networks, want 3", len(networks))
	}

	api := &API{trusted: networks}
	cases := []struct {
		ip   string
		want bool
	}{
		{"10.1.2.3", true},
		{"192.168.1.10", true},
		{"192.168.1.11", false},
		{"203.0.113.7", false},
		{"::1", true},
		{"not-an-ip", false},
	}
	for _, tc := range cases {
		if got := api.isTrustedProxy(tc.ip); got != tc.want {
			t.Fatalf("isTrustedProxy(%q) = %v, want %v", tc.ip, got, tc.want)
		}
	}
}

func TestClientIPIgnoresSpoofedHeaders(t *testing.T) {
	r, err := http.NewRequest(http.MethodGet, "http://example.test/", nil)
	if err != nil {
		t.Fatal(err)
	}
	r.RemoteAddr = "198.51.100.20:54321"
	r.Header.Set("X-Forwarded-For", "203.0.113.7")
	r.Header.Set("X-Real-IP", "203.0.113.8")

	untrusted := &API{}
	if got := untrusted.clientIP(r); got != "198.51.100.20" {
		t.Fatalf("untrusted clientIP = %q, want socket address", got)
	}

	trusted := &API{trusted: parseTrustedProxies([]string{"198.51.100.0/24"})}
	if got := trusted.clientIP(r); got != "203.0.113.7" {
		t.Fatalf("trusted clientIP = %q, want forwarded address", got)
	}
}
