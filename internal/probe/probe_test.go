package probe

import "testing"

// A process can name itself anything, including a string containing spaces
// and a stray closing parenthesis. Splitting /proc/<pid>/stat on whitespace is
// the classic way to get this wrong, so the parser anchors on the last ')'.
func TestParseProcStatHandlesCommWithSpacesAndParens(t *testing.T) {
	line := []byte("42 (weird ) name) S 1 42 42 0 -1 4194560 100 0 7 0 111 222 " +
		"0 0 20 0 3 0 99 1234 500 18446744073709551615 0 0 0 0 0 0 0 0 0 0 0 0 17 0 0 0 0 0 0")

	name, utime, stime, majflt, rss, err := parseProcStat(line)
	if err != nil {
		t.Fatalf("parseProcStat: %v", err)
	}
	if name != "weird ) name" {
		t.Errorf("name = %q, want %q", name, "weird ) name")
	}
	if utime != 111 {
		t.Errorf("utime = %d, want 111", utime)
	}
	if stime != 222 {
		t.Errorf("stime = %d, want 222", stime)
	}
	if majflt != 7 {
		t.Errorf("majflt = %d, want 7", majflt)
	}
	if rss != 500 {
		t.Errorf("rss = %d, want 500", rss)
	}
}

func TestParseProcStatRejectsLineTruncatedBeforeRSS(t *testing.T) {
	if _, _, _, _, _, err := parseProcStat([]byte("1 (x) S 1 2 3")); err == nil {
		t.Fatal("want an error for a stat line that ends before the rss field")
	}
}

// A counter that fails to parse must be an error, never a silent zero. In a
// monitoring tool a silent zero reads as "disk 0% full", which means no alert.
func TestParseProcStatErrorsOnNonNumericCounter(t *testing.T) {
	line := []byte("42 (x) S 1 42 42 0 -1 4194560 100 0 0 0 abc 222 " +
		"0 0 20 0 3 0 99 1234 500 18446744073709551615 0 0 0 0 0 0 0 0 0 0 0 0 17 0 0 0 0 0 0")
	if _, _, _, _, _, err := parseProcStat(line); err == nil {
		t.Fatal("want an error when utime is not numeric, got a silent zero")
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

func TestReadCPUUsesAggregateLineNotPerCore(t *testing.T) {
	cpu, err := ReadCPU(NewRoot(demoCur))
	if err != nil {
		t.Fatalf("ReadCPU: %v", err)
	}
	if cpu.Idle != 1400900 {
		t.Fatalf("idle = %d, want the aggregate value 1400900", cpu.Idle)
	}
	if cpu.Steal != 5060 {
		t.Fatalf("steal = %d, want 5060", cpu.Steal)
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
}
