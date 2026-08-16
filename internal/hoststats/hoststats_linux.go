//go:build linux

package hoststats

import (
	"bufio"
	"os"
	"strconv"
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
