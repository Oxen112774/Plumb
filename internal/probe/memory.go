package probe

import (
	"fmt"
	"strings"
)

// Memory is a snapshot of /proc/meminfo, in kilobytes.
type Memory struct {
	TotalKB     uint64 `json:"total_kb"`
	FreeKB      uint64 `json:"free_kb"`
	AvailableKB uint64 `json:"available_kb"`
	BuffersKB   uint64 `json:"buffers_kb"`
	CachedKB    uint64 `json:"cached_kb"`
	SwapTotalKB uint64 `json:"swap_total_kb"`
	SwapFreeKB  uint64 `json:"swap_free_kb"`

	// DirtyKB is data written by applications but not yet on disk.
	// WritebackKB is data currently in flight to the device.
	//
	// These two together are the earliest warning a write stall gives you:
	// when Dirty climbs into the hundreds of megabytes, every subsequent
	// write() starts blocking on the kernel to flush, and applications see
	// multi-second stalls on what look like trivial writes. Watching them
	// means you find out before the users do.
	DirtyKB     uint64 `json:"dirty_kb"`
	WritebackKB uint64 `json:"writeback_kb"`
}

// UsedKB is total minus available. Available (not free) is the right basis:
// it is the kernel's own estimate of what a new workload could claim, already
// accounting for reclaimable page cache.
func (m Memory) UsedKB() uint64 {
	if m.TotalKB < m.AvailableKB {
		return 0
	}
	return m.TotalKB - m.AvailableKB
}

// UsedPercent is UsedKB as a percentage of total.
func (m Memory) UsedPercent() float64 { return pct(m.UsedKB(), m.TotalKB) }

// SwapUsedKB is swap in use.
func (m Memory) SwapUsedKB() uint64 {
	if m.SwapTotalKB < m.SwapFreeKB {
		return 0
	}
	return m.SwapTotalKB - m.SwapFreeKB
}

// SwapPercent is swap in use as a percentage of total, or zero when no swap
// is configured.
func (m Memory) SwapPercent() float64 { return pct(m.SwapUsedKB(), m.SwapTotalKB) }

// ReadMemory parses /proc/meminfo.
//
// Every value the kernel reports is used; a missing key stays at zero. Unlike
// a counter, a missing meminfo key is meaningful on its own (a kernel built
// without swap simply omits SwapTotal), so absence is not an error here.
func ReadMemory(r Root) (Memory, error) {
	data, err := r.ReadFile("proc/meminfo")
	if err != nil {
		return Memory{}, fmt.Errorf("read proc/meminfo: %w", err)
	}

	info := make(map[string]uint64, 64)
	err = lines(data, func(line string) error {
		key, rest, ok := strings.Cut(line, ":")
		if !ok {
			return nil
		}
		fields := strings.Fields(rest)
		if len(fields) == 0 {
			return nil
		}
		v, err := atou(fields[0])
		if err != nil {
			return fmt.Errorf("proc/meminfo %s value %q: %w", key, fields[0], err)
		}
		info[key] = v
		return nil
	})
	if err != nil {
		return Memory{}, err
	}
	if _, ok := info["MemTotal"]; !ok {
		return Memory{}, fmt.Errorf("proc/meminfo: no MemTotal")
	}

	return Memory{
		TotalKB:     info["MemTotal"],
		FreeKB:      info["MemFree"],
		AvailableKB: info["MemAvailable"],
		BuffersKB:   info["Buffers"],
		CachedKB:    info["Cached"],
		SwapTotalKB: info["SwapTotal"],
		SwapFreeKB:  info["SwapFree"],
		DirtyKB:     info["Dirty"],
		WritebackKB: info["Writeback"],
	}, nil
}

// pct returns part/total as a percentage, guarding against a zero denominator.
func pct(part, total uint64) float64 {
	if total == 0 {
		return 0
	}
	return float64(part) * 100 / float64(total)
}
