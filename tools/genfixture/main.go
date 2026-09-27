// Command genfixture writes the demonstration fixture tree under testdata/demo.
//
// The fixture exists so plumb can be run and its output inspected on a machine
// that is not Linux — the development machine is Windows. It describes a
// plausible incident rather than a synthetic one: a database host whose CPU is
// only 40% busy while the disk stalls every task on the machine, with a
// hypervisor taking a slice of the CPU on top.
//
// Every number is chosen so the arithmetic works out to a readable rate over a
// one-second window. Regenerate with:
//
//	go run ./tools/genfixture
package main

import (
	"fmt"
	"os"
	"path/filepath"
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

// snap holds the values that differ between the two samples.
type snap struct {
	uptime string
	load   string

	user, system, idle, iowait, softirq, steal string

	memAvailable, dirty, writeback string

	ioSome, ioFull   string
	cpuSome          string
	memSome, memFull string

	devReads, devSectorsRead    string
	devWrites, devSectorsWritten string
	devInFlight, devMillis      string

	mysqlUTime, mysqlSTime, mysqlRSS    string
	mysqlRead, mysqlWrite               string
	mysqlRChar, mysqlWChar              string

	nodeUTime, nodeSTime, nodeRSS string
	nodeRead, nodeWrite           string

	nginxUTime, nginxSTime, nginxRSS string
	nginxRead, nginxWrite            string
}

func prevSnap() snap {
	return snap{
		uptime: "1036800.42", load: "2.14 1.87 1.42 2/318 41234",

		user: "480000", system: "120000", idle: "1400000",
		iowait: "180000", softirq: "20000", steal: "5000",

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
		uptime: "1036801.42", load: "2.21 1.90 1.44 3/319 41877",

		user: "480180", system: "120150", idle: "1400900",
		iowait: "180180", softirq: "20030", steal: "5060",

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
	return map[string]string{
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

		// Fields: user nice system idle iowait irq softirq steal.
		// Only the aggregate line is parsed; the per-core lines exist so the
		// core count can be derived, and are left static to keep the format
		// string's argument count honest.
		"proc/stat": fmt.Sprintf(`cpu  %s 0 %s %s %s 0 %s %s 0 0
cpu0 120000 0 30000 350000 45000 0 5000 1250 0 0
cpu1 119000 0 30100 349500 45200 0 4900 1240 0 0
cpu2 121000 0 29900 350200 44800 0 5100 1260 0 0
cpu3 120180 0 30150 350100 45180 0 5030 1310 0 0
intr 1234567890 12 0 0
ctxt 987654321
btime 1700000000
processes 1234567
procs_running 2
procs_blocked 1
`, v.user, v.system, v.idle, v.iowait, v.softirq, v.steal),

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

		// The database: the IO culprit, but not the CPU or memory one.
		"proc/1234/stat":    procStatLine(1234, "mysqld", v.mysqlUTime, v.mysqlSTime, "0", v.mysqlRSS, 32),
		"proc/1234/status":  statusBlock("mysqld", 1234, 113),
		"proc/1234/cmdline": "/usr/sbin/mysqld\x00--defaults-file=/etc/mysql/my.cnf",
		"proc/1234/io": fmt.Sprintf(`rchar: %s
wchar: %s
syscr: 5000000
syscw: 900000
read_bytes: %s
write_bytes: %s
cancelled_write_bytes: 0
`, v.mysqlRChar, v.mysqlWChar, v.mysqlRead, v.mysqlWrite),

		// The application: nominal IO, but by far the largest resident set.
		"proc/3000/stat":    procStatLine(3000, "node", v.nodeUTime, v.nodeSTime, "12", v.nodeRSS, 11),
		"proc/3000/status":  statusBlock("node", 3000, 1000),
		"proc/3000/cmdline": "node\x00/home/app/server.js\x00--port=8080",
		"proc/3000/io": fmt.Sprintf(`rchar: 400000000
wchar: 120000000
syscr: 900000
syscw: 210000
read_bytes: %s
write_bytes: %s
cancelled_write_bytes: 0
`, v.nodeRead, v.nodeWrite),

		"proc/2048/stat":    procStatLine(2048, "nginx", v.nginxUTime, v.nginxSTime, "0", v.nginxRSS, 4),
		"proc/2048/status":  statusBlock("nginx", 2048, 33),
		"proc/2048/cmdline": "nginx:\x00worker\x00process",
		"proc/2048/io": fmt.Sprintf(`rchar: 900000000
wchar: 30000000
syscr: 4000000
syscw: 40000
read_bytes: %s
write_bytes: %s
cancelled_write_bytes: 0
`, v.nginxRead, v.nginxWrite),
	}
}

// procStatLine renders a /proc/<pid>/stat line.
//
// The layout after comm must reach field 24 (rss) to be parseable:
//
//	3 state  4 ppid  5 pgrp  6 session  7 tty_nr  8 tpgid  9 flags
//	10 minflt  11 cminflt  12 majflt  13 cmajflt
//	14 utime  15 stime  16 cutime  17 cstime
//	18 priority  19 nice  20 num_threads  21 itrealvalue
//	22 starttime  23 vsize  24 rss
func procStatLine(pid int, comm, utime, stime, majflt, rss string, threads int) string {
	return fmt.Sprintf(
		"%d (%s) S 1 %d %d 0 -1 4194560 123456 0 %s 0 %s %s 0 0 20 0 %d 0 1234567 987654321 %s 18446744073709551615 1 1 0 0 0 0 0 0 0 0 0 0 17 0 0 0 0 0 0\n",
		pid, comm, pid, pid, majflt, utime, stime, threads, rss)
}

func statusBlock(name string, pid, uid int) string {
	return fmt.Sprintf(`Name:	%s
State:	S (sleeping)
Tgid:	%d
Pid:	%d
PPid:	1
Uid:	%d	%d	%d	%d
Gid:	%d	%d	%d	%d
Threads:	8
`, name, pid, pid, uid, uid, uid, uid, uid, uid, uid, uid)
}
