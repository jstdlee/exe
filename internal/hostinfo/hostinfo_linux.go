//go:build linux

package hostinfo

import (
	"os"
	"strings"
)

// detectModel reads the SMBIOS/DMI fields x86 machines expose under
// /sys/class/dmi/id (sys_vendor + product_name); ARM machines like the DGX
// Spark usually have no DMI and expose their model in the device tree instead
// (a NUL-terminated string).
func detectModel() string {
	rd := func(p string) string { b, _ := os.ReadFile(p); return string(b) }
	if m := combine(rd("/sys/class/dmi/id/sys_vendor"), rd("/sys/class/dmi/id/product_name")); m != "" {
		return m
	}
	return clean(strings.TrimRight(rd("/proc/device-tree/model"), "\x00"))
}
