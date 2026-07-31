package discovery

import (
	"strings"
	"testing"
)

func TestExpandCIDR(t *testing.T) {
	tests := []struct {
		name    string
		cidr    string
		wantErr string // substring of expected error; "" means success
		wantLen int    // expected host count on success
	}{
		{name: "single host /32", cidr: "192.168.1.5/32", wantLen: 1},
		{name: "point-to-point /31", cidr: "192.168.1.0/31", wantLen: 2},
		{name: "classic /24", cidr: "192.168.1.0/24", wantLen: 254},
		{name: "full /16 allowed", cidr: "10.0.0.0/16", wantLen: 65534},
		{name: "/15 too large", cidr: "10.0.0.0/15", wantErr: "too large"},
		{name: "/8 too large", cidr: "10.0.0.0/8", wantErr: "too large"},
		{name: "IPv4 /0 too large", cidr: "0.0.0.0/0", wantErr: "too large"},
		{name: "IPv6 unsupported", cidr: "::/0", wantErr: "not supported"},
		{name: "garbage", cidr: "not-a-cidr", wantErr: "invalid CIDR"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ips, err := expandCIDR(tt.cidr)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("expandCIDR(%q) error = %v, want containing %q", tt.cidr, err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("expandCIDR(%q) unexpected error: %v", tt.cidr, err)
			}
			if len(ips) != tt.wantLen {
				t.Fatalf("expandCIDR(%q) = %d hosts, want %d", tt.cidr, len(ips), tt.wantLen)
			}
			if len(ips) > MaxExpandHosts {
				t.Fatalf("expandCIDR(%q) exceeded MaxExpandHosts: %d", tt.cidr, len(ips))
			}
		})
	}
}

func TestExpandCIDRNetworkAndBroadcastStripped(t *testing.T) {
	ips, err := expandCIDR("192.168.1.0/24")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(ips) != 254 {
		t.Fatalf("len = %d, want 254", len(ips))
	}
	if ips[0] != "192.168.1.1" {
		t.Errorf("first host = %s, want 192.168.1.1", ips[0])
	}
	if ips[len(ips)-1] != "192.168.1.254" {
		t.Errorf("last host = %s, want 192.168.1.254", ips[len(ips)-1])
	}
}

func TestExpandRange(t *testing.T) {
	tests := []struct {
		name    string
		rng     string
		wantErr string
		wantLen int
	}{
		{name: "full ip range", rng: "192.168.1.1-192.168.1.5", wantLen: 5},
		{name: "last octet range", rng: "192.168.1.1-5", wantLen: 5},
		{name: "single address", rng: "192.168.1.1-192.168.1.1", wantLen: 1},
		{name: "malformed part count", rng: "1.2.3.4-5-6", wantErr: "expected START-END"},
		{name: "bad start", rng: "abc-192.168.1.5", wantErr: "invalid range start"},
		{name: "bad end", rng: "192.168.1.1-abc", wantErr: "invalid range end"},
		{name: "ipv6 end", rng: "192.168.1.1-::1", wantErr: "invalid range end"},
		{name: "end precedes start", rng: "192.168.1.10-192.168.1.1", wantErr: "end precedes start"},
		{name: "full ipv4 space too large", rng: "0.0.0.0-255.255.255.255", wantErr: "max"},
		{name: "large range too large", rng: "10.0.0.0-10.1.0.0", wantErr: "max"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ips, err := expandRange(tt.rng)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("expandRange(%q) error = %v, want containing %q", tt.rng, err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("expandRange(%q) unexpected error: %v", tt.rng, err)
			}
			if len(ips) != tt.wantLen {
				t.Fatalf("expandRange(%q) = %d hosts, want %d", tt.rng, len(ips), tt.wantLen)
			}
		})
	}
}

func TestExpandTarget(t *testing.T) {
	if ips, err := expandTarget("192.168.1.5"); err != nil || len(ips) != 1 || ips[0] != "192.168.1.5" {
		t.Fatalf("expandTarget(single) = %v, %v", ips, err)
	}
	if ips, err := expandTarget(" 192.168.1.0/24 "); err != nil || len(ips) != 254 {
		t.Fatalf("expandTarget(cidr) = %d hosts, %v", len(ips), err)
	}
	for _, bad := range []string{"", "1.2.3.999", "not-an-ip", "1.2.3.4-"} {
		if ips, err := expandTarget(bad); err == nil {
			t.Fatalf("expandTarget(%q) = %v, nil; want error", bad, ips)
		}
	}
}
