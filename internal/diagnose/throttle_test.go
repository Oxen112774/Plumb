package diagnose

import (
	"strings"
	"testing"
	"time"

	"github.com/Oxen112774/plumb/internal/i18n"
	"github.com/Oxen112774/plumb/internal/probe"
)

// A pod pinned at a 1.5-core quota, stopped in 300 of its last thousand
// enforcement periods.
func throttleSnapshot(throttled, periods uint64) probe.Snapshot {
	return probe.Snapshot{
		At: time.Now(),
		Cgroup: probe.Cgroup{
			V2:            true,
			HostCores:     8,
			CPUQuotaCores: 1.5,
			Throttle: probe.CPUThrottle{
				Periods:       periods,
				Throttled:     throttled,
				ThrottledUsec: 12500000,
				Known:         true,
			},
		},
	}
}

// hasCJK reports whether s contains a Han character. The English output must
// contain none: a half-translated report is worse than an honest Chinese one,
// because the reader cannot tell whether the Chinese passages are deliberate.
func hasCJK(s string) bool {
	for _, r := range s {
		if r >= 0x4e00 && r <= 0x9fff {
			return true
		}
	}
	return false
}

func assertNoChinese(t *testing.T, rep Report) {
	t.Helper()
	if len(rep.Findings) == 0 {
		t.Fatal("no findings; the translation test would pass vacuously")
	}
	for _, f := range rep.Findings {
		for _, s := range append([]string{f.Area, f.Headline, f.Advice}, evidenceStrings(&f)...) {
			if hasCJK(s) {
				t.Errorf("untranslated Chinese in %q: %q", f.Area, s)
			}
		}
	}
}

func evidenceStrings(f *Finding) []string {
	out := make([]string, 0, len(f.Evidence))
	for _, e := range f.Evidence {
		out = append(out, e.Source, e.Detail)
	}
	return out
}

// The finding that nothing else in the report can produce: a workload losing
// a third of its scheduling opportunities while CPU usage, load and pressure
// all read clean.
func TestCPUThrottleIsReported(t *testing.T) {
	cur := throttleSnapshot(300, 1000)
	rep := Analyze(probe.Snapshot{}, cur, Options{})

	f := findArea(rep, "CPU 配额")
	if f == nil {
		t.Fatalf("no CPU quota finding at 30%% throttled; findings = %+v", rep.Findings)
	}
	if f.Severity != SeverityCritical {
		t.Errorf("severity = %q, want %q at 30%% of periods throttled", f.Severity, SeverityCritical)
	}
	// The two counts must come out in this order. The Chinese and English
	// sentences order them differently and share one argument list, so
	// "2617 of 8431" is one edit away from printing as "8431 of 2617".
	if !strings.Contains(f.Headline, "1000 个调度周期里有 300 个") {
		t.Errorf("headline = %q, want periods first then throttled", f.Headline)
	}
	if !strings.Contains(f.Headline, "30.0%") {
		t.Errorf("headline = %q, want the throttled share", f.Headline)
	}
	// A cumulative counter is not a rate, and the finding must not dress one
	// up as the other.
	if !strings.Contains(f.Headline, "累计值") {
		t.Errorf("headline = %q, want the total labelled as cumulative", f.Headline)
	}
	joined := strings.Join(evidenceStrings(f), " ")
	for _, want := range []string{
		"/sys/fs/cgroup/cpu.stat",
		"nr_throttled=300",
		"nr_periods=1000",
		"/sys/fs/cgroup/cpu.max",
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("evidence missing %q: %q", want, joined)
		}
	}
	// Why every other section stayed quiet belongs in the finding: without it
	// the reader sees 30% throttled next to a clean CPU section and has to
	// choose which to believe.
	if !strings.Contains(joined, "不计入") {
		t.Errorf("evidence does not explain why PSI and load missed it: %q", joined)
	}
}

// The distinction that makes this worth more than a counter on a dashboard:
// whether the waiting is all at the quota, or whether tasks are also queueing
// behind someone else on the node.
//
// These are different problems with opposite fixes. Raising a limit when the
// node is contended makes the queue longer; tuning code when the limit is the
// whole story achieves nothing.
func TestCPUThrottleSeparatesQuotaFromNodeContention(t *testing.T) {
	t.Run("quota only", func(t *testing.T) {
		cur := throttleSnapshot(300, 1000)
		cur.Pressure = psi(1.0, 0.5) // cpu some 1%: nobody queueing
		rep := Analyze(probe.Snapshot{}, cur, Options{})

		f := findArea(rep, "CPU 配额")
		if f == nil {
			t.Fatalf("no CPU quota finding; findings = %+v", rep.Findings)
		}
		joined := strings.Join(evidenceStrings(f), " ")
		if !strings.Contains(joined, "不是宿主机的争抢") {
			t.Errorf("evidence does not rule out node contention: %q", joined)
		}
		if strings.Contains(f.Advice, "两件事同时成立") {
			t.Error("advice describes contention although cpu some is 1%")
		}
	})

	t.Run("quota and contention", func(t *testing.T) {
		cur := throttleSnapshot(300, 1000)
		cur.Pressure = psi(40.0, 0.5) // cpu some 40%: tasks queueing too
		rep := Analyze(probe.Snapshot{}, cur, Options{})

		f := findArea(rep, "CPU 配额")
		if f == nil {
			t.Fatalf("no CPU quota finding; findings = %+v", rep.Findings)
		}
		if !strings.Contains(f.Advice, "两件事同时成立") {
			t.Errorf("advice does not warn that both hold: %q", f.Advice)
		}
		// Raising the limit is the wrong first move here, and saying so is
		// the whole value of the distinction.
		if !strings.Contains(f.Advice, "先解决节点侧的争抢") {
			t.Errorf("advice does not sequence the two fixes: %q", f.Advice)
		}
	})
}

// A cgroup with no quota cannot be throttled, and a cpu.stat with no
// nr_periods means no quota is being enforced. Both are "not applicable", not
// "throttled zero times" — the second would print a clean bill of health for a
// container plumb never actually checked.
func TestCPUThrottleIsSilentWhenItDoesNotApply(t *testing.T) {
	cases := []struct {
		name    string
		mutate  func(*probe.Snapshot)
		wantNil bool
	}{
		{"no quota", func(s *probe.Snapshot) { s.Cgroup.CPUQuotaCores = 0 }, true},
		{"no cgroup", func(s *probe.Snapshot) { s.Cgroup = probe.Cgroup{} }, true},
		{"throttling unknown", func(s *probe.Snapshot) { s.Cgroup.Throttle.Known = false }, true},
		{"no periods yet", func(s *probe.Snapshot) { s.Cgroup.Throttle.Periods = 0 }, true},
		// 1% of periods is below the 2% watch line: reporting it would put a
		// line in every report on every machine.
		{"below the watch line", func(s *probe.Snapshot) {
			s.Cgroup.Throttle.Periods = 1000
			s.Cgroup.Throttle.Throttled = 10
		}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cur := throttleSnapshot(300, 1000)
			tc.mutate(&cur)
			rep := Analyze(probe.Snapshot{}, cur, Options{})
			f := findArea(rep, "CPU 配额")
			if tc.wantNil && f != nil {
				t.Errorf("reported throttling for %s: %+v", tc.name, f)
			}
		})
	}
}

// nr_throttled is a counter since the cgroup was created. With two samples
// the increase inside the window is the number that says whether this is
// happening now — a pod throttled hard for a minute last Tuesday carries a
// huge total and is perfectly healthy today.
func TestCPUThrottleJudgesTheWindowWhenTwoSamplesExist(t *testing.T) {
	prev := throttleSnapshot(2617, 8431)
	cur := throttleSnapshot(2617+8, 8431+10) // 8 of 10 periods in the window

	rep := Analyze(prev, cur, Options{Interval: time.Second})
	f := findArea(rep, "CPU 配额")
	if f == nil {
		t.Fatalf("no CPU quota finding; findings = %+v", rep.Findings)
	}
	// 80% of the window, not 31% of the pod's life: judging the total would
	// have reported this as a warning instead of a critical.
	if f.Severity != SeverityCritical {
		t.Errorf("severity = %q, want critical for 8 of 10 periods in the window", f.Severity)
	}
	if !strings.Contains(f.Headline, "10 个调度周期里有 8 个") {
		t.Errorf("headline = %q, want this window's counts", f.Headline)
	}
	if strings.Contains(f.Headline, "累计值") {
		t.Errorf("headline = %q, want no cumulative caveat when the window is known", f.Headline)
	}
	// Both numbers belong in the evidence: the rate that was judged, and the
	// total that gives it context.
	joined := strings.Join(evidenceStrings(f), " ")
	for _, want := range []string{"nr_throttled 增加 8", "nr_periods 增加 10", "nr_throttled=2625"} {
		if !strings.Contains(joined, want) {
			t.Errorf("evidence missing %q: %q", want, joined)
		}
	}
}

// The threshold is a real knob: moving it must move the verdict, which is the
// test that separates a configurable threshold from a decorative one.
func TestCPUThrottleThresholdMovesTheVerdict(t *testing.T) {
	cur := throttleSnapshot(50, 1000) // 5%: watch at most, by default

	base := Analyze(probe.Snapshot{}, cur, Options{})
	if f := findArea(base, "CPU 配额"); f == nil || f.Severity != SeverityWatch {
		t.Fatalf("want watch at 5%% with defaults, got %+v", f)
	}

	th := DefaultThresholds()
	th.CPUThrottleWatch = 1.0
	th.CPUThrottleWarn = 4.0
	th.CPUThrottleCritical = 6.0
	rep := Analyze(probe.Snapshot{}, cur, Options{Thresholds: th})
	f := findArea(rep, "CPU 配额")
	if f == nil || f.Severity != SeverityWarning {
		t.Fatalf("want warning at 5%% with warn moved to 4, got %+v", f)
	}
	// A moved line has to be disclosed in the finding, or the reader cannot
	// tell whether "warning" is plumb's judgement or the operator's.
	if !strings.Contains(strings.Join(evidenceStrings(f), " "), "config") {
		t.Errorf("no config evidence for a moved threshold: %+v", f.Evidence)
	}
}

// /proc/pressure is not namespaced. Inside a container whose cgroup has no
// PSI files of its own, the verdict borrows the whole node's waiting — and
// presenting that as the workload's own is what sends someone off to profile
// an innocent service.
func TestPressureScopeIsDisclosedInsideAContainer(t *testing.T) {
	cur := probe.Snapshot{
		At:       time.Now(),
		Pressure: psi(30.0, 17.4), // node-wide: a disk in trouble somewhere
		Cgroup: probe.Cgroup{
			V2:            true,
			HostCores:     8,
			CPUQuotaCores: 1.5,
			// No per-cgroup PSI, so the rules must fall back to the node's.
		},
	}
	rep := Analyze(probe.Snapshot{}, cur, Options{})

	f := findArea(rep, "磁盘 IO")
	if f == nil {
		t.Fatalf("no IO finding; findings = %+v", rep.Findings)
	}
	joined := strings.Join(evidenceStrings(f), " ")
	if !strings.Contains(joined, "节点级") {
		t.Errorf("IO verdict used node-wide PSI without saying so: %q", joined)
	}
	if !strings.Contains(joined, "不是你") {
		t.Errorf("evidence does not warn that the waiting may be a neighbour's: %q", joined)
	}
}

// In a pod the note names pods rather than "other containers": the reader is
// a Kubernetes operator, and the neighbours that matter to them are pods.
func TestPressureScopeNoteNamesPodsInsideKubernetes(t *testing.T) {
	cur := probe.Snapshot{
		At:       time.Now(),
		Pressure: psi(30.0, 17.4),
		Cgroup:   probe.Cgroup{V2: true, CPUQuotaCores: 1.5},
		K8s:      probe.K8sContext{InPod: true, PodUID: "8f2c1a3e-9d4b-4c6e-a1b2-c3d4e5f60718"},
	}
	rep := Analyze(probe.Snapshot{}, cur, Options{})

	f := findArea(rep, "磁盘 IO")
	if f == nil {
		t.Fatalf("no IO finding; findings = %+v", rep.Findings)
	}
	joined := strings.Join(evidenceStrings(f), " ")
	if !strings.Contains(joined, "其他 Pod") {
		t.Errorf("note does not mention other pods: %q", joined)
	}
}

// When the cgroup has its own PSI the number is already this workload's, and
// qualifying it would just add noise to every report on a healthy host.
func TestPressureScopeNoteIsAbsentWhenTheCgroupHasItsOwnPSI(t *testing.T) {
	cur := probe.Snapshot{
		At:       time.Now(),
		Pressure: psi(1.0, 0.5),
		Cgroup: probe.Cgroup{
			V2:            true,
			CPUQuotaCores: 1.5,
			Pressure:      psi(40.0, 17.4),
		},
	}
	rep := Analyze(probe.Snapshot{}, cur, Options{})

	f := findArea(rep, "磁盘 IO")
	if f == nil {
		t.Fatalf("no IO finding; findings = %+v", rep.Findings)
	}
	if strings.Contains(strings.Join(evidenceStrings(f), " "), "节点级") {
		t.Errorf("qualified a number that came from the cgroup's own PSI: %+v", f.Evidence)
	}
}

// The memory ceiling counters are read in order of severity — touching the
// ceiling, invoking the killer, killing — and each carries which file and
// whose allocations it counts.
func TestCgroupMemoryCarriesBreachAndOOMCounts(t *testing.T) {
	cur := probe.Snapshot{
		At: time.Now(),
		Cgroup: probe.Cgroup{
			V2:              true,
			HostCores:       8,
			MemoryMaxKB:     8388608,
			MemoryCurrentKB: 7549747, // 90%
			MemoryBreaches:  7,
			MemoryOOMs:      2,
			OOMKills:        1,
			EventsLocal:     true,
		},
	}
	rep := Analyze(probe.Snapshot{}, cur, Options{})

	f := findArea(rep, "内存配额")
	if f == nil {
		t.Fatalf("no cgroup memory finding; findings = %+v", rep.Findings)
	}
	joined := strings.Join(evidenceStrings(f), " ")
	for _, want := range []string{"max=7", "oom=2", "oom_kill=1", "memory.events.local", "仅本层"} {
		if !strings.Contains(joined, want) {
			t.Errorf("evidence missing %q: %q", want, joined)
		}
	}
}

// The English report has to carry all of this too. Every string the new rules
// produce goes through the same check as the rest of the report: one Chinese
// character in an English run is a promise broken.
func TestCPUThrottleAndScopeInEnglishHaveNoChinese(t *testing.T) {
	cur := throttleSnapshot(300, 1000)
	cur.Pressure = psi(40.0, 17.4)
	cur.K8s = probe.K8sContext{InPod: true, PodUID: "8f2c1a3e-9d4b-4c6e-a1b2-c3d4e5f60718"}
	cur.Cgroup.MemoryMaxKB = 8388608
	cur.Cgroup.MemoryCurrentKB = 8388608
	cur.Cgroup.MemoryBreaches = 7
	cur.Cgroup.MemoryOOMs = 2
	cur.Cgroup.OOMKills = 1
	cur.Cgroup.EventsLocal = true

	rep := Analyze(probe.Snapshot{}, cur, Options{Lang: i18n.EN})
	assertNoChinese(t, rep)

	// The area name is the key the rest of the suite finds findings by, so a
	// translation that renamed it would silently disable these tests.
	if findArea(rep, "CPU quota") == nil {
		t.Errorf("no English CPU quota finding; areas = %v", areaNames(rep))
	}
	if findArea(rep, "memory quota") == nil {
		t.Errorf("no English memory quota finding; areas = %v", areaNames(rep))
	}
	joined := allStrings(rep)
	for _, want := range []string{"node-wide", "neighbouring", "this level only"} {
		if !strings.Contains(joined, want) {
			t.Errorf("English report missing %q", want)
		}
	}
}

func areaNames(rep Report) []string {
	out := make([]string, 0, len(rep.Findings))
	for _, f := range rep.Findings {
		out = append(out, f.Area)
	}
	return out
}

func allStrings(rep Report) string {
	var parts []string
	for _, f := range rep.Findings {
		parts = append(parts, f.Area, f.Headline, f.Advice)
		parts = append(parts, evidenceStrings(&f)...)
	}
	return strings.Join(parts, " ")
}
