// Package hoststats samples this machine's CPU, disk I/O, network and
// free space so the VM list can show how much headroom the host has.
package hoststats

import (
	"strings"
	"sync"
	"time"
)

// Stats is a point-in-time view. Rates are bytes/sec over the interval
// since the previous Sample; the first Sample may sleep briefly.
type Stats struct {
	CPUPct       float64 `json:"cpu_pct"`
	DiskReadBps  int64   `json:"disk_read_bps"`
	DiskWriteBps int64   `json:"disk_write_bps"`
	NetRxBps     int64   `json:"net_rx_bps"`
	NetTxBps     int64   `json:"net_tx_bps"`
	DiskFree     int64   `json:"disk_free"`
	DiskTotal    int64   `json:"disk_total"`
}

type Proc struct {
	PID  int     `json:"pid"`
	CPU  float64 `json:"cpu"`
	Mem  float64 `json:"mem"`
	Cmd  string  `json:"cmd"`
}

func TopProcs(n int) []Proc { return readProcs(n) }

type snap struct {
	cpuIdle, cpuTotal    uint64
	diskRead, diskWrite  uint64
	netRx, netTx         uint64
	okCPU, okDisk, okNet bool
}

// Sampler remembers the last counters so rates are cheap to compute.
type Sampler struct {
	mu   sync.Mutex
	last snap
	at   time.Time
}

func (s *Sampler) Sample(diskPath string) Stats {
	free, total := DiskUsage(diskPath)
	out := Stats{DiskFree: free, DiskTotal: total}

	now := readSnap()
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.at.IsZero() || time.Since(s.at) > 30*time.Second {
		// no useful previous sample — take a short second look
		s.last, s.at = now, time.Now()
		s.mu.Unlock()
		time.Sleep(150 * time.Millisecond)
		now = readSnap()
		s.mu.Lock()
	}
	dt := time.Since(s.at).Seconds()
	if dt < 0.05 {
		dt = 0.05
	}
	if now.okCPU && s.last.okCPU {
		dIdle := now.cpuIdle - s.last.cpuIdle
		dTot := now.cpuTotal - s.last.cpuTotal
		if dTot > 0 {
			busy := 1 - float64(dIdle)/float64(dTot)
			if busy < 0 {
				busy = 0
			}
			if busy > 1 {
				busy = 1
			}
			out.CPUPct = busy * 100
		}
	}
	if now.okDisk && s.last.okDisk {
		out.DiskReadBps = rate(now.diskRead, s.last.diskRead, dt)
		out.DiskWriteBps = rate(now.diskWrite, s.last.diskWrite, dt)
	}
	if now.okNet && s.last.okNet {
		out.NetRxBps = rate(now.netRx, s.last.netRx, dt)
		out.NetTxBps = rate(now.netTx, s.last.netTx, dt)
	}
	s.last, s.at = now, time.Now()
	return out
}

func keepDisk(name string) bool {
	if name == "" || strings.HasPrefix(name, "loop") || strings.HasPrefix(name, "ram") ||
		strings.HasPrefix(name, "sr") || strings.HasPrefix(name, "fd") ||
		strings.HasPrefix(name, "dm-") || strings.HasPrefix(name, "zram") {
		return false
	}
	if strings.HasPrefix(name, "nvme") && strings.Contains(name, "p") {
		return false
	}
	if len(name) >= 3 && (strings.HasPrefix(name, "sd") || strings.HasPrefix(name, "vd") ||
		strings.HasPrefix(name, "hd") || strings.HasPrefix(name, "xvd")) {
		last := name[len(name)-1]
		if last >= '0' && last <= '9' {
			return false
		}
	}
	if strings.HasPrefix(name, "mmcblk") && strings.Contains(name, "p") {
		return false
	}
	return strings.HasPrefix(name, "sd") || strings.HasPrefix(name, "hd") ||
		strings.HasPrefix(name, "vd") || strings.HasPrefix(name, "xvd") ||
		strings.HasPrefix(name, "nvme") || strings.HasPrefix(name, "mmcblk")
}

func skipNet(name string) bool {
	switch {
	case name == "lo", name == "docker0",
		strings.HasPrefix(name, "br-"), strings.HasPrefix(name, "veth"),
		strings.HasPrefix(name, "virbr"), strings.HasPrefix(name, "tun"),
		strings.HasPrefix(name, "tap"), strings.HasPrefix(name, "fwbr"),
		strings.HasPrefix(name, "fwpr"), strings.HasPrefix(name, "fwln"),
		strings.HasPrefix(name, "cni"), strings.HasPrefix(name, "flannel"),
		strings.HasPrefix(name, "cali"), strings.HasPrefix(name, "dummy"),
		strings.HasPrefix(name, "sit"), strings.HasPrefix(name, "ifb"),
		strings.HasPrefix(name, "docker"):
		return true
	}
	return false
}

func rate(cur, prev uint64, dt float64) int64 {
	if cur < prev {
		return 0
	}
	return int64(float64(cur-prev) / dt)
}
