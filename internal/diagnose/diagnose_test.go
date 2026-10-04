package diagnose

import (
	"testing"
	"time"

	"github.com/Oxen112774/plumb/internal/probe"
)

func findArea(r Report, area string) *Finding {
	for i := range r.Findings {
		if r.Findings[i].Area == area {
			return &r.Findings[i]
		}
	}
	return nil
}

func psi(some, full float64) probe.Pressure {
	return probe.Pressure{
		Supported: true,
		CPU: probe.PSIStall{
			Some: probe.PSIValues{Avg10: some}, HasSome: true,
		},
		IO: probe.PSIStall{
			Some:    probe.PSIValues{Avg10: some},
			Full:    probe.PSIValues{Avg10: full},
			HasSome: true, HasFull: true,
		},
		Memory: probe.PSIStall{
			Some:    probe.PSIValues{Avg10: some},
			Full:    probe.PSIValues{Avg10: full},
			HasSome: true, HasFull: true,
		},
	}
}

func TestIOPressureAtFullCriticalIsCritical(t *testing.T) {
	cur := probe.Snapshot{At: time.Now(), Pressure: psi(42.1, 17.4)}
	rep := Analyze(probe.Snapshot{}, cur, Options{})

	f := findArea(rep, "磁盘 IO")
	if f == nil {
		t.Fatalf("no IO finding; findings = %+v", rep.Findings)
	}
	if f.Severity != SeverityCritical {
		t.Errorf("severity = %q, want %q", f.Severity, SeverityCritical)
	}
	if len(f.Evidence) == 0 {
		t.Error("a non-OK finding must cite at least one measurement")
	}
}

func TestIOPressureFromSomeOnlyIsNotCritical(t *testing.T) {
	// A `some` value alone means individual tasks were delayed, not that the
	// machine stopped. It must not be reported as critically as a `full`.
	cur := probe.Snapshot{At: time.Now(), Pressure: psi(20, 0)}
	rep := Analyze(probe.Snapshot{}, cur, Options{})

	f := findArea(rep, "磁盘 IO")
	if f == nil {
		t.Fatal("no IO finding for some avg10 = 20")
	}
	if f.Severity != SeverityWatch {
		t.Errorf("severity = %q, want %q", f.Severity, SeverityWatch)
	}
}

// Stolen CPU is invisible to everything inside the guest: top shows idle, the
// application shows latency, and no amount of tuning helps.
func TestStealIsReported(t *testing.T) {
	prev := probe.Snapshot{
		At:  time.Now().Add(-time.Second),
		CPU: probe.CPUTimes{Idle: 1000},
	}
	cur := probe.Snapshot{
		At:  time.Now(),
		CPU: probe.CPUTimes{User: 100, Idle: 1400, Steal: 50},
	}
	rep := Analyze(prev, cur, Options{Interval: time.Second})

	f := findArea(rep, "CPU 被抢占")
	if f == nil {
		t.Fatalf("steal not reported; findings = %+v", rep.Findings)
	}
	// Total delta 550 ticks, of which 50 were stolen: 9.09%.
	if f.Severity != SeverityWatch {
		t.Errorf("severity = %q, want %q for 9.09%% steal", f.Severity, SeverityWatch)
	}
}

func TestHealthyMachineProducesNoFindings(t *testing.T) {
	prev := probe.Snapshot{
		At:  time.Now().Add(-time.Second),
		CPU: probe.CPUTimes{Idle: 1000},
	}
	cur := probe.Snapshot{
		At:  time.Now(),
		CPU: probe.CPUTimes{User: 10, Idle: 1090}, // 10% busy
		Memory: probe.Memory{
			TotalKB: 8192000, AvailableKB: 6000000, SwapTotalKB: 0,
		},
		Pressure: psi(1, 0),
		RootFS:   probe.DiskSpace{Path: "/", TotalBytes: 100, FreeBytes: 50, UsedBytes: 50, UsedPct: 50},
	}
	rep := Analyze(prev, cur, Options{Interval: time.Second})

	if len(rep.Findings) != 0 {
		t.Fatalf("a healthy machine produced findings: %+v", rep.Findings)
	}
	if rep.HasProblems() {
		t.Error("HasProblems() = true on a healthy machine")
	}
}

func TestSingleSampleModeSkipsRateBasedRules(t *testing.T) {
	cur := probe.Snapshot{
		At:       time.Now(),
		Pressure: psi(42.1, 17.4),
		Procs:    []probe.Process{{PID: 1, Name: "x", ReadBytes: 999, IOKnown: true}},
	}
	rep := Analyze(probe.Snapshot{}, cur, Options{})

	if !rep.SingleSample {
		t.Fatal("SingleSample = false, want true when prev is the zero value")
	}
	f := findArea(rep, "磁盘 IO")
	if f == nil {
		t.Fatal("no IO finding")
	}
	if len(f.Culprits) != 0 {
		t.Errorf("got %d culprits with no previous sample, want 0", len(f.Culprits))
	}
}

// Naming a process as the cause of an incident is an accusation; a process
// moving three orders of magnitude less I/O than the leader is noise.
func TestCulpritFloorFiltersNoise(t *testing.T) {
	prev := probe.Snapshot{
		At:  time.Now().Add(-time.Second),
		CPU: probe.CPUTimes{Idle: 1000},
		Procs: []probe.Process{
			{PID: 1, Name: "heavy", ReadBytes: 1000, IOKnown: true},
			{PID: 2, Name: "light1", ReadBytes: 10, IOKnown: true},
			{PID: 3, Name: "light2", ReadBytes: 5, IOKnown: true},
		},
	}
	cur := probe.Snapshot{
		At:  time.Now(),
		CPU: probe.CPUTimes{Idle: 1000},
		Procs: []probe.Process{
			{PID: 1, Name: "heavy", ReadBytes: 1000 + 42_000_000, IOKnown: true},
			{PID: 2, Name: "light1", ReadBytes: 10 + 10_000, IOKnown: true},
			{PID: 3, Name: "light2", ReadBytes: 5 + 5_000, IOKnown: true},
		},
		Pressure: psi(42.1, 17.4),
	}
	rep := Analyze(prev, cur, Options{Interval: time.Second, Top: 3})

	f := findArea(rep, "磁盘 IO")
	if f == nil {
		t.Fatal("no IO finding")
	}
	if len(f.Culprits) != 1 {
		t.Fatalf("got %d culprits, want 1 — sub-threshold processes must be dropped: %+v",
			len(f.Culprits), f.Culprits)
	}
	if f.Culprits[0].Name != "heavy" {
		t.Errorf("culprit = %q, want heavy", f.Culprits[0].Name)
	}
}

// A leader whose activity is below the divisor would floor to zero, and a floor
// of zero lets a process that did nothing at all clear it — the heading then
// names the idle as suspects.
func TestCulpritFloorIsNeverZero(t *testing.T) {
	if got := floorOf(3); got != 1 {
		t.Errorf("floorOf(3) = %d, want 1", got)
	}
	if got := floorOf(4000); got != 200 {
		t.Errorf("floorOf(4000) = %d, want 200", got)
	}
}

// A running machine always accumulates jiffies, so a zero aggregate means
// /proc/stat could not be read. Reporting 0.0% would be a fabricated reading.
func TestCPUIsMarkedUnmeasuredOnlyWhenUnreadable(t *testing.T) {
	prev := probe.Snapshot{At: time.Now().Add(-time.Second), CPU: probe.CPUTimes{Idle: 1000}}

	read := probe.Snapshot{At: time.Now(), CPU: probe.CPUTimes{Idle: 1000}}
	if rep := Analyze(prev, read, Options{Interval: time.Second}); !rep.CPUMeasured {
		t.Error("CPUMeasured = false after a readable /proc/stat")
	}
	unread := probe.Snapshot{At: time.Now()}
	if rep := Analyze(prev, unread, Options{Interval: time.Second}); rep.CPUMeasured {
		t.Error("CPUMeasured = true for a zero aggregate")
	}
}

// USER_HZ is 100, so one tick per second is one percent of one core.
func TestPercentOfCore(t *testing.T) {
	if got := PercentOfCore(110, time.Second); got != 110 {
		t.Errorf("PercentOfCore(110, 1s) = %v, want 110", got)
	}
	if got := PercentOfCore(50, 500*time.Millisecond); got != 100 {
		t.Errorf("PercentOfCore(50, 500ms) = %v, want 100", got)
	}
	if got := PercentOfCore(10, 0); got != 0 {
		t.Errorf("PercentOfCore with a zero interval = %v, want 0", got)
	}
}

func TestFindingsAreOrderedWorstFirst(t *testing.T) {
	cur := probe.Snapshot{
		At:       time.Now(),
		Pressure: psi(42.1, 17.4), // critical IO
		Memory: probe.Memory{
			TotalKB: 1000, AvailableKB: 500, SwapTotalKB: 1000, SwapFreeKB: 900, // 10% swap -> warning
		},
	}
	rep := Analyze(probe.Snapshot{}, cur, Options{})
	for i := 1; i < len(rep.Findings); i++ {
		if rep.Findings[i-1].Severity.Rank() > rep.Findings[i].Severity.Rank() {
			t.Fatalf("findings out of order: %q before %q",
				rep.Findings[i-1].Severity, rep.Findings[i].Severity)
		}
	}
}

// A stored snapshot's timestamp is when it was *read*, not the window its
// counters span. Two archives opened back to back are microseconds apart, so
// using that gap as the divisor inflates every rate by the ratio of the real
// window to the read time — on the demo fixtures that turned 40 MiB/s into
// 909 MiB/s, right in the headline example of the README.
func TestArchiveIntervalUsesTheNominalWindow(t *testing.T) {
	// 100 ticks of new work. CPU ticks count hundredths of a second, so over
	// a nominal 1s window that is one full core — and over a 3s gap the same
	// 100 ticks would read as a third of a core.
	prev := probe.Snapshot{At: time.Now()}
	cur := probe.Snapshot{At: time.Now(), CPU: probe.CPUTimes{User: 100}}

	t.Run("archive", func(t *testing.T) {
		// The archive is opened a millisecond after the current tree — a gap
		// that says nothing about the window its counters span.
		p2, c2 := prev, cur
		c2.At = p2.At.Add(time.Millisecond)
		rep := Analyze(p2, c2, Options{Interval: time.Second, PrevIsArchive: true})

		if rep.Interval != time.Second {
			t.Errorf("archive interval = %v, want the nominal 1s", rep.Interval)
		}
		if got := PercentOfCore(100, rep.Interval); got != 100 {
			t.Errorf("core usage = %v%%, want 100%% — reading an archive must not rescale the rate", got)
		}
	})

	t.Run("sampled", func(t *testing.T) {
		// Two live samples 3s apart: the real gap is the honest denominator,
		// because the counters really did accumulate over it.
		p2, c2 := prev, cur
		c2.At = p2.At.Add(3 * time.Second)
		rep := Analyze(p2, c2, Options{Interval: time.Second, PrevIsArchive: false})

		if rep.Interval != 3*time.Second {
			t.Errorf("sampled interval = %v, want the real 3s", rep.Interval)
		}
		if got := PercentOfCore(100, rep.Interval); got == 100 {
			t.Error("sampled core usage = 100%; the 3s gap must shrink it")
		}
	})
}
