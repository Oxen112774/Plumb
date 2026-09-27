package diagnose

import (
	"strings"
	"testing"
	"time"

	"github.com/Oxen112774/plumb/internal/probe"
)

// A database with two threads stuck on the journal commit, holding its redo
// log open. This is the shape the whole feature exists for.
func blockedSnapshot() probe.Snapshot {
	return probe.Snapshot{
		At: time.Now(),
		Threads: probe.ThreadScan{
			Processes: []probe.ProcessThreads{{
				PID: 1234, Name: "mysqld", Total: 4, Blocked: 2,
				BlockedOn: []probe.Thread{
					{TID: 1240, Comm: "mysqld", State: "D", WChan: "jbd2_log_wait_commit"},
					{TID: 1241, Comm: "mysqld", State: "D", WChan: "io_schedule"},
				},
			}},
			Total: 10, Blocked: 2,
		},
		OpenFiles: map[int]probe.OpenFileSet{
			1234: {Paths: []string{"/var/lib/mysql/ib_logfile0"}, Distinct: 1},
		},
	}
}

func TestBlockedThreadsNameTheWaitAndTheFile(t *testing.T) {
	rep := Analyze(probe.Snapshot{}, blockedSnapshot(), Options{})

	f := findArea(rep, "被堵住的线程")
	if f == nil {
		t.Fatalf("no blocked-thread finding; findings = %+v", rep.Findings)
	}
	if f.Severity != SeverityWatch {
		t.Errorf("severity = %q, want %q", f.Severity, SeverityWatch)
	}
	if len(f.Culprits) != 1 {
		t.Fatalf("got %d culprits, want 1: %+v", len(f.Culprits), f.Culprits)
	}

	c := f.Culprits[0]
	if c.PID != 1234 || c.Name != "mysqld" {
		t.Errorf("culprit = %+v, want pid 1234 mysqld", c)
	}
	// The raw symbol has to survive: the translation is this tool's reading of
	// the kernel, and the reader must be able to check it.
	for _, want := range []string{"jbd2_log_wait_commit", "io_schedule", "/var/lib/mysql/ib_logfile0"} {
		if !strings.Contains(c.Note, want) {
			t.Errorf("note is missing %q:\n%s", want, c.Note)
		}
	}
}

// Threads blocked on storage *and* PSI confirming that IO is stalling tasks is
// a stronger claim than either measurement alone.
func TestBlockedThreadsEscalateWhenIOIsStalling(t *testing.T) {
	cur := blockedSnapshot()
	cur.Pressure = psi(42.1, 17.4)

	f := findArea(Analyze(probe.Snapshot{}, cur, Options{}), "被堵住的线程")
	if f == nil {
		t.Fatal("no blocked-thread finding")
	}
	if f.Severity != SeverityWarning {
		t.Errorf("severity = %q, want %q — the pressure data corroborates the blocked threads",
			f.Severity, SeverityWarning)
	}
}

// D state is real evidence even when the wait point is not storage, but it is
// not a problem claim: a driver may sleep uninterruptibly for a routine poll.
// It must be reported without failing a health check.
func TestBlockedThreadsOnANonStorageWaitStayInformational(t *testing.T) {
	cur := probe.Snapshot{
		At: time.Now(),
		Threads: probe.ThreadScan{
			Processes: []probe.ProcessThreads{{
				PID: 2048, Name: "nginx", Total: 4, Blocked: 1,
				BlockedOn: []probe.Thread{{TID: 2049, State: "D", WChan: "msleep"}},
			}},
			Total: 4, Blocked: 1,
		},
	}
	rep := Analyze(probe.Snapshot{}, cur, Options{})

	f := findArea(rep, "被堵住的线程")
	if f == nil {
		t.Fatal("a D-state thread must be reported even when its wait point is not storage")
	}
	if f.Severity != SeverityInfo {
		t.Errorf("severity = %q, want %q", f.Severity, SeverityInfo)
	}
	if rep.HasProblems() {
		t.Error("HasProblems() = true for an informational finding")
	}
}

func TestNoBlockedThreadsNoFinding(t *testing.T) {
	cur := probe.Snapshot{
		At: time.Now(),
		Threads: probe.ThreadScan{
			Processes: []probe.ProcessThreads{{PID: 1, Name: "init", Total: 1}},
			Total:     1,
		},
	}
	if f := findArea(Analyze(probe.Snapshot{}, cur, Options{}), "被堵住的线程"); f != nil {
		t.Errorf("reported blocked threads on a machine with none: %+v", f)
	}
}

// One core at 99% of four reads as 25% busy, which is why the average never
// shows this.
func skewedSnapshots() (probe.Snapshot, probe.Snapshot) {
	prev := probe.Snapshot{
		At:       time.Now().Add(-time.Second),
		CPU:      probe.CPUTimes{Idle: 1000},
		CPUCores: []probe.CPUCore{{ID: 0}, {ID: 1}, {ID: 2}, {ID: 3}},
	}
	cur := probe.Snapshot{
		At:  time.Now(),
		CPU: probe.CPUTimes{User: 99, Idle: 1301},
		CPUCores: []probe.CPUCore{
			{ID: 0, Times: probe.CPUTimes{User: 99, Idle: 1}},
			{ID: 1, Times: probe.CPUTimes{Idle: 100}},
			{ID: 2, Times: probe.CPUTimes{Idle: 100}},
			{ID: 3, Times: probe.CPUTimes{Idle: 100}},
		},
	}
	return prev, cur
}

func TestCoreSkewIsReportedWhenTheMachineAverageIsLow(t *testing.T) {
	prev, cur := skewedSnapshots()
	rep := Analyze(prev, cur, Options{Interval: time.Second})

	f := findArea(rep, "单核跑满")
	if f == nil {
		t.Fatalf("no core-skew finding; findings = %+v", rep.Findings)
	}
	if !strings.Contains(f.Headline, "CPU0") {
		t.Errorf("headline does not name the saturated core: %q", f.Headline)
	}
	if len(f.Evidence) == 0 {
		t.Error("a finding must cite at least one measurement")
	}
	// The per-core view has to reach the report, or nothing downstream can
	// show which core it was.
	if len(rep.Cores) != 4 || rep.Cores[0].Busy != 99 {
		t.Errorf("report.Cores = %+v, want 4 cores with CPU0 at 99%%", rep.Cores)
	}
}

// When the whole machine is busy this is not a skew problem, and the CPU
// pressure rule already covers it.
func TestCoreSkewIsNotReportedWhenEveryCoreIsBusy(t *testing.T) {
	prev, cur := skewedSnapshots()
	for i := range cur.CPUCores {
		cur.CPUCores[i] = probe.CPUCore{ID: i, Times: probe.CPUTimes{User: 99, Idle: 1}}
	}
	cur.CPU = probe.CPUTimes{User: 396, Idle: 1004}
	cur.Pressure = psi(8, 0)

	rep := Analyze(prev, cur, Options{Interval: time.Second})
	if f := findArea(rep, "单核跑满"); f != nil {
		t.Errorf("reported a skew on a machine with no idle cores: %+v", f)
	}
}

// USER_HZ is 100, so 100 ticks in one second is one core's worth of CPU time.
func TestCoresUsed(t *testing.T) {
	if got := CoresUsed(110, time.Second); got != 1.1 {
		t.Errorf("CoresUsed(110, 1s) = %v, want 1.1", got)
	}
	if got := CoresUsed(45, time.Second); got != 0.45 {
		t.Errorf("CoresUsed(45, 1s) = %v, want 0.45", got)
	}
	if got := CoresUsed(50, 500*time.Millisecond); got != 1 {
		t.Errorf("CoresUsed(50, 500ms) = %v, want 1", got)
	}
}

// Every one of these eventually calls io_schedule, so the table's order is
// what stops them all collapsing into the generic "waiting for the disk" and
// losing the reason.
func TestTranslateWChanPrefersTheSpecificReason(t *testing.T) {
	cases := map[string]WaitKind{
		"balance_dirty_pages":  WaitWriteback,
		"jbd2_log_wait_commit": WaitFlush,
		"ext4_sync_file":       WaitFlush,
		"filemap_fault":        WaitDiskIO,
		"io_schedule":          WaitDiskIO,
		"io_schedule_timeout":  WaitDiskIO,
		"try_to_free_pages":    WaitReclaim,
		"futex_wait_queue_me":  WaitLock,
		"ep_poll":              WaitNormal,
	}
	for symbol, want := range cases {
		if got := TranslateWChan(symbol).Kind; got != want {
			t.Errorf("TranslateWChan(%q).Kind = %v, want %v", symbol, got, want)
		}
	}
}

// An unrecognised symbol is reported as unrecognised, and kept. Guessing would
// be worse than saying nothing: someone is about to act on this.
func TestTranslateWChanKeepsUnknownSymbols(t *testing.T) {
	const symbol = "some_kernel_function_from_the_future"
	got := TranslateWChan(symbol)

	if got.Kind != WaitUnknown {
		t.Errorf("kind = %v, want WaitUnknown", got.Kind)
	}
	if got.Symbol != symbol {
		t.Errorf("symbol = %q, want %q kept for the reader to look up", got.Symbol, symbol)
	}
	if !strings.Contains(got.Short, symbol) {
		t.Errorf("short text %q does not name the symbol", got.Short)
	}
	if got.Blocking() {
		t.Error("an unknown wait must not be counted as a storage stall")
	}
}

func TestBlockingOnlyForStorageAndMemoryWaits(t *testing.T) {
	for _, k := range []WaitKind{WaitDiskIO, WaitFlush, WaitWriteback, WaitReclaim} {
		if !(Wait{Kind: k}).Blocking() {
			t.Errorf("kind %v should count as blocked", k)
		}
	}
	for _, k := range []WaitKind{WaitNormal, WaitLock, WaitNetwork, WaitUnknown} {
		if (Wait{Kind: k}).Blocking() {
			t.Errorf("kind %v should not count as blocked", k)
		}
	}
}
