package probe

import (
	"fmt"
	"math"
	"strconv"
	"strings"
)

// Cgroup is the resource envelope this process actually runs under, read from
// the unified cgroup v2 hierarchy mounted at /sys/fs/cgroup.
//
// In a container, /proc tells the truth about the host and nothing about the
// container: /proc/meminfo reports the host's memory, /proc/stat reports all of
// the host's cores, and /proc/pressure reports everyone's waiting. The cgroup
// tree is the one place a container can read its own limits — and its own
// pressure, because cgroup v2 exposes per-cgroup PSI alongside the system-wide
// one. A diagnosis that ignores it systematically overstates what the workload
// owns: load 4.0 is nothing on the host's 8 cores and a queue on a 2-core
// quota.
type Cgroup struct {
	// V2 reports whether a unified hierarchy exposes limits for this process.
	// cgroup v1 is not read: it has no per-cgroup PSI, and the majority of
	// current systems run v2.
	V2 bool

	// CPUQuotaCores is the bandwidth limit of cpu.max in cores, or 0 when
	// unlimited ("max 100000") or absent. "200000 100000" reads as 2.0.
	CPUQuotaCores float64

	// HostCores is the core count /proc/stat sees, recorded so the report can
	// state both numbers when a quota applies: "限额 2 核（宿主机 8 核）".
	HostCores int

	// MemoryMaxKB is the hard limit of memory.max, or 0 when unlimited.
	// Hitting it does not slow the cgroup down — the OOM killer takes the
	// largest process in it, which is why this is measured before it happens.
	MemoryMaxKB     uint64 `json:"memory_max_kb"`
	MemoryCurrentKB uint64 `json:"memory_current_kb"`

	// OOMKills is the oom_kill counter of memory.events: how many times the
	// kernel has already killed a process in this cgroup since it was created.
	// It is history, not a rate, and is reported as such.
	OOMKills uint64 `json:"oom_kills"`

	// Pressure is this cgroup's own PSI, from cpu/io/memory.pressure in the
	// same directory. Empty when the kernel does not expose per-cgroup PSI.
	// Judging by it, when it exists, is the difference between "the machine is
	// in trouble" and "this workload is in trouble" — on a noisy host those
	// are different findings.
	Pressure Pressure `json:"pressure"`

	// Throttle is the CFS bandwidth record from cpu.stat: how often this
	// cgroup used up its quota inside a period and was stopped until the next
	// one.
	//
	// It is the one measurement that explains a workload that is slow while
	// every other number looks healthy. A throttled task is not runnable, so
	// it does not appear in PSI cpu `some` (which counts runnable-but-
	// unscheduled) and it does not raise the load average (which counts
	// runnable and uninterruptible tasks). A container pinned at its quota
	// therefore shows low CPU usage, a flat load, clean pressure — and a
	// request latency that tripled.
	Throttle CPUThrottle `json:"throttle"`

	// MemoryBreaches is the `max` counter of memory.events: how many times
	// this cgroup touched its ceiling. Every one of them is a synchronous
	// reclaim stall, and none of them is yet a kill.
	MemoryBreaches uint64 `json:"memory_breaches"`

	// MemoryOOMs is the `oom` counter: how many times the limit forced the
	// OOM killer to run in this cgroup. It is one higher in the stack than
	// OOMKills — the killer ran, whether or not it found something to take.
	MemoryOOMs uint64 `json:"memory_ooms"`

	// EventsLocal is true when the memory counters came from
	// memory.events.local, which excludes descendant cgroups. Without it a
	// parent's totals would be attributed to the workload being diagnosed.
	EventsLocal bool `json:"events_local"`
}

// CPUThrottle is the CFS bandwidth control record.
//
// nr_throttled counting up means the workload ran out of quota, and only
// that: the kernel stops a cgroup when it has consumed its own quota for the
// period, independent of how busy the rest of the machine is. That makes it
// one of the few counters in here that licenses a definite statement rather
// than a suspicion.
type CPUThrottle struct {
	// Periods is nr_periods: how many enforcement windows have elapsed.
	Periods uint64 `json:"periods"`
	// Throttled is nr_throttled: how many of those windows ended with the
	// cgroup stopped for having used its quota up.
	Throttled uint64 `json:"throttled"`
	// ThrottledUsec is the cumulative time spent stopped, in microseconds.
	// It is a total since the cgroup was created, not a rate, and is reported
	// as such: on a long-lived pod a large value can be ancient history.
	ThrottledUsec uint64 `json:"throttled_usec"`
	// Known is false when cpu.stat was absent or had no throttling lines.
	// A zero counter must never be presented as "never throttled".
	Known bool `json:"known"`
}

// ThrottledPct is the share of enforcement periods that ended with this
// cgroup stopped, or 0 when no quota is in force.
//
// This is the number to judge by rather than throttled_usec: a pod that was
// throttled hard for ten seconds last Tuesday carries a huge total and is
// perfectly healthy now, while 30% of the last hundred periods means it is
// being stopped right now.
func (t CPUThrottle) ThrottledPct() float64 {
	if !t.Known || t.Periods == 0 {
		return 0
	}
	return float64(t.Throttled) * 100 / float64(t.Periods)
}

// ThrottledSeconds is the cumulative stopped time, for an evidence line that
// shows how much wall-clock the quota has cost in total.
func (t CPUThrottle) ThrottledSeconds() float64 {
	return float64(t.ThrottledUsec) / 1e6
}

// MemoryUsedPercent is memory.current as a percentage of memory.max, or 0 when
// no limit is set.
func (c Cgroup) MemoryUsedPercent() float64 {
	return pct(c.MemoryCurrentKB, c.MemoryMaxKB)
}

// ReadCgroup reads this process's own cgroup v2 limits and pressure.
//
// A missing hierarchy is not an error: on bare metal without a limit, and on
// cgroup v1 systems, V2 is simply false and every consumer falls back to the
// system-wide numbers. A present-but-unparseable file is an error, on the same
// principle as atou — a limit that failed to parse must not read as "no limit".
func ReadCgroup(r Root) (Cgroup, error) {
	var c Cgroup

	// Every file below lives at the *relative* path of this process's own
	// cgroup, not at the root of the hierarchy. Reading the root instead is
	// a silent failure that looks like success: the root of a unified
	// hierarchy is always "max 100000", so V2 comes back true, the quota
	// comes back unset, and ruleCPUThrottle then skips on a nil quota — the
	// headline K8s feature quietly off, with nothing in the report to say so.
	base := cgroupV2Base(r)

	data, err := r.ReadFile(base + "cpu.max")
	if err != nil {
		// No unified hierarchy mounted. Distinguish "this is a cgroup v1
		// host" from "this host has no cgroup at all", because they need
		// different things from the reader: v1 means every container limit
		// on this machine went unread, and staying quiet about that would
		// leave a plumb report on a RHEL 7 box looking complete while
		// silently skipping the entire cgroup section.
		if v1 := hasCgroupV1(r); v1 != "" {
			return Cgroup{}, fmt.Errorf(
				"cgroup v1 only (%s): container limits are not read — "+
					"this tool reads cgroup v2 (kernel 4.5+ with unified hierarchy)", v1)
		}
		// No cgroup hierarchy at all: bare metal without a limit, or a
		// namespace that hides it. Not an error — every consumer falls
		// back to the host-wide numbers.
		return Cgroup{}, nil
	}
	c.V2 = true

	fields := strings.Fields(string(data))
	if len(fields) != 2 {
		return c, fmt.Errorf("cgroup cpu.max: %d fields, want 2", len(fields))
	}
	if fields[0] != "max" {
		quota, qerr := atof(fields[0])
		period, perr := atof(fields[1])
		if qerr != nil || perr != nil || period <= 0 {
			return c, fmt.Errorf("cgroup cpu.max %q: cannot read quota/period", strings.Join(fields, " "))
		}
		c.CPUQuotaCores = quota / period
	}

	if data, err := r.ReadFile(base + "memory.max"); err == nil {
		v := strings.TrimSpace(string(data))
		if v != "max" {
			if bytes, err := atou(v); err != nil {
				return c, fmt.Errorf("cgroup memory.max %q: %w", v, err)
			} else {
				c.MemoryMaxKB = bytes / 1024
			}
		}
	}
	if data, err := r.ReadFile(base + "memory.current"); err == nil {
		if bytes, err := atou(strings.TrimSpace(string(data))); err == nil {
			c.MemoryCurrentKB = bytes / 1024
		}
	}
	// memory.events counts hierarchically — it includes descendant cgroups —
	// while memory.events.local counts only this one. The local file is
	// preferred: on a pod cgroup with children, the hierarchical total would
	// blame this workload for allocations it did not make.
	eventsPath := base + "memory.events"
	if r.Exists(base + "memory.events.local") {
		eventsPath = base + "memory.events.local"
		c.EventsLocal = true
	}
	if data, err := r.ReadFile(eventsPath); err == nil {
		ev, err := parseKeyValueLines(data, "oom_kill", "oom", "max")
		if err != nil {
			return c, fmt.Errorf("cgroup %s: %w", eventsPath, err)
		}
		c.OOMKills = ev["oom_kill"]
		c.MemoryOOMs = ev["oom"]
		c.MemoryBreaches = ev["max"]
	}

	var err2 error
	c.Throttle, err2 = readCPUStat(r, base)
	if err2 != nil {
		return c, err2
	}

	c.Pressure = readCgroupPressure(r, base)
	return c, nil
}

// hasCgroupV1 reports whether this host uses the legacy split hierarchies,
// naming the first controller mount it finds. It exists so ReadCgroup can say
// "your limits are in v1 files and this tool reads v2" instead of returning
// an empty Cgroup that reads as "no limit set".
func hasCgroupV1(r Root) string {
	// The v1 layout mounts one hierarchy per controller, each with its own
	// top-level directory under /sys/fs/cgroup.
	for _, c := range []string{"cpu", "memory", "pids", "cpuacct", "blkio", "cpuset"} {
		if r.Exists("sys/fs/cgroup/" + c) {
			return c
		}
	}
	return ""
}

// cgroupV2Base returns the /proc-relative directory holding this process's
// own cgroup v2 files, always ending in a slash so callers can concatenate a
// filename onto it.
//
// /proc/self/cgroup lists one "hierarchy:controllers:path" line per hierarchy;
// on a unified (v2) host the line with empty controllers is the one to use.
// Its path is relative to the cgroup mount, so joining it onto
// sys/fs/cgroup is what locates this process's own limits.
//
// If the file is missing or has no v2 line, the root of the hierarchy is
// returned. That is the best guess available and it is correct on a host
// where the tool runs in the root cgroup; the alternative would be to return
// nothing at all and silently report "no cgroup" on every system where the
// file cannot be read.
func cgroupV2Base(r Root) string {
	const mount = "sys/fs/cgroup/"
	base := mount
	data, err := r.ReadFile("proc/self/cgroup")
	if err != nil {
		return mount
	}
	_ = lines(data, func(line string) error {
		// Format: "0::/path" for v2, "2:cpu,cpuacct:/path" for v1.
		hierarchy, rest, ok := strings.Cut(line, ":")
		if !ok || hierarchy != "0" {
			return nil
		}
		_, path, ok := strings.Cut(rest, ":")
		if !ok || path == "" || path == "/" {
			return nil
		}
		// Reject anything that could climb out of the mount. The kernel
		// does not emit such paths, but this string is used to build a
		// filename, and -root lets tests point at an arbitrary tree.
		if strings.Contains(path, "..") {
			return nil
		}
		base = mount + strings.TrimPrefix(path, "/") + "/"
		return errStop
	})
	return base
}

// readCPUStat reads the CFS bandwidth counters from cpu.stat.
//
// nr_periods and nr_throttled exist only when a quota is in force, so a file
// without them reads as Known=false rather than as "throttled 0 times" — the
// difference between a ceiling that was never hit and one that was never
// installed.
func readCPUStat(r Root, base string) (CPUThrottle, error) {
	data, err := r.ReadFile(base + "cpu.stat")
	if err != nil {
		return CPUThrottle{}, nil
	}
	kv, err := parseKeyValueLines(data, "nr_periods", "nr_throttled", "throttled_usec")
	if err != nil {
		return CPUThrottle{}, fmt.Errorf("cgroup cpu.stat: %w", err)
	}
	_, hasPeriods := kv["nr_periods"]
	return CPUThrottle{
		Periods:       kv["nr_periods"],
		Throttled:     kv["nr_throttled"],
		ThrottledUsec: kv["throttled_usec"],
		Known:         hasPeriods,
	}, nil
}

// parseKeyValueLines reads a "key value" file — the format cpu.stat,
// memory.events and memory.stat all use — keeping only the named keys.
//
// Unknown keys are ignored so a newer kernel adding a counter does not break
// the reader, but a wanted key that will not parse is an error: a counter
// that silently reads as zero is a report claiming nothing ever happened.
func parseKeyValueLines(data []byte, want ...string) (map[string]uint64, error) {
	out := make(map[string]uint64, len(want))
	err := lines(data, func(line string) error {
		key, val, ok := strings.Cut(strings.TrimSpace(line), " ")
		if !ok {
			return nil
		}
		for _, w := range want {
			if key == w {
				n, err := atou(val)
				if err != nil {
					return fmt.Errorf("%s=%q: %w", key, val, err)
				}
				out[key] = n
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// readCgroupPressure reads this cgroup's own PSI files, which live beside the
// limits rather than under /proc. Missing files leave the zero value with
// Supported false — per-cgroup PSI needs the same kernel options as the
// system-wide one, plus a kernel new enough to expose it per cgroup.
func readCgroupPressure(r Root, base string) Pressure {
	var p Pressure
	p.CPU, _ = readPSIAt(r, base+"cpu.pressure")
	p.IO, _ = readPSIAt(r, base+"io.pressure")
	p.Memory, _ = readPSIAt(r, base+"memory.pressure")
	p.Supported = p.CPU.HasSome || p.IO.HasFull || p.Memory.HasFull
	return p
}

// EffectiveCores reports the core count a load average should be read against:
// the quota when one applies and is lower, the host count otherwise.
//
// The quota is rounded up, so a 1.5-core quota reads as 2: against the true
// 1.5 a load of 1.6 would look like a queue when the throttling has barely
// begun. Erring toward fewer alerts is the right direction for an accusation,
// and the exact quota is carried in the evidence for the reader who wants it.
// A host count of zero means /proc/stat could not be read, and in that case
// the quota is the more trustworthy of the two numbers: it is this workload's
// own ceiling, read from a different file. Falling back to 1 there would
// report a 1.5-core container as a single core.
func EffectiveCores(hostCores int, quotaCores float64) int {
	if quotaCores > 0 {
		q := int(math.Ceil(quotaCores))
		if q < 1 {
			q = 1
		}
		if hostCores <= 0 || q < hostCores {
			return q
		}
		return hostCores
	}
	if hostCores <= 0 {
		return 1
	}
	return hostCores
}

// FormatQuota renders the quota for an evidence line, e.g. "1.5 核".
func (c Cgroup) FormatQuota() string {
	return strconv.FormatFloat(c.CPUQuotaCores, 'f', -1, 64) + " 核"
}
