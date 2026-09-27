package probe

import (
	"strconv"
	"strings"
)

// Thread states, the one-byte field 3 of /proc/<pid>/stat.
const (
	StateRunning   = 'R' // on a CPU or in the run queue
	StateSleeping  = 'S' // interruptible sleep, waiting for something normal
	StateDiskSleep = 'D' // uninterruptible sleep — almost always block I/O
	StateZombie    = 'Z'
	StateStopped   = 'T'
	StateTracing   = 't'
	StateIdle      = 'I' // idle kernel thread
	StateDead      = 'X'
)

// Thread is one task inside a process.
type Thread struct {
	TID   int    `json:"tid"`
	Comm  string `json:"comm"`
	State string `json:"state"`

	// WChan is the kernel function the thread is sleeping in, read from
	// /proc/<pid>/task/<tid>/wchan. It names what the thread is waiting for
	// and is the difference between "something is slow" and "this is blocked
	// on a file-system flush". Empty when the thread is running, or when the
	// kernel refuses the read.
	WChan string `json:"wchan,omitempty"`
}

// ProcessThreads summarises one process's thread table.
type ProcessThreads struct {
	PID       int      `json:"pid"`
	Name      string   `json:"name"`
	Total     int      `json:"total"`
	Running   int      `json:"running"`
	Blocked   int      `json:"blocked"` // threads in D state
	BlockedOn []Thread `json:"blocked_on,omitempty"`
}

// ThreadScan is the result of walking the kernel's thread tables.
type ThreadScan struct {
	Processes []ProcessThreads `json:"processes"`
	Total     int              `json:"total"`
	Blocked   int              `json:"blocked"`
}

// BlockedProcesses returns only the processes that have threads in D state,
// worst first.
func (s ThreadScan) BlockedProcesses() []ProcessThreads {
	var out []ProcessThreads
	for _, p := range s.Processes {
		if p.Blocked > 0 {
			out = append(out, p)
		}
	}
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j].Blocked > out[j-1].Blocked; j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out
}

// ReadThreadScan walks every process's thread table looking for threads stuck
// in uninterruptible sleep.
//
// This is the one measurement a throughput ranking cannot replace. A thread
// blocked on a file-system flush transfers almost no data while it is stuck,
// so /proc/<pid>/io — and every dashboard built on it — reports the most
// blocked process on the machine as idle. Worse, the kernel charges
// uninterruptible sleep to iowait, so the very act of being stuck makes the
// CPU look idle too.
//
// D state also means the task cannot be killed or even interrupted: SIGKILL
// will not land until the I/O completes. That is why a "stuck" process
// survives `kill -9`, and why an operator needs to know which file it is on.
//
// Only threads in D state get their wchan read. That file needs
// PTRACE_MODE_READ — unprivileged runs see it only for their own processes —
// and reading it for every thread on a large machine buys nothing.
func ReadThreadScan(r Root, pids []int) ThreadScan {
	var scan ThreadScan

	for _, pid := range pids {
		pt, ok := readProcessThreads(r, pid)
		if !ok {
			continue // the process exited mid-sweep; normal on a busy machine
		}
		scan.Processes = append(scan.Processes, pt)
		scan.Total += pt.Total
		scan.Blocked += pt.Blocked
	}
	return scan
}

func readProcessThreads(r Root, pid int) (ProcessThreads, bool) {
	dir := "proc/" + strconv.Itoa(pid) + "/task"
	entries, err := r.ReadDir(dir)
	if err != nil {
		return ProcessThreads{}, false
	}

	pt := ProcessThreads{PID: pid}
	for _, e := range entries {
		tid, err := strconv.Atoi(e.Name())
		if err != nil || tid <= 0 {
			continue
		}
		data, err := r.ReadFile(dir + "/" + e.Name() + "/stat")
		if err != nil {
			continue // thread exited between readdir and read
		}
		st, err := parseProcStat(data)
		if err != nil {
			continue
		}

		pt.Total++
		if tid == pid {
			pt.Name = st.Name // the thread group leader names the process
		}
		switch st.State {
		case StateRunning:
			pt.Running++
		case StateDiskSleep:
			pt.Blocked++
			pt.BlockedOn = append(pt.BlockedOn, Thread{
				TID:   tid,
				Comm:  st.Name,
				State: string(st.State),
				WChan: readWChan(r, dir+"/"+e.Name()+"/wchan"),
			})
		}
	}
	if pt.Name == "" && len(entries) > 0 {
		pt.Name = "-"
	}
	return pt, true
}

// readWChan returns the kernel symbol a thread is sleeping in, or "" when the
// kernel reports "0" (running) or refuses the read.
func readWChan(r Root, rel string) string {
	data, err := r.ReadFile(rel)
	if err != nil {
		return ""
	}
	s := strings.TrimSpace(string(data))
	if s == "0" {
		return ""
	}
	return s
}

// OpenFileSet is the regular files a process holds open.
type OpenFileSet struct {
	Paths []string `json:"paths"`

	// Distinct counts every distinct regular file found, which can exceed
	// len(Paths) when the list was capped.
	Distinct int `json:"distinct"`
}

// Truncated reports whether Paths is a prefix of a longer list.
func (s OpenFileSet) Truncated() bool { return s.Distinct > len(s.Paths) }

// ReadOpenFiles lists the regular files a process has open, which is how a
// blocked process gets named: the kernel will not say which descriptor is
// stuck, but a process blocked on a flush with one database file open is not
// ambiguous in practice.
//
// Sockets, pipes, anonymous inodes and the /dev, /proc, /sys and /run
// pseudo-filesystems are dropped: none of them is what a block-I/O wait is
// waiting for, and including them buries the one path that matters.
func ReadOpenFiles(r Root, pid, limit int) OpenFileSet {
	var set OpenFileSet

	dir := "proc/" + strconv.Itoa(pid) + "/fd"
	entries, err := r.ReadDir(dir)
	if err != nil {
		return set
	}

	seen := make(map[string]bool, len(entries))
	for _, e := range entries {
		target, err := r.ReadLink(dir + "/" + e.Name())
		if err != nil {
			continue // descriptor closed between readdir and readlink
		}
		if !isInterestingPath(target) || seen[target] {
			continue
		}
		seen[target] = true
		set.Distinct++
		if limit > 0 && len(set.Paths) >= limit {
			continue
		}
		set.Paths = append(set.Paths, target)
	}
	return set
}

// isInterestingPath filters readlink output down to real files.
func isInterestingPath(target string) bool {
	if !strings.HasPrefix(target, "/") {
		// "socket:[123]", "pipe:[123]", "anon_inode:[eventpoll]"
		return false
	}
	for _, prefix := range []string{"/dev/", "/proc/", "/sys/", "/run/"} {
		if strings.HasPrefix(target, prefix) {
			return false
		}
	}
	// A path ending in " (deleted)" is kept deliberately: the file was
	// unlinked but the process still holds it open, which is itself worth
	// seeing. The suffix is left on so the report does not present a path
	// that no longer exists as though it did.
	return true
}
