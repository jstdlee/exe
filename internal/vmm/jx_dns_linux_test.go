//go:build linux

package vmm

import (
	"strings"
	"testing"
)

func TestGuestDNSConfigured(t *testing.T) {
	n := &vmNetwork{GuestIP: "172.30.0.2", HostIP: "172.30.0.1", PrefixLen: 30, DNS: []string{"10.64.0.1"}}
	got := systemdNetworkConfig("02:00:00:00:00:01", n)
	if !strings.Contains(got, "DNS=10.64.0.1\n") || strings.Contains(got, "1.1.1.1") {
		t.Fatalf("network file:\n%s", got)
	}
	if b := bootDNS(n); b != "10.64.0.1" {
		t.Fatalf("bootDNS = %q", b)
	}
	n.DNS = []string{"a", "b", "c"}
	if b := bootDNS(n); b != "a:b" {
		t.Fatalf("bootDNS = %q, want two servers", b)
	}
	if b := bootDNS(&vmNetwork{}); b != "1.1.1.1:8.8.8.8" {
		t.Fatalf("default bootDNS = %q", b)
	}
}
