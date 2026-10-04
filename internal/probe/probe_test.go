package probe

import (
	"strings"
	"testing"
)

// A process can name itself anything, including a string containing spaces
// and a stray closing parenthesis. Splitting /proc/<pid>/stat on whitespace is
// the classic way to get this wrong, so the parser anchors on the last ')'.
func TestParseProcStatHandlesCommWithSpacesAndParens(t *testing.T) {
	line := []byte("42 (weird ) name) S 1 42 42 0 -1 4194560 100 0 7 0 111 222 " +
		"0 0 20 0 3 0 99 1234 500 18446744073709551615 0 0 0 0 0 0 0 0 0 0 0 0 17 0 0 0 0 0 0")

	st, err := parseProcStat(line)
	if err != nil {
		t.Fatalf("parseProcStat: %v", err)
	}
	if st.Name != "weird ) name" {
		t.Errorf("name = %q, want %q", st.Name, "weird ) name")
	}
	if st.State != StateSleeping {
		t.Errorf("state = %q, want %q", string(st.State), string(StateSleeping))
	}
	if st.MajFlt != 7 {
		t.Errorf("majflt = %d, want 7", st.MajFlt)
	}
	if st.UTime != 111 {
		t.Errorf("utime = %d, want 111", st.UTime)
	}
	if st.STime != 222 {
		t.Errorf("stime = %d, want 222", st.STime)
	}
	if st.NThreads != 3 {
		t.Errorf("num_threads = %d, want 3", st.NThreads)
	}
	if st.RSSPages != 500 {
		t.Errorf("rss = %d, want 500", st.RSSPages)
	}
	if !st.BlkIOKnown {
		t.Error("BlkIOKnown = false, want true — the line carries field 42")
	}
}

// Field 39 is the only link between a process and a specific core, and
// therefore the only thing that lets a per-core finding name a process instead
// of guessing from two unrelated facts.
func TestParseProcStatReadsLastCPU(t *testing.T) {
	line := []byte("42 (x) S 1 42 42 0 -1 4194560 100 0 7 0 111 222 " +
		"0 0 20 0 3 0 99 1234 500 18446744073709551615 0 0 0 0 0 0 0 0 0 0 0 0 17 3 0 0 0 0 0 0")

	st, err := parseProcStat(line)
	if err != nil {
		t.Fatalf("parseProcStat: %v", err)
	}
	if !st.LastCPUKnown {
		t.Fatal("LastCPUKnown = false, want true — the line carries field 39")
	}
	if st.LastCPU != 3 {
		t.Errorf("LastCPU = %d, want 3", st.LastCPU)
	}
}

// A line that ends before field 39 is a missing measurement, not a claim that
// the process ran on core 0 — same rule as field 42.
func TestParseProcStatMarksAbsentLastCPUAsUnknown(t *testing.T) {
	st, err := parseProcStat([]byte("1 (x) S 1 2 3 0 0 0 0 0 0 0 0 0 " +
		"0 0 20 0 3 0 99 1234 500 18446744073709551615"))
	if err != nil {
		t.Fatalf("parseProcStat: %v", err)
	}
	if st.LastCPUKnown {
		t.Errorf("LastCPUKnown = true on a line that ends before field 39")
	}
}

func TestParseProcStatRejectsLineTruncatedBeforeRSS(t *testing.T) {
	if _, err := parseProcStat([]byte("1 (x) S 1 2 3")); err == nil {
		t.Fatal("want an error for a stat line that ends before the rss field")
	}
}

// A counter that fails to parse must be an error, never a silent zero. In a
// monitoring tool a silent zero reads as "disk 0% full", which means no alert.
func TestParseProcStatErrorsOnNonNumericCounter(t *testing.T) {
	line := []byte("42 (x) S 1 42 42 0 -1 4194560 100 0 0 0 abc 222 " +
		"0 0 20 0 3 0 99 1234 500 18446744073709551615 0 0 0 0 0 0 0 0 0 0 0 0 17 0 0 0 0 0 0")
	if _, err := parseProcStat(line); err == nil {
		t.Fatal("want an error when utime is not numeric, got a silent zero")
	}
}

// A kernel built without CONFIG_TASK_DELAY_ACCT omits field 42 entirely. That
// is a missing measurement and must stay distinguishable from a genuine zero —
// the difference between "never blocked" and "cannot tell".
func TestParseProcStatMarksAbsentBlkIOFieldAsUnknown(t *testing.T) {
	line := []byte("42 (x) S 1 42 42 0 -1 4194560 100 0 0 0 111 222 " +
		"0 0 20 0 3 0 99 1234 500 18446744073709551615 0 0 0 0 0 0 0 0")
	st, err := parseProcStat(line)
	if err != nil {
		t.Fatalf("parseProcStat: %v", err)
	}
	if st.BlkIOKnown {
		t.Error("BlkIOKnown = true, but the line stops well before field 42")
	}
}

func TestAtouRejectsGarbage(t *testing.T) {
	if _, err := atou("12x4"); err == nil {
		t.Fatal("atou accepted a non-numeric field")
	}
	if v, err := atou(" 42 "); err != nil || v != 42 {
		t.Fatalf("atou(\" 42 \") = %d, %v; want 42, nil", v, err)
	}
}

// Partitions and virtual devices share a parent's queue, so counting both
// would double-count the same I/O.
func TestWholeDeviceAcceptsDisksAndRejectsPartitions(t *testing.T) {
	whole := []string{"sda", "vda", "xvdh", "nvme0n1", "mmcblk0", "dm-0", "md0"}
	for _, name := range whole {
		if !wholeDevice.MatchString(name) {
			t.Errorf("%q should be treated as a whole device", name)
		}
	}
	other := []string{"sda1", "nvme0n1p1", "loop0", "ram3", "sdaa1"}
	for _, name := range other {
		if wholeDevice.MatchString(name) {
			t.Errorf("%q should not be treated as a whole device", name)
		}
	}
}

func TestDiskSpaceFromStatfs(t *testing.T) {
	// 100 GiB total, 10 GiB available, 4 KiB blocks.
	got := diskSpaceFromStatfs("/", 26214400, 2621440, 4096)

	const wantTotal = 107374182400
	const wantFree = 10737418240
	if got.TotalBytes != wantTotal {
		t.Errorf("total = %d, want %d", got.TotalBytes, wantTotal)
	}
	if got.FreeBytes != wantFree {
		t.Errorf("free = %d, want %d", got.FreeBytes, wantFree)
	}
	if got.UsedBytes != wantTotal-wantFree {
		t.Errorf("used = %d, want %d", got.UsedBytes, wantTotal-wantFree)
	}
	if got.UsedPct != 90 {
		t.Errorf("used%% = %v, want 90", got.UsedPct)
	}
}

func TestDiskSpaceFromStatfsDoesNotWrapWhenFreeExceedsTotal(t *testing.T) {
	got := diskSpaceFromStatfs("/", 10, 20, 4096)
	if got.UsedBytes != 0 {
		t.Errorf("used = %d, want 0 (clamped, not wrapped)", got.UsedBytes)
	}
}

// A kernel older than 2.6.11 prints four counters per core instead of eight.
// Missing columns are genuinely zero, not unparseable.
func TestParseCPUCounterFieldsPadsShortLines(t *testing.T) {
	v, err := parseCPUCounterFields([]string{"10", "0", "20", "70"}, "cpu0")
	if err != nil {
		t.Fatalf("parseCPUCounterFields: %v", err)
	}
	if v.User != 10 || v.System != 20 || v.Idle != 70 {
		t.Errorf("got %+v, want user 10 system 20 idle 70", v)
	}
	if v.Steal != 0 || v.SoftIRQ != 0 {
		t.Errorf("absent columns should be zero, got steal %d softirq %d", v.Steal, v.SoftIRQ)
	}
}

func TestParseCPUCounterFieldsRejectsGarbage(t *testing.T) {
	if _, err := parseCPUCounterFields([]string{"10", "x", "20", "70"}, "cpu0"); err == nil {
		t.Fatal("want an error for a non-numeric counter")
	}
}

// Cores must be matched by ID, not by position: a core can be offlined and
// re-onlined between samples, which renumbers the list, and comparing position
// i to position i would then silently mix up two different cores.
func TestCoreLoadsMatchByIDNotPosition(t *testing.T) {
	prev := []CPUCore{
		{ID: 0, Times: CPUTimes{Idle: 1000}},
		{ID: 1, Times: CPUTimes{Idle: 1000}},
	}
	cur := []CPUCore{
		{ID: 1, Times: CPUTimes{User: 50, Idle: 1050}},
		{ID: 2, Times: CPUTimes{User: 10, Idle: 1090}},
	}

	loads := CoreLoads(prev, cur)
	if len(loads) != 1 {
		t.Fatalf("got %d loads, want 1 — core 2 has no previous sample", len(loads))
	}
	if loads[0].ID != 1 {
		t.Fatalf("load reported for core %d, want 1", loads[0].ID)
	}
	if loads[0].Busy != 50 {
		t.Errorf("busy = %v, want 50", loads[0].Busy)
	}
}

func TestCoreLoadComputesIOWaitAndSteal(t *testing.T) {
	prev := []CPUCore{{ID: 0, Times: CPUTimes{}}}
	cur := []CPUCore{{ID: 0, Times: CPUTimes{User: 40, IOWait: 30, Steal: 10, Idle: 20}}}

	loads := CoreLoads(prev, cur)
	if len(loads) != 1 {
		t.Fatalf("got %d loads, want 1", len(loads))
	}
	if loads[0].Busy != 80 {
		t.Errorf("busy = %v, want 80", loads[0].Busy)
	}
	if loads[0].IOWait != 30 {
		t.Errorf("iowait = %v, want 30", loads[0].IOWait)
	}
	if loads[0].Steal != 10 {
		t.Errorf("steal = %v, want 10", loads[0].Steal)
	}
}

// --- fixture-backed readers ---

const demoCur = "../../testdata/demo/cur"

func TestCountCoresReadsPerCPULines(t *testing.T) {
	n, err := CountCores(NewRoot(demoCur))
	if err != nil {
		t.Fatalf("CountCores: %v", err)
	}
	if n != 4 {
		t.Fatalf("cores = %d, want 4", n)
	}
}

func TestReadPressureParsesBothLines(t *testing.T) {
	p := ReadPressure(NewRoot(demoCur))
	if !p.Supported {
		t.Fatal("Supported = false, want true")
	}
	if got := p.IO.Full.Avg10; got != 17.4 {
		t.Errorf("io full avg10 = %v, want 17.4", got)
	}
	if got := p.IO.Some.Avg10; got != 42.1 {
		t.Errorf("io some avg10 = %v, want 42.1", got)
	}
	// The CPU file has a `some` line only; that is not an error.
	if !p.CPU.HasSome {
		t.Error("cpu some missing")
	}
	if p.CPU.HasFull {
		t.Error("cpu reported a `full` line, but /proc/pressure/cpu has none")
	}
}

// The aggregate must be read from the "cpu " line, not from a core. Asserting
// against the sum of the per-core lines is what distinguishes the two: if the
// parser read cpu0 instead, the total would come out a quarter of this.
func TestReadCPUReadsTheAggregateLine(t *testing.T) {
	agg, cores, err := ReadCPUAll(NewRoot(demoCur))
	if err != nil {
		t.Fatalf("ReadCPUAll: %v", err)
	}
	if len(cores) != 4 {
		t.Fatalf("got %d cores, want 4", len(cores))
	}

	var sum CPUTimes
	for _, c := range cores {
		sum.User += c.Times.User
		sum.System += c.Times.System
		sum.Idle += c.Times.Idle
		sum.IOWait += c.Times.IOWait
		sum.Steal += c.Times.Steal
	}
	if agg.User != sum.User || agg.Idle != sum.Idle || agg.Steal != sum.Steal {
		t.Errorf("aggregate %+v is not the sum of the per-core lines %+v", agg, sum)
	}
}

func TestMemoryDerivesUsedFromAvailable(t *testing.T) {
	m, err := ReadMemory(NewRoot(demoCur))
	if err != nil {
		t.Fatalf("ReadMemory: %v", err)
	}
	if m.TotalKB != 16384000 {
		t.Errorf("total = %d, want 16384000", m.TotalKB)
	}
	if want := uint64(16384000 - 6050000); m.UsedKB() != want {
		t.Errorf("used = %d, want %d", m.UsedKB(), want)
	}
	if m.DirtyKB != 384000 {
		t.Errorf("dirty = %d, want 384000", m.DirtyKB)
	}
}

func TestDiskStatsDeltaConvertsSectorsToBytes(t *testing.T) {
	disks, err := ReadDiskStats(NewRoot(demoCur))
	if err != nil {
		t.Fatalf("ReadDiskStats: %v", err)
	}
	if len(disks) != 1 || disks[0].Name != "sda" {
		t.Fatalf("disks = %+v, want one entry named sda", disks)
	}
	if got := disks[0].BytesRead(); got != 40082000*512 {
		t.Errorf("bytes read = %d, want %d", got, uint64(40082000)*512)
	}
}

func TestReadProcessesResolvesUserAndCmdline(t *testing.T) {
	root := NewRoot(demoCur)
	pids, err := ListPIDs(root)
	if err != nil {
		t.Fatalf("ListPIDs: %v", err)
	}
	if len(pids) != 3 {
		t.Fatalf("pids = %v, want 3", pids)
	}
	procs := ReadProcesses(root, pids, ReadUsers(root))

	byName := map[string]Process{}
	for _, p := range procs {
		byName[p.Name] = p
	}
	mysqld, ok := byName["mysqld"]
	if !ok {
		t.Fatalf("mysqld not found in %v", procs)
	}
	if mysqld.User != "mysql" {
		t.Errorf("user = %q, want mysql (uid 113)", mysqld.User)
	}
	if !mysqld.IOKnown {
		t.Error("IOKnown = false, want true — the fixture provides /proc/<pid>/io")
	}
	if mysqld.Cmdline == "" {
		t.Error("cmdline is empty")
	}
	if mysqld.NThreads != 6 {
		t.Errorf("nthreads = %d, want 6", mysqld.NThreads)
	}
}

func TestReadThreadScanFindsBlockedThreads(t *testing.T) {
	root := NewRoot(demoCur)
	pids, err := ListPIDs(root)
	if err != nil {
		t.Fatalf("ListPIDs: %v", err)
	}
	scan := ReadThreadScan(root, pids)

	if scan.Total != 13 {
		t.Errorf("total threads = %d, want 13 (6 mysqld + 3 node + 4 nginx)", scan.Total)
	}
	if scan.Blocked != 3 {
		t.Errorf("blocked = %d, want 3 (2 mysqld + 1 node)", scan.Blocked)
	}

	blocked := scan.BlockedProcesses()
	if len(blocked) != 2 {
		t.Fatalf("got %d processes with blocked threads, want 2: %+v", len(blocked), blocked)
	}
	// Worst first: mysqld has two blocked threads, node has one.
	if blocked[0].Name != "mysqld" || blocked[0].Blocked != 2 {
		t.Errorf("first blocked process = %+v, want mysqld with 2", blocked[0])
	}
	if blocked[1].Name != "node" || blocked[1].Blocked != 1 {
		t.Errorf("second blocked process = %+v, want node with 1", blocked[1])
	}
}

// A process whose threads are all sleeping normally is not blocked and must
// not appear: reporting a healthy worker as stuck would make the finding
// worthless.
func TestReadThreadScanExcludesHealthyProcesses(t *testing.T) {
	root := NewRoot(demoCur)
	pids, err := ListPIDs(root)
	if err != nil {
		t.Fatalf("ListPIDs: %v", err)
	}
	for _, p := range ReadThreadScan(root, pids).BlockedProcesses() {
		if p.Name == "nginx" {
			t.Errorf("nginx was reported as blocked: %+v", p)
		}
	}
}

func TestReadThreadScanCapturesWChan(t *testing.T) {
	root := NewRoot(demoCur)
	scan := ReadThreadScan(root, []int{1234})

	if len(scan.Processes) != 1 {
		t.Fatalf("got %d processes, want 1", len(scan.Processes))
	}
	blocked := scan.Processes[0].BlockedOn
	if len(blocked) != 2 {
		t.Fatalf("got %d blocked threads, want 2: %+v", len(blocked), blocked)
	}
	byWChan := map[string]bool{}
	for _, th := range blocked {
		if th.State != "D" {
			t.Errorf("thread %d state = %q, want D", th.TID, th.State)
		}
		byWChan[th.WChan] = true
	}
	for _, want := range []string{"jbd2_log_wait_commit", "io_schedule"} {
		if !byWChan[want] {
			t.Errorf("wchan %q missing from %v", want, blocked)
		}
	}
}

func TestReadOpenFilesKeepsRealFilesAndDropsTheRest(t *testing.T) {
	set := ReadOpenFiles(NewRoot(demoCur), 1234, 10)

	// The fixture gives mysqld six descriptors: three real files, one socket,
	// one /dev/null and one anonymous inode.
	if set.Distinct != 3 {
		t.Errorf("distinct = %d, want 3: %+v", set.Distinct, set.Paths)
	}
	joined := strings.Join(set.Paths, " ")
	if !strings.Contains(joined, "/var/lib/mysql/ib_logfile0") {
		t.Errorf("the redo log is missing from %v", set.Paths)
	}
}

func TestReadOpenFilesReportsTruncation(t *testing.T) {
	set := ReadOpenFiles(NewRoot(demoCur), 1234, 1)
	if len(set.Paths) != 1 {
		t.Fatalf("got %d paths, want 1", len(set.Paths))
	}
	if set.Distinct != 3 {
		t.Errorf("distinct = %d, want 3 — a cap must not hide the real count", set.Distinct)
	}
	if !set.Truncated() {
		t.Error("Truncated() = false, but only 1 of 3 files was returned")
	}
}

func TestIsInterestingPathRejectsTheNoiseInARealDescriptorTable(t *testing.T) {
	noise := []string{
		"socket:[284917]",
		"pipe:[918273]",
		"anon_inode:[eventpoll]",
		"/dev/null",
		"/dev/sda",
		"/proc/1234/status",
		"/sys/kernel/mm/transparent_hugepage/enabled",
		"/run/systemd/notify",
	}
	for _, p := range noise {
		if isInterestingPath(p) {
			t.Errorf("%q should not be reported as a file a process is working on", p)
		}
	}
	keep := []string{
		"/var/lib/mysql/ib_logfile0",
		"/home/app/server.js",
		"/var/log/app/app.log (deleted)",
	}
	for _, p := range keep {
		if !isInterestingPath(p) {
			t.Errorf("%q should be kept", p)
		}
	}
}

func TestReadWChanTreatsZeroAsRunning(t *testing.T) {
	root := NewRoot(demoCur)
	// The fixture stores wchan as a regular file, which ReadFile reads directly.
	if got := readWChan(root, "proc/1234/task/1240/wchan"); got != "jbd2_log_wait_commit" {
		t.Errorf("wchan = %q, want jbd2_log_wait_commit", got)
	}
	if got := readWChan(root, "proc/1234/task/1234/wchan"); got != "" {
		t.Errorf("wchan = %q, want empty when the kernel reports 0", got)
	}
}
