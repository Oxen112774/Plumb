package diagnose

import (
	"strings"
	"testing"
	"time"

	"github.com/Oxen112774/plumb/internal/probe"
)

// A container 7.5 GiB into an 8 GiB ceiling. /proc/meminfo cannot see this: in
// a container it reports the host's memory, so the limit itself is the only
// thing that can warn.
func containerSnapshot(usedKB, maxKB uint64, oomKills uint64) probe.Snapshot {
	return probe.Snapshot{
		At: time.Now(),
		Cgroup: probe.Cgroup{
			V2:              true,
			HostCores:       8,
			CPUQuotaCores:   1.5,
			MemoryMaxKB:     maxKB,
			MemoryCurrentKB: usedKB,
			OOMKills:        oomKills,
		},
	}
}

func TestCgroupMemoryCeilingIsReported(t *testing.T) {
	cur := containerSnapshot(7864320, 8388608, 0) // 7.5 GiB of 8 GiB = 93.75%
	rep := Analyze(probe.Snapshot{}, cur, Options{})

	f := findArea(rep, "内存配额")
	if f == nil {
		t.Fatalf("no cgroup memory finding at 93.75%%; findings = %+v", rep.Findings)
	}
	if f.Severity != SeverityWarning {
		t.Errorf("severity = %q, want %q at 93.75%% of the ceiling", f.Severity, SeverityWarning)
	}
	if !strings.Contains(f.Headline, "93.8%") {
		t.Errorf("headline = %q, want the percentage of the ceiling", f.Headline)
	}
	if len(f.Evidence) == 0 || f.Evidence[0].Source != "/sys/fs/cgroup/memory.max" {
		t.Errorf("evidence[0].Source = %q, want /sys/fs/cgroup/memory.max", f.Evidence[0].Source)
	}
}

// A cgroup that has already been OOM-killed is past predicting. The count is
// history, and the finding has to carry it so the reader knows this ceiling has
// been hit before.
func TestCgroupMemoryFindingCarriesOOMHistory(t *testing.T) {
	cur := containerSnapshot(7549747, 8388608, 2) // 90% and two kills
	rep := Analyze(probe.Snapshot{}, cur, Options{})

	f := findArea(rep, "内存配额")
	if f == nil {
		t.Fatalf("no cgroup memory finding; findings = %+v", rep.Findings)
	}
	found := false
	for _, e := range f.Evidence {
		if e.Source == "/sys/fs/cgroup/memory.events" && strings.Contains(e.Detail, "2 次") {
			found = true
		}
	}
	if !found {
		t.Errorf("no evidence citing memory.events with the kill count; evidence = %+v", f.Evidence)
	}
}

// No ceiling means nothing to warn about. Reporting 0% of a limit that does not
// exist would be noise, and "unlimited" is the uninteresting case.
func TestCgroupMemoryIsSilentWithoutACeiling(t *testing.T) {
	cur := containerSnapshot(7864320, 0, 0)
	rep := Analyze(probe.Snapshot{}, cur, Options{})

	if f := findArea(rep, "内存配额"); f != nil {
		t.Errorf("cgroup memory reported with no memory.max: %+v", f)
	}
}

// On a host with no limits at all there is no cgroup story, and the rules must
// not invent one.
func TestCgroupMemoryIsSilentOutsideACgroup(t *testing.T) {
	cur := probe.Snapshot{At: time.Now()}
	rep := Analyze(probe.Snapshot{}, cur, Options{})

	if f := findArea(rep, "内存配额"); f != nil {
		t.Errorf("cgroup memory reported outside a cgroup v2 hierarchy: %+v", f)
	}
}

// Under a CPU quota the per-core rows come from /proc/stat, which is the
// host's view: a co-tenant can be the one pinning core 0. So the rule may name
// a process only when the kernel says that process actually ran on a peaked
// core — field 39 of /proc/<pid>/stat. Two facts ("a core is full", "a process
// is busy") do not by themselves license the accusation.
func TestCoreSkewInsideAQuotaNeedsTheProcessOnTheHotCore(t *testing.T) {
	prev, cur := skewedSnapshots()
	prev.Procs = []probe.Process{{PID: 3000, Name: "node"}}
	cur.Procs = []probe.Process{{PID: 3000, Name: "node", UTime: 90, STime: 9, LastCPU: 0, LastCPUKnown: true}}
	cur.Cgroup = probe.Cgroup{V2: true, HostCores: 4, CPUQuotaCores: 1.5}

	rep := Analyze(prev, cur, Options{Interval: time.Second})
	if f := findArea(rep, "单核跑满"); f == nil {
		t.Fatalf("no core-skew finding when the culprit last ran on the hot core; findings = %+v", rep.Findings)
	}

	// Same machine, same quota, but the process last ran on an idle core: the
	// hot core belongs to someone this cgroup cannot see.
	prev2, cur2 := skewedSnapshots()
	prev2.Procs = prev.Procs
	cur2.Procs = []probe.Process{{PID: 3000, Name: "node", UTime: 90, STime: 9, LastCPU: 1, LastCPUKnown: true}}
	cur2.Cgroup = cur.Cgroup

	rep2 := Analyze(prev2, cur2, Options{Interval: time.Second})
	if f := findArea(rep2, "单核跑满"); f != nil {
		t.Errorf("named a process that last ran on an idle core: %+v", f)
	}
}

// A kernel that does not report field 39 leaves the link unproven, and an
// unproven link is not evidence.
func TestCoreSkewInsideAQuotaIsSilentWhenLastCPUIsUnknown(t *testing.T) {
	prev, cur := skewedSnapshots()
	prev.Procs = []probe.Process{{PID: 3000, Name: "node"}}
	cur.Procs = []probe.Process{{PID: 3000, Name: "node", UTime: 90, STime: 9}}
	cur.Cgroup = probe.Cgroup{V2: true, HostCores: 4, CPUQuotaCores: 1.5}

	rep := Analyze(prev, cur, Options{Interval: time.Second})
	if f := findArea(rep, "单核跑满"); f != nil {
		t.Errorf("named a process with no core attribution: %+v", f)
	}
}
func TestCgroupPressureIsJudgedInsteadOfTheHost(t *testing.T) {
	cur := probe.Snapshot{
		At:       time.Now(),
		Pressure: psi(1.0, 0.5), // host-wide: nearly nothing waiting
		Cgroup: probe.Cgroup{
			V2:            true,
			HostCores:     8,
			CPUQuotaCores: 1.5,
			Pressure:      psi(40.0, 25.0), // this cgroup: a quarter of the time frozen
		},
	}
	rep := Analyze(probe.Snapshot{}, cur, Options{})

	f := findArea(rep, "磁盘 IO")
	if f == nil {
		t.Fatalf("no IO finding from cgroup pressure; findings = %+v", rep.Findings)
	}
	if f.Severity != SeverityCritical {
		t.Errorf("severity = %q, want %q for cgroup io full 25", f.Severity, SeverityCritical)
	}
	for _, e := range f.Evidence {
		if strings.Contains(e.Detail, "full avg10=25") &&
			e.Source != "/sys/fs/cgroup/io.pressure" {
			t.Errorf("the judged number is cited as %q, want /sys/fs/cgroup/io.pressure", e.Source)
		}
	}
}
