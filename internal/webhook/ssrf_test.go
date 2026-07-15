package webhook

import (
	"net"
	"testing"
)

func TestCheckWebhookIP(t *testing.T) {
	cases := []struct {
		ip           string
		allowPrivate bool
		wantErr      bool
	}{
		// Public addresses always pass
		{"93.184.216.34", false, false},
		{"2606:2800:220:1:248:1893:25c8:1946", false, false},

		// Private ranges gated by the flag
		{"10.0.0.5", false, true},
		{"10.0.0.5", true, false},
		{"192.168.1.20", false, true},
		{"192.168.1.20", true, false},
		{"172.16.9.1", false, true},
		{"fd12:3456::1", false, true},
		{"fd12:3456::1", true, false},

		// Loopback metadata unspecified multicast always blocked
		{"127.0.0.1", true, true},
		{"::1", true, true},
		{"169.254.169.254", true, true},
		{"169.254.10.10", true, true},
		{"fe80::1", true, true},
		{"0.0.0.0", true, true},
		{"224.0.0.1", true, true},
	}

	for _, tc := range cases {
		ip := net.ParseIP(tc.ip)
		if ip == nil {
			t.Fatalf("bad test IP %q", tc.ip)
		}
		err := checkWebhookIP(ip, tc.allowPrivate)
		if (err != nil) != tc.wantErr {
			t.Errorf("checkWebhookIP(%s, allowPrivate=%v) err=%v, wantErr=%v", tc.ip, tc.allowPrivate, err, tc.wantErr)
		}
	}
}
