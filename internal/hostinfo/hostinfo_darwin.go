//go:build darwin

package hostinfo

import (
	"os/exec"
	"strings"
)

// detectModel reads the hardware profile's "Model Name" (e.g. "MacBook Pro"),
// which is friendlier than the hw.model identifier used as a fallback.
func detectModel() string {
	if out, err := exec.Command("system_profiler", "SPHardwareDataType").Output(); err == nil {
		for _, line := range strings.Split(string(out), "\n") {
			if k, v, ok := strings.Cut(line, ":"); ok && strings.TrimSpace(k) == "Model Name" {
				return clean(v)
			}
		}
	}
	if out, err := exec.Command("sysctl", "-n", "hw.model").Output(); err == nil {
		return clean(string(out))
	}
	return ""
}
