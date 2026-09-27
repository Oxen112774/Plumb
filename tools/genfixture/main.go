// Command genfixture writes the demonstration fixture tree under testdata/demo.
//
// The fixture exists so plumb can be run and its output inspected on a machine
// that is not Linux — the development machine is Windows. It describes a
// plausible incident rather than a synthetic one: a database host whose CPU is
// only about 40% busy while the disk stalls every task on the machine, a
// single-threaded application pinning one core solid, and a hypervisor taking
// a slice of the CPU on top.
//
// The detail that matters most is invisible to a throughput ranking: mysqld
// has two threads in D state, stuck on the journal commit. Blocked threads
// transfer almost no data, so a tool that sorts processes by I/O per second
// reports the most blocked process on the machine as idle.
//
// Every number is chosen so the arithmetic works out to a readable rate over a
// one-second window, and the aggregate CPU line is computed as the sum of the
// per-core lines rather than written out by hand — the kernel guarantees that
// identity, and a fixture that violates it would hide a real bug.
// Regenerate with:
//
//	go run ./tools/genfixture
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

func main() {
	const root = "testdata/demo"
	if err := os.RemoveAll(root); err != nil {
		fail(err)
	}
	for _, t := range []struct {
		name string
		v    snap
	}{
		{"prev", prevSnap()},
		{"cur", curSnap()},
	} {
		if err := writeTree(filepath.Join(root, t.name), buildTree(t.v)); err != nil {
			fail(err)
		}
	}
	fmt.Println("wrote", root)
}

func writeTree(base string, files map[string]string) error {
	for rel, content := range files {
		p := filepath.Join(base, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			return err
		}
	}
	return nil
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "genfixture:", err)
	os.Exit(1)
}

// coreTimes is one core's counters: user, system, idle, iowait, steal.
type coreTimes [5]uint64

// coreDeltas is the change over the one-second window, per core.
//
// Core 0 is pinned by `node`, which is single-threaded: its garbage collector
// has one core at 99% while the machine average sits near 42%. That is the
// shape a per-core view catches and an average cannot.
var coreDeltas = []coreTimes{
	{84, 10, 1, 0, 5},
	{7, 4, 74, 10, 5},
	{5, 3, 77, 10, 5},
	{4, 3, 78, 10, 5},
}

// coreBase is the counters as of the earlier sample.
var coreBase = []coreTimes{
	{480000, 120000, 350000, 45000, 1250},
	{479000, 119000, 350500, 45000, 1240},
	{481000, 121000, 349800, 44800, 1260},
	{480180, 120150, 349900, 45180, 1310},
}

// sumCores adds the per-core counters, which is what the kernel's aggregate
// "cpu " line reports.
func sumCores(cores []coreTimes) coreTimes {
	var out coreTimes
	for _, c := range cores {
		for i := range out {
			out[i] += c[i]
		}
	}
	return out
}

func addCores(base, delta []coreTimes) []coreTimes {
	out := make([]coreTimes, len(base))
	for i := range base {
		for j := range out[i] {
			out[i][j] = base[i][j] + delta[i][j]
		}
	}
	return out
}

// procStat renders /proc/stat from the per-core rows, deriving the aggregate
// line rather than restating it.
func procStat(cores []coreTimes) string {
	var b strings.Builder

	agg := sumCores(cores)
	fmt.Fprintf(&b, "cpu  %d 0 %d %d %d 0 0 %d 0 0\n",
		agg[0], agg[1], agg[2], agg[3], agg[4])
	for i, c := range cores {
		// Fields: user nice system idle iowait irq softirq steal guest guest_nice
		fmt.Fprintf(&b, "cpu%d %d 0 %d %d %d 0 0 %d 0 0\n",
			i, c[0], c[1], c[2], c[3], c[4])
	}
	b.WriteString(`intr 1234567890 12 0 0
ctxt 987654321
btime 1700000000
processes 1234567
procs_running 2
procs_blocked 2
`)
	return b.String()
}

// snap holds the values that differ between the two samples.
type snap struct {
	cores []coreTimes

	uptime string
	load   string

	memAvailable, dirty, writeback string

	ioSome, ioFull   string
	cpuSome          string
	memSome, memFull string

	devReads, devSectorsRead     string
	devWrites, devSectorsWritten string
	devInFlight, devMillis       string

	mysqlUTime, mysqlSTime, mysqlRSS string
	mysqlRead, mysqlWrite            string
	mysqlRChar, mysqlWChar           string

	nodeUTime, nodeSTime, nodeRSS string
	nodeRead, nodeWrite           string

	nginxUTime, nginxSTime, nginxRSS string
	nginxRead, nginxWrite            string
}

func prevSnap() snap {
	return snap{
		cores: coreBase,

		uptime: "1036800.42", load: "2.14 1.87 1.42 2/318 41234",

		memAvailable: "6200000", dirty: "180000", writeback: "4200",

		ioSome: "38.40", ioFull: "15.10",
		cpuSome: "7.80",
		memSome: "7.90", memFull: "2.90",

		devReads: "1000000", devSectorsRead: "40000000",
		devWrites: "500000", devSectorsWritten: "20000000",
		devInFlight: "4", devMillis: "1600000",

		mysqlUTime: "1200", mysqlSTime: "300", mysqlRSS: "690000",
		mysqlRead: "8000000000", mysqlWrite: "2000000000",
		mysqlRChar: "9000000000", mysqlWChar: "2500000000",

		nodeUTime: "8000", nodeSTime: "2000", nodeRSS: "1190000",
		nodeRead: "100000", nodeWrite: "200000",

		nginxUTime: "300", nginxSTime: "100", nginxRSS: "5000",
		nginxRead: "50000000", nginxWrite: "10000000",
	}
}

func curSnap() snap {
	return snap{
		cores: addCores(coreBase, coreDeltas),

		uptime: "1036801.42", load: "2.21 1.90 1.44 3/319 41877",

		memAvailable: "6050000", dirty: "384000", writeback: "18000",

		ioSome: "42.10", ioFull: "17.40",
		cpuSome: "8.20",
		memSome: "8.60", memFull: "3.20",

		devReads: "1000200", devSectorsRead: "40082000",
		devWrites: "5000200", devSectorsWritten: "20002000",
		devInFlight: "7", devMillis: "1610200",

		mysqlUTime: "1235", mysqlSTime: "310", mysqlRSS: "700000",
		mysqlRead: "8042000000", mysqlWrite: "2001000000",
		mysqlRChar: "9004000000", mysqlWChar: "2501000000",

		nodeUTime: "8080", nodeSTime: "2030", nodeRSS: "1200000",
		nodeRead: "110000", nodeWrite: "210000",

		nginxUTime: "308", nginxSTime: "104", nginxRSS: "5000",
		nginxRead: "50010000", nginxWrite: "10005000",
	}
}

func buildTree(v snap) map[string]string {
	files := map[string]string{
		"etc/passwd": `root:x:0:0:root:/root:/bin/bash
daemon:x:1:1:daemon:/usr/sbin:/usr/sbin/nologin
www-data:x:33:33:www-data:/var/www:/usr/sbin/nologin
mysql:x:113:113:MySQL Server:/nonexistent:/bin/false
app:x:1000:1000:app:/home/app:/bin/bash
`,

		"proc/sys/kernel/hostname":  "prod-db-01\n",
		"proc/sys/kernel/osrelease": "5.15.0-91-generic\n",

		"proc/uptime":  v.uptime + " 9876543.21\n",
		"proc/loadavg": v.load + "\n",

		"proc/stat": procStat(v.cores),

		"proc/meminfo": fmt.Sprintf(`MemTotal:       16384000 kB
MemFree:          820000 kB
MemAvailable:    %s kB
Buffers:          210000 kB
Cached:          5100000 kB
SwapCached:            0 kB
Active:          7200000 kB
Inactive:        4800000 kB
SwapTotal:       2097152 kB
SwapFree:        1900000 kB
Dirty:           %s kB
Writeback:       %s kB
`, v.memAvailable, v.dirty, v.writeback),

		"proc/diskstats": fmt.Sprintf(
			"   8       0 sda %s 500000 %s 900000 %s 300000 %s 800000 %s %s 1800000 0 0 0 0\n",
			v.devReads, v.devSectorsRead, v.devWrites, v.devSectorsWritten,
			v.devInFlight, v.devMillis),

		"proc/pressure/cpu": fmt.Sprintf(
			"some avg10=%s avg60=6.10 avg300=4.20 total=3210445\n", v.cpuSome),

		"proc/pressure/io": fmt.Sprintf(
			"some avg10=%s avg60=31.20 avg300=18.40 total=18430122\nfull avg10=%s avg60=12.10 avg300=6.80 total=7210455\n",
			v.ioSome, v.ioFull),

		"proc/pressure/memory": fmt.Sprintf(
			"some avg10=%s avg60=5.20 avg300=2.10 total=2010445\nfull avg10=%s avg60=1.40 avg300=0.60 total=712300\n",
			v.memSome, v.memFull),

		// --- the database: the block-I/O culprit, not the CPU or memory one ---
		"proc/1234/stat":    statLine(1234, "mysqld", 'S', v.mysqlUTime, v.mysqlSTime, "0", 6, v.mysqlRSS, "120"),
		"proc/1234/status":  statusBlock("mysqld", 1234, 113, 6),
		"proc/1234/cmdline": "/usr/sbin/mysqld\x00--defaults-file=/etc/mysql/my.cnf",
		"proc/1234/io": fmt.Sprintf(`rchar: %s
wchar: %s
syscr: 5000000
syscw: 900000
read_bytes: %s
write_bytes: %s
cancelled_write_bytes: 0
`, v.mysqlRChar, v.mysqlWChar, v.mysqlRead, v.mysqlWrite),

		// Two of mysqld's threads are in D state on the journal commit. They
		// move almost no data while blocked, which is exactly why a
		// throughput ranking cannot find them.
		"proc/1234/task/1234/stat":  statLine(1234, "mysqld", 'S', "900", "240", "0", 6, "620000", "120"),
		"proc/1234/task/1240/stat":  statLine(1240, "mysqld", 'D', "300", "60", "3", 6, "40000", "8900"),
		"proc/1234/task/1241/stat":  statLine(1241, "mysqld", 'D', "280", "55", "2", 6, "38000", "6400"),
		"proc/1234/task/1242/stat":  statLine(1242, "mysqld", 'S', "90", "12", "0", 6, "1200", "40"),
		"proc/1234/task/1243/stat":  statLine(1243, "mysqld", 'S', "40", "6", "0", 6, "600", "20"),
		"proc/1234/task/1244/stat":  statLine(1244, "mysqld", 'S', "5", "1", "0", 6, "200", "0"),
		"proc/1234/task/1240/wchan": "jbd2_log_wait_commit\n",
		"proc/1234/task/1241/wchan": "io_schedule\n",

		// --- the application: nominal I/O, by far the largest resident set ---
		"proc/3000/stat":    statLine(3000, "node", 'R', v.nodeUTime, v.nodeSTime, "12", 3, v.nodeRSS, "60"),
		"proc/3000/status":  statusBlock("node", 3000, 1000, 3),
		"proc/3000/cmdline": "node\x00/home/app/server.js\x00--port=8080",
		"proc/3000/io": fmt.Sprintf(`rchar: 400000000
wchar: 120000000
syscr: 900000
syscw: 210000
read_bytes: %s
write_bytes: %s
cancelled_write_bytes: 0
`, v.nodeRead, v.nodeWrite),

		// The main thread is runnable: this is the process pinning core 0.
		// A second thread is stuck faulting a page back in from disk, which
		// is what memory pressure looks like from inside a process.
		"proc/3000/task/3000/stat":  statLine(3000, "node", 'R', "8000", "2000", "0", 3, "1150000", "0"),
		"proc/3000/task/3001/stat":  statLine(3001, "node", 'D', "120", "30", "8", 3, "30000", "2200"),
		"proc/3000/task/3002/stat":  statLine(3002, "node", 'S', "60", "24", "0", 3, "18000", "30"),
		"proc/3000/task/3001/wchan": "filemap_fault\n",

		// --- the reverse proxy: nothing blocked, and it should say so ---
		"proc/2048/stat":    statLine(2048, "nginx", 'S', v.nginxUTime, v.nginxSTime, "0", 4, v.nginxRSS, "20"),
		"proc/2048/status":  statusBlock("nginx", 2048, 33, 4),
		"proc/2048/cmdline": "nginx:\x00worker\x00process",
		"proc/2048/io": fmt.Sprintf(`rchar: 900000000
wchar: 30000000
syscr: 4000000
syscw: 40000
read_bytes: %s
write_bytes: %s
cancelled_write_bytes: 0
`, v.nginxRead, v.nginxWrite),

		"proc/2048/task/2048/stat": statLine(2048, "nginx", 'S', "200", "60", "0", 4, "3000", "20"),
		"proc/2048/task/2049/stat": statLine(2049, "nginx", 'S', "60", "24", "0", 4, "1200", "5"),
		"proc/2048/task/2050/stat": statLine(2050, "nginx", 'S', "30", "12", "0", 4, "900", "3"),
		"proc/2048/task/2051/stat": statLine(2051, "nginx", 'S', "10", "4", "0", 4, "600", "2"),

		// --- open descriptors ---
		//
		// A fixture tree cannot hold real symlinks: creating one on Windows
		// needs a privilege an unprivileged process does not have. The fixture
		// format stores a link target as the content of a regular file, which
		// probe.Root.ReadLink falls back to reading.
		//
		// The sockets, the anonymous inode and /dev/null are here on purpose:
		// they are what a real descriptor table looks like, and they must be
		// filtered out rather than printed.
		"proc/1234/fd/3": "/var/lib/mysql/ib_logfile0",
		"proc/1234/fd/4": "/var/lib/mysql/ibdata1",
		"proc/1234/fd/5": "/var/lib/mysql/prod/orders.ibd",
		"proc/1234/fd/6": "socket:[284917]",
		"proc/1234/fd/7": "/dev/null",
		"proc/1234/fd/8": "anon_inode:[eventpoll]",

		"proc/3000/fd/3": "/home/app/server.js",
		"proc/3000/fd/4": "/var/log/app/app.log",
		"proc/3000/fd/5": "/var/lib/app/session.db",
		"proc/3000/fd/6": "socket:[918273]",
		"proc/3000/fd/7": "anon_inode:[eventpoll]",

		"proc/2048/fd/3": "/var/log/nginx/access.log",
	}
	return files
}

// statLine renders a /proc/<pid>/stat or /proc/<pid>/task/<tid>/stat line.
//
// Fields are assigned by index — documented field N lands at slice position
// N-1 — rather than through a positional format string. With 52 fields and a
// parser that reads field 42, a positional format is one typo away from
// silently shifting every field after it, which is the class of bug that
// produces a plausible wrong number rather than a failure.
func statLine(id int, comm string, state byte, utime, stime, majflt string, threads int, rss, blkio string) string {
	var f [52]string
	for i := range f {
		f[i] = "0"
	}

	set := func(field int, v string) { f[field-1] = v }

	set(1, strconv.Itoa(id))        // pid / tid
	set(2, "("+comm+")")            // comm, parenthesised
	set(3, string(state))           // state
	set(4, "1")                     // ppid
	set(5, strconv.Itoa(id))        // pgrp
	set(9, "4194560")               // flags
	set(10, "123456")               // minflt
	set(11, "2000")                 // cminflt
	set(12, majflt)                 // majflt
	set(14, utime)                  // utime
	set(15, stime)                  // stime
	set(18, "20")                   // priority
	set(20, strconv.Itoa(threads))  // num_threads
	set(22, "1700000000")           // starttime
	set(23, "1234567890")           // vsize
	set(24, rss)                    // rss
	set(25, "18446744073709551615") // rsslim
	set(38, "17")                   // exit_signal
	set(42, blkio)                  // delayacct_blkio_ticks

	return strings.Join(f[:], " ") + "\n"
}

func statusBlock(name string, pid, uid, threads int) string {
	return fmt.Sprintf(`Name:	%s
State:	S (sleeping)
Tgid:	%d
Pid:	%d
PPid:	1
Uid:	%d	%d	%d	%d
Gid:	%d	%d	%d	%d
Threads:	%d
`, name, pid, pid, uid, uid, uid, uid, uid, uid, uid, uid, threads)
}
