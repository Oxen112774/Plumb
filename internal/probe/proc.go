package probe

import (
	"fmt"
	"strconv"
	"strings"
	"unicode/utf8"
)

// Process is one task's cumulative counters, from /proc/<pid>.
type Process struct {
	PID  int    `json:"pid"`
	Name string `json:"name"`
	User string `json:"user,omitempty"`
	UID  int    `json:"uid"`

	// Cmdline is the full argument vector, space-joined and length-capped.
	// Without it a culprit reads as "java" or "python3", which tells an
	// operator nothing about which of their services it is.
	Cmdline string `json:"cmdline,omitempty"`

	UTime    uint64 `json:"utime"`
	STime    uint64 `json:"stime"`
	MajFlt   uint64 `json:"majflt"`
	RSSPages uint64 `json:"rss_pages"`

	// NThreads comes from field 20, so the thread count is known even when
	// the thread table is not walked.
	NThreads uint64 `json:"nthreads"`

	// LastCPU is the core this process last ran on (field 39). See
	// procStatFields: it is what lets a per-core finding name a process
	// instead of guessing.
	LastCPU      int  `json:"last_cpu"`
	LastCPUKnown bool `json:"last_cpu_known"`

	// BlkIOTicks is cumulative time blocked on block I/O (see procStatFields).
	BlkIOTicks uint64 `json:"blkio_ticks"`
	BlkIOKnown bool   `json:"blkio_known"`

	ReadBytes  uint64 `json:"read_bytes"`
	WriteBytes uint64 `json:"write_bytes"`

	// IOKnown is false when /proc/<pid>/io could not be read. On a default
	// kernel that file requires PTRACE_MODE_READ, so an unprivileged run
	// sees it only for its own processes. Without this flag an unreadable
	// counter is indistinguishable from a process doing no I/O.
	IOKnown bool `json:"io_known"`
}

// CPUTicks is total CPU time charged to this process, in USER_HZ ticks.
func (p Process) CPUTicks() uint64 { return p.UTime + p.STime }

// IOBytes is total bytes this process moved to and from storage.
func (p Process) IOBytes() uint64 { return p.ReadBytes + p.WriteBytes }

// cmdlineCap is a character count, not a byte count — see parseCmdline.
const cmdlineCap = 160

// ListPIDs enumerates the numeric entries in /proc.
func ListPIDs(r Root) ([]int, error) {
	entries, err := r.ReadDir("proc")
	if err != nil {
		return nil, fmt.Errorf("read proc: %w", err)
	}
	pids := make([]int, 0, len(entries))
	for _, e := range entries {
		pid, err := strconv.Atoi(e.Name())
		if err != nil || pid <= 0 {
			continue
		}
		pids = append(pids, pid)
	}
	return pids, nil
}

// ReadProcesses reads the counters for each pid. A process that exits
// mid-scan is skipped rather than failing the whole sweep — on a busy machine
// that races constantly and is not an error.
func ReadProcesses(r Root, pids []int, users map[int]string) []Process {
	out := make([]Process, 0, len(pids))
	for _, pid := range pids {
		p, err := readProcess(r, pid)
		if err != nil {
			continue
		}
		// UID < 0 means /proc/<pid>/status could not be read; leave the user
		// empty rather than printing a user literally named "-1".
		if p.UID >= 0 {
			if u, ok := users[p.UID]; ok {
				p.User = u
			} else {
				p.User = strconv.Itoa(p.UID)
			}
		}
		out = append(out, p)
	}
	return out
}

func readProcess(r Root, pid int) (Process, error) {
	dir := "proc/" + strconv.Itoa(pid)

	statData, err := r.ReadFile(dir + "/stat")
	if err != nil {
		return Process{}, err
	}
	st, err := parseProcStat(statData)
	if err != nil {
		return Process{}, err
	}

	// UID starts at -1, not 0. A zero-valued Process has UID 0, and 0 is a
	// real uid — root's. So if /proc/<pid>/status cannot be read (the process
	// is exiting mid-scan, /proc is mounted hidepid, or the pid is a kernel
	// thread hidden by the container's namespace) the zero value silently
	// labels an ordinary user's process as root-owned. That is the field
	// that points at who to investigate, so it must distinguish "read 0"
	// from "did not read". parseStatusUID already returns -1 for that case;
	// this is where that value has to be carried in.
	p := Process{
		PID:          pid,
		UID:          -1,
		Name:         st.Name,
		UTime:        st.UTime,
		STime:        st.STime,
		MajFlt:       st.MajFlt,
		RSSPages:     st.RSSPages,
		NThreads:     st.NThreads,
		BlkIOTicks:   st.BlkIOTicks,
		BlkIOKnown:   st.BlkIOKnown,
		LastCPU:      st.LastCPU,
		LastCPUKnown: st.LastCPUKnown,
	}

	if data, err := r.ReadFile(dir + "/io"); err == nil {
		if rb, wb, err := parseProcIO(data); err == nil {
			p.ReadBytes, p.WriteBytes, p.IOKnown = rb, wb, true
		}
	}
	if data, err := r.ReadFile(dir + "/status"); err == nil {
		p.UID = parseStatusUID(data)
	}
	if data, err := r.ReadFile(dir + "/cmdline"); err == nil {
		p.Cmdline = parseCmdline(data)
	}
	return p, nil
}

// procStatFields is the subset of /proc/<pid>/stat this tool uses.
//
// Field numbers below are the documented ones (proc(5)); the parser converts
// to a zero-based index into the fields that follow comm.
type procStatFields struct {
	Name     string
	State    byte   // field 3
	MajFlt   uint64 // field 12
	UTime    uint64 // field 14
	STime    uint64 // field 15
	NThreads uint64 // field 20
	RSSPages uint64 // field 24

	// LastCPU is field 39, processor: which CPU this task last ran on.
	//
	// It is the only link between a process and a *specific* core. Every other
	// CPU number is either per-process (its own ticks) or per-machine (the
	// aggregate), so without this field "this process is pinning core 0" is a
	// guess from two unrelated facts: something is busy, and a core is busy.
	LastCPU      int
	LastCPUKnown bool

	// BlkIOTicks is field 42, delayacct_blkio_ticks: cumulative time this task
	// spent blocked on block I/O. It is the one counter that measures being
	// stuck rather than being busy — a process doing a large fsync transfers
	// almost nothing while being blocked for seconds, so every throughput
	// ranking misses it.
	//
	// The field is only maintained when the kernel was built with
	// CONFIG_TASK_DELAY_ACCT, so BlkIOKnown records whether the column was
	// even present. A zero from an absent column must not be read as "this
	// process never blocked".
	BlkIOTicks uint64
	BlkIOKnown bool
}

// parseProcStat extracts the fields we need from /proc/<pid>/stat.
//
// The comm field (field 2) is wrapped in parentheses and may itself contain
// spaces and parentheses — a process can name itself "foo) bar". Splitting
// the whole line on whitespace is therefore wrong, and is the single most
// common bug in readers of this file. The fields after comm are parsed
// relative to the last ')' instead.
func parseProcStat(data []byte) (procStatFields, error) {
	var out procStatFields

	s := string(data)
	open := strings.IndexByte(s, '(')
	closing := strings.LastIndexByte(s, ')')
	if open < 0 || closing < open {
		return out, fmt.Errorf("proc stat: no comm field")
	}
	out.Name = s[open+1 : closing]

	rest := strings.Fields(s[closing+1:])
	// After comm the documented layout runs state(3) .. rss(24), so at least
	// 22 fields are required to reach rss.
	if len(rest) < 22 {
		return out, fmt.Errorf("proc stat: %d fields after comm, want at least 22", len(rest))
	}

	// rest[i] corresponds to documented field i+3.
	at := func(i int, what string) (uint64, error) {
		v, err := atou(rest[i])
		if err != nil {
			return 0, fmt.Errorf("proc stat %s (%q): %w", what, rest[i], err)
		}
		return v, nil
	}

	if len(rest[0]) != 1 {
		return out, fmt.Errorf("proc stat: state %q is not a single byte", rest[0])
	}
	out.State = rest[0][0]

	var err error
	if out.MajFlt, err = at(9, "majflt"); err != nil { // field 12
		return out, err
	}
	if out.UTime, err = at(11, "utime"); err != nil { // field 14
		return out, err
	}
	if out.STime, err = at(12, "stime"); err != nil { // field 15
		return out, err
	}
	if out.NThreads, err = at(17, "num_threads"); err != nil { // field 20
		return out, err
	}
	if out.RSSPages, err = at(21, "rss"); err != nil { // field 24
		return out, err
	}
	// Field 39 is a plain counter on every kernel that writes it, but a line
	// truncated before it is a missing measurement, not an error — the same
	// rule as field 42 below.
	if len(rest) > 36 {
		if v, err := atou(rest[36]); err == nil {
			out.LastCPU, out.LastCPUKnown = int(v), true
		}
	}
	// Field 42 is absent on kernels without delay accounting; that is a
	// missing measurement, not an error.
	if len(rest) > 39 {
		if v, err := atou(rest[39]); err == nil {
			out.BlkIOTicks, out.BlkIOKnown = v, true
		}
	}
	return out, nil
}

// parseProcIO extracts actual storage I/O. rchar/wchar count every read and
// write including cache hits, so they overstate disk pressure; read_bytes and
// write_bytes are what reached the block layer, which is what a saturated
// disk cares about.
func parseProcIO(data []byte) (readBytes, writeBytes uint64, err error) {
	var got bool
	err = lines(data, func(line string) error {
		key, val, ok := strings.Cut(line, ":")
		if !ok {
			return nil
		}
		n, err := atou(val)
		if err != nil {
			return fmt.Errorf("proc io %s: %w", key, err)
		}
		switch key {
		case "read_bytes":
			readBytes = n
			got = true
		case "write_bytes":
			writeBytes = n
			got = true
		}
		return nil
	})
	if err != nil {
		return 0, 0, err
	}
	if !got {
		return 0, 0, fmt.Errorf("proc io: no read_bytes or write_bytes")
	}
	return readBytes, writeBytes, nil
}

// parseStatusUID returns the real UID from /proc/<pid>/status.
func parseStatusUID(data []byte) int {
	uid := -1
	_ = lines(data, func(line string) error {
		rest, ok := strings.CutPrefix(line, "Uid:")
		if !ok {
			return nil
		}
		fields := strings.Fields(rest)
		if len(fields) == 0 {
			return nil
		}
		if v, err := strconv.Atoi(fields[0]); err == nil {
			uid = v
		}
		return errStop
	})
	return uid
}

// parseCmdline joins the NUL-separated argv and caps its length.
func parseCmdline(data []byte) string {
	s := strings.TrimRight(string(data), "\x00")
	if s == "" {
		return ""
	}
	s = strings.ReplaceAll(s, "\x00", " ")
	s = strings.Join(strings.Fields(s), " ")
	// Cap in runes, not bytes. Slicing at a byte offset can land in the
	// middle of a multi-byte character; the result is invalid UTF-8, which
	// json.Marshal replaces with U+FFFD and the terminal prints as garbage.
	// A Chinese command line is not an exotic case on a Chinese-locale host.
	if n := utf8.RuneCountInString(s); n > cmdlineCap {
		r := []rune(s)
		s = string(r[:cmdlineCap]) + "..."
	}
	return s
}

// ReadUsers parses /etc/passwd into a uid -> name map so findings can name
// the account that owns a culprit process.
func ReadUsers(r Root) map[int]string {
	data, err := r.ReadFile("etc/passwd")
	if err != nil {
		return nil
	}
	users := make(map[int]string, 32)
	_ = lines(data, func(line string) error {
		fields := strings.Split(line, ":")
		if len(fields) < 3 {
			return nil
		}
		uid, err := strconv.Atoi(fields[2])
		if err != nil {
			return nil
		}
		users[uid] = fields[0]
		return nil
	})
	return users
}
