package hoststats

import "testing"

func TestRate(t *testing.T) {
	if g := rate(2000, 1000, 1); g != 1000 {
		t.Fatalf("rate = %d", g)
	}
	if g := rate(100, 200, 1); g != 0 {
		t.Fatalf("wrap = %d", g)
	}
}

func TestKeepDisk(t *testing.T) {
	for _, n := range []string{"sda", "nvme0n1", "vda", "mmcblk0"} {
		if !keepDisk(n) {
			t.Fatalf("keep %s", n)
		}
	}
	for _, n := range []string{"loop0", "sda1", "nvme0n1p2", "dm-0", "zram0"} {
		if keepDisk(n) {
			t.Fatalf("drop %s", n)
		}
	}
}

func TestSkipNet(t *testing.T) {
	if !skipNet("lo") || !skipNet("veth0") || !skipNet("docker0") {
		t.Fatal("should skip")
	}
	if skipNet("eth0") || skipNet("enp1s0") || skipNet("wlan0") {
		t.Fatal("should keep")
	}
}
