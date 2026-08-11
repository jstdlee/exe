//go:build windows

package hostinfo

import "golang.org/x/sys/windows/registry"

// detectModel reads the SMBIOS system fields the firmware exposes under the
// BIOS registry key (SystemManufacturer + SystemProductName, e.g. "Alienware"
// + "Alienware 18 Area-51"). SystemFamily is the marketing name on some OEMs
// and is used when the product name is generic.
func detectModel() string {
	k, err := registry.OpenKey(registry.LOCAL_MACHINE, `HARDWARE\DESCRIPTION\System\BIOS`, registry.QUERY_VALUE)
	if err != nil {
		return ""
	}
	defer k.Close()
	get := func(name string) string { v, _, _ := k.GetStringValue(name); return v }
	vendor := get("SystemManufacturer")
	if m := combine(vendor, get("SystemProductName")); m != "" && m != clean(vendor) {
		return m
	}
	return combine(vendor, get("SystemFamily"))
}
