//go:build linux

package hoststats

import (
	"bufio"
	"os"
	"strconv"
	"slices"
	"path/filepath"
	"strings"
	"syscall"
)

func DiskUsage(path string) (free, total int64) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(path, &st); err != nil {
		return 0, 0
	}
	bs := int64(st.Bsize)
	return int64(st.Bavail) * bs, int64(st.Blocks) * bs
}

func readSnap() snap {
	var s snap
	s.cpuIdle, s.cpuTotal, s.okCPU = readCPU()
	s.diskRead, s.diskWrite, s.okDisk = readDisk()
	s.netRx, s.netTx, s.okNet = readNet()
	return s
}

func readCPU() (idle, total uint64, ok bool) {
	f, err := os.Open("/proc/stat")
	if err != nil {
		return 0, 0, false
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	if !sc.Scan() {
		return 0, 0, false
	}
	// cpu  user nice system idle iowait irq softirq steal …
	fs := strings.Fields(sc.Text())
	if len(fs) < 5 || fs[0] != "cpu" {
		return 0, 0, false
	}
	var nums []uint64
	for _, t := range fs[1:] {
		n, err := strconv.ParseUint(t, 10, 64)
		if err != nil {
			break
		}
		nums = append(nums, n)
		total += n
	}
	if len(nums) < 4 {
		return 0, 0, false
	}
	idle = nums[3]
	if len(nums) > 4 {
		idle += nums[4] // iowait
	}
	return idle, total, true
}

func readDisk() (rd, wr uint64, ok bool) {
	f, err := os.Open("/proc/diskstats")
	if err != nil {
		return 0, 0, false
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		fs := strings.Fields(sc.Text())
		if len(fs) < 14 {
			continue
		}
		if !keepDisk(fs[2]) {
			continue
		}
		r, err1 := strconv.ParseUint(fs[5], 10, 64) // sectors read
		w, err2 := strconv.ParseUint(fs[9], 10, 64) // sectors written
		if err1 != nil || err2 != nil {
			continue
		}
		rd += r * 512
		wr += w * 512
		ok = true
	}
	return rd, wr, ok
}

func readNet() (rx, tx uint64, ok bool) {
	f, err := os.Open("/proc/net/dev")
	if err != nil {
		return 0, 0, false
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := sc.Text()
		left, right, found := strings.Cut(line, ":")
		if !found {
			continue
		}
		name := strings.TrimSpace(left)
		if skipNet(name) {
			continue
		}
		fs := strings.Fields(right)
		if len(fs) < 9 {
			continue
		}
		r, err1 := strconv.ParseUint(fs[0], 10, 64)
		t, err2 := strconv.ParseUint(fs[8], 10, 64)
		if err1 != nil || err2 != nil {
			continue
		}
		rx += r
		tx += t
		ok = true
	}
	return rx, tx, ok
}

func readProcs(limit int) []Proc {
	if limit <= 0 {
		limit = 20
	}
	memTotal := uint64(0)
	if f, err := os.Open("/proc/meminfo"); err == nil {
		sc := bufio.NewScanner(f)
		for sc.Scan() {
			fs := strings.Fields(sc.Text())
			if len(fs) >= 2 && fs[0] == "MemTotal:" {
				memTotal, _ = strconv.ParseUint(fs[1], 10, 64)
				memTotal *= 1024
				break
			}
		}
		f.Close()
	}
	type raw struct {
		pid    int
		utime  uint64
		stime  uint64
		rss    uint64
		cmd    string
	}
	var out []raw
	dir, _ := os.Open("/proc")
	if dir == nil {
		return nil
	}
	defer dir.Close()
	names, _ := dir.Readdirnames(-1)
	for _, name := range names {
		pid, err := strconv.Atoi(name)
		if err != nil {
			continue
		}
		statPath := filepath.Join("/proc", name, "stat")
		b, err := os.ReadFile(statPath)
		if err != nil {
			continue
		}
		cmd := ""
		if c, err := os.ReadFile(filepath.Join("/proc", name, "cmdline")); err == nil && len(c) > 0 {
			cmd = strings.ReplaceAll(string(c), "\x00", " ")
			cmd = strings.TrimSpace(cmd)
			if len(cmd) > 120 {
				cmd = cmd[:120]
			}
		}
		if cmd == "" {
			fs := strings.Fields(string(b))
			if len(fs) > 1 {
				cmd = strings.Trim(fs[1], "()")
			}
		}
		fs := strings.Fields(string(b))
		if len(fs) < 24 {
			continue
		}
		utime, _ := strconv.ParseUint(fs[13], 10, 64)
		stime, _ := strconv.ParseUint(fs[14], 10, 64)
		rss, _ := strconv.ParseUint(fs[23], 10, 64)
		out = append(out, raw{pid, utime, stime, rss * 4096, cmd})
	}
	slices.SortFunc(out, func(a, b raw) int {
		pa := a.utime + a.stime
		pb := b.utime + b.stime
		if pa > pb {
			return -1
		}
		if pa < pb {
			return 1
		}
		return 0
	})
	var res []Proc
	for i, r := range out {
		if i >= limit {
			break
		}
		p := Proc{PID: r.pid, Cmd: r.cmd}
		if memTotal > 0 {
			p.Mem = float64(r.rss) / float64(memTotal) * 100
		}
		res = append(res, p)
	}
	return res
}
