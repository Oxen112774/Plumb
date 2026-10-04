package probe

import (
	"os"
	"path/filepath"
	"testing"
)

// fixture writes rel -> content into a temporary tree and returns a Root over
// it. The cgroup and network interfaces are files, so a test of them is a test
// of the parser: no /proc is involved, and these run on the Windows machine
// the project is developed on.
func fixture(t *testing.T, files map[string]string) Root {
	t.Helper()
	dir := t.TempDir()
	for rel, content := range files {
		p := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", rel, err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatalf("write %s: %v", rel, err)
		}
	}
	return NewRoot(dir)
}

// A container's own envelope: a 1.5-core quota on a 4-core host, an 8 GiB
// memory ceiling mostly used, and one OOM kill already on the record.
func cgroupFiles() map[string]string {
	return map[string]string{
		"sys/fs/cgroup/cpu.max":        "150000 100000\n",
		"sys/fs/cgroup/memory.max":     "8589934592\n",
		"sys/fs/cgroup/memory.current": "7516192768\n",
		"sys/fs/cgroup/memory.events":  "low 0\nhigh 12\nmax 0\noom 1\noom_kill 1\n",
		// 300 of the last 1000 enforcement periods ended with this cgroup
		// stopped: a container losing nearly a third of its scheduling
		// opportunities while its CPU usage reads modest.
		"sys/fs/cgroup/cpu.stat": "usage_usec 123456789\nuser_usec 90000000\nsystem_usec 30000000\n" +
			"nr_periods 1000\nnr_throttled 300\nthrottled_usec 12500000\n",
		"sys/fs/cgroup/io.pressure":     "some avg10=30.10 avg60=20.00 avg300=9.00 total=12345\nfull avg10=24.50 avg60=15.00 avg300=6.00 total=6789\n",
		"sys/fs/cgroup/memory.pressure": "some avg10=4.00 avg60=2.00 avg300=1.00 total=111\nfull avg10=1.10 avg60=0.50 avg300=0.20 total=222\n",
	}
}

func TestReadCgroupReadsQuotaCeilingAndOOMHistory(t *testing.T) {
	c, err := ReadCgroup(fixture(t, cgroupFiles()))
	if err != nil {
		t.Fatalf("ReadCgroup: %v", err)
	}
	if !c.V2 {
		t.Fatal("V2 = false, want true when cpu.max exists")
	}
	// 150000/100000 µs of runtime per 100000 µs of period is 1.5 cores.
	if c.CPUQuotaCores != 1.5 {
		t.Errorf("CPUQuotaCores = %v, want 1.5", c.CPUQuotaCores)
	}
	// 7516192768 / 8589934592 = 7 GiB of 8 GiB.
	if c.MemoryMaxKB != 8388608 {
		t.Errorf("MemoryMaxKB = %d, want 8388608", c.MemoryMaxKB)
	}
	if c.MemoryCurrentKB != 7340032 {
		t.Errorf("MemoryCurrentKB = %d, want 7340032", c.MemoryCurrentKB)
	}
	if p := c.MemoryUsedPercent(); p < 87.4 || p > 87.6 {
		t.Errorf("MemoryUsedPercent = %.2f, want 87.5", p)
	}
	if c.OOMKills != 1 {
		t.Errorf("OOMKills = %d, want 1", c.OOMKills)
	}
	if !c.Pressure.Supported {
		t.Error("Pressure.Supported = false; per-cgroup PSI files were present")
	}
	if c.Pressure.IO.Full.Avg10 != 24.5 {
		t.Errorf("cgroup io full avg10 = %v, want 24.5", c.Pressure.IO.Full.Avg10)
	}
}

// "max" is how the kernel writes "no limit". It must read as no limit, not as
// zero cores of quota — a container reported as having 0 cores would make
// every load figure look infinite.
func TestReadCgroupTreatsMaxAsNoLimit(t *testing.T) {
	files := cgroupFiles()
	files["sys/fs/cgroup/cpu.max"] = "max 100000\n"
	files["sys/fs/cgroup/memory.max"] = "max\n"

	c, err := ReadCgroup(fixture(t, files))
	if err != nil {
		t.Fatalf("ReadCgroup: %v", err)
	}
	if !c.V2 {
		t.Error("V2 = false; an unlimited cgroup is still a cgroup")
	}
	if c.CPUQuotaCores != 0 {
		t.Errorf("CPUQuotaCores = %v, want 0 for \"max\"", c.CPUQuotaCores)
	}
	if c.MemoryMaxKB != 0 {
		t.Errorf("MemoryMaxKB = %d, want 0 for \"max\"", c.MemoryMaxKB)
	}
	if c.MemoryUsedPercent() != 0 {
		t.Errorf("MemoryUsedPercent = %v, want 0 with no ceiling", c.MemoryUsedPercent())
	}
}

// A host running cgroup v1, or no hierarchy at all, has nothing to say here.
// That is not an error: every consumer falls back to the system-wide numbers,
// and an error would make a bare-metal report complain about a file it never
// needed.
func TestReadCgroupIsSilentWhenHierarchyIsAbsent(t *testing.T) {
	c, err := ReadCgroup(fixture(t, map[string]string{"proc/stat": "cpu 1 2 3\n"}))
	if err != nil {
		t.Fatalf("ReadCgroup on a tree without cgroup v2: %v", err)
	}
	if c.V2 {
		t.Error("V2 = true on a tree with no unified hierarchy")
	}
	if c.Pressure.Supported {
		t.Error("Pressure.Supported = true with no cgroup PSI files")
	}
}

// A limit that will not parse must not read as "no limit". This is the same
// rule as atou: in a monitoring tool a silently-ignored ceiling is a report
// that says everything is fine right up to the OOM kill.
func TestReadCgroupErrorsOnUnparseableQuota(t *testing.T) {
	files := cgroupFiles()
	files["sys/fs/cgroup/cpu.max"] = "150000\n"

	if _, err := ReadCgroup(fixture(t, files)); err == nil {
		t.Fatal("want an error for cpu.max with one field, got no error")
	}
}

func TestReadCgroupErrorsOnNonNumericQuota(t *testing.T) {
	files := cgroupFiles()
	files["sys/fs/cgroup/cpu.max"] = "abc def\n"

	if _, err := ReadCgroup(fixture(t, files)); err == nil {
		t.Fatal("want an error for a non-numeric quota, got no error")
	}
}

// Throttling is read as a share of periods, not of wall-clock time: a pod
// throttled hard for ten seconds last Tuesday carries a huge throttled_usec
// and is perfectly healthy now, while 30% of the last thousand periods means
// it is being stopped right now.
func TestReadCgroupReadsCPUThrottling(t *testing.T) {
	c, err := ReadCgroup(fixture(t, cgroupFiles()))
	if err != nil {
		t.Fatalf("ReadCgroup: %v", err)
	}
	if !c.Throttle.Known {
		t.Fatal("Throttle.Known = false; cpu.stat carried nr_periods")
	}
	if c.Throttle.Periods != 1000 || c.Throttle.Throttled != 300 {
		t.Errorf("periods/throttled = %d/%d, want 1000/300",
			c.Throttle.Periods, c.Throttle.Throttled)
	}
	if p := c.Throttle.ThrottledPct(); p < 29.9 || p > 30.1 {
		t.Errorf("ThrottledPct = %.2f, want 30", p)
	}
	if s := c.Throttle.ThrottledSeconds(); s < 12.4 || s > 12.6 {
		t.Errorf("ThrottledSeconds = %.2f, want 12.5", s)
	}
}

// A cpu.stat with no nr_periods means no quota is being enforced. It must not
// read as "throttled 0 of 0" and then be printed as a clean bill of health:
// the difference between a ceiling that was never hit and one that was never
// installed is the whole finding.
func TestReadCgroupThrottleIsUnknownWithoutPeriods(t *testing.T) {
	files := cgroupFiles()
	files["sys/fs/cgroup/cpu.stat"] = "usage_usec 123\nuser_usec 100\nsystem_usec 20\n"

	c, err := ReadCgroup(fixture(t, files))
	if err != nil {
		t.Fatalf("ReadCgroup: %v", err)
	}
	if c.Throttle.Known {
		t.Error("Throttle.Known = true with no nr_periods: that is not an enforced quota")
	}
	if p := c.Throttle.ThrottledPct(); p != 0 {
		t.Errorf("ThrottledPct = %v, want 0 when unknown", p)
	}
}

// memory.events counts hierarchically, so on a cgroup with children it would
// blame this workload for its descendants' allocations. Where the kernel
// offers memory.events.local, that is the file to read — and the report has
// to be able to say which one it used.
func TestReadCgroupPrefersLocalMemoryEvents(t *testing.T) {
	files := cgroupFiles()
	files["sys/fs/cgroup/memory.events.local"] = "low 0\nhigh 3\nmax 7\noom 2\noom_kill 2\n"

	c, err := ReadCgroup(fixture(t, files))
	if err != nil {
		t.Fatalf("ReadCgroup: %v", err)
	}
	if !c.EventsLocal {
		t.Error("EventsLocal = false; memory.events.local was present")
	}
	// The hierarchical file in the fixture says max 0 / oom 1 / oom_kill 1.
	// Reading those would under-report this cgroup's own history.
	if c.MemoryBreaches != 7 {
		t.Errorf("MemoryBreaches = %d, want 7 from memory.events.local", c.MemoryBreaches)
	}
	if c.MemoryOOMs != 2 {
		t.Errorf("MemoryOOMs = %d, want 2 from memory.events.local", c.MemoryOOMs)
	}
	if c.OOMKills != 2 {
		t.Errorf("OOMKills = %d, want 2 from memory.events.local", c.OOMKills)
	}
}

// A counter that will not parse must not read as zero: "oom_kill 0" and a
// corrupted oom_kill are different situations, and only one of them means
// nobody died.
func TestReadCgroupErrorsOnUnparseableMemoryEvents(t *testing.T) {
	files := cgroupFiles()
	files["sys/fs/cgroup/memory.events"] = "low 0\nhigh 12\nmax 0\noom abc\noom_kill 1\n"

	if _, err := ReadCgroup(fixture(t, files)); err == nil {
		t.Fatal("want an error for an unparseable oom counter, got none")
	}
}

// Rounding the quota up is a choice about which way to be wrong: a 1.5-core
// quota reads as 2, so a load of 1.6 is not called a queue while throttling
// has barely begun. Under-alerting is the right direction for an accusation.
func TestEffectiveCoresRoundsQuotaUpAndNeverExceedsHost(t *testing.T) {
	cases := []struct {
		name  string
		host  int
		quota float64
		want  int
	}{
		{"quota below host", 4, 1.5, 2},
		{"just under a core", 4, 1.01, 2},
		{"whole quota", 8, 2, 2},
		{"no quota", 8, 0, 8},
		{"quota wider than host", 8, 16, 8},
		{"degenerate host count", 0, 1.5, 2},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := EffectiveCores(tc.host, tc.quota); got != tc.want {
				t.Errorf("EffectiveCores(%d, %v) = %d, want %d",
					tc.host, tc.quota, got, tc.want)
			}
		})
	}
}
