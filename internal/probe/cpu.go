package probe

import (
	"fmt"
	"strings"
)

// CPUTimes is the cumulative jiffy counter set from the "cpu " line of
// /proc/stat. All values are monotonic since boot.
//
// The fields are kept separate rather than collapsed into a single "busy"
// number because the decomposition is the diagnosis:
//
//	User/System  — the machine is doing work someone asked for
//	IOWait       — the machine is idle *because* storage is slow
//	Steal        — a hypervisor took the CPU away from this guest
//	SoftIRQ      — usually the network stack; a flood shows up here
//
// "CPU 90%" is a symptom. "CPU 61% iowait" and "CPU 71% steal" are two
// completely different problems with completely different fixes.
type CPUTimes struct {
	User    uint64 `json:"user"`
	Nice    uint64 `json:"nice"`
	System  uint64 `json:"system"`
	Idle    uint64 `json:"idle"`
	IOWait  uint64 `json:"iowait"`
	IRQ     uint64 `json:"irq"`
	SoftIRQ uint64 `json:"softirq"`
	Steal   uint64 `json:"steal"`
}

// Total is the sum of every field, i.e. all jiffies accounted for.
func (c CPUTimes) Total() uint64 {
	return c.User + c.Nice + c.System + c.Idle + c.IOWait + c.IRQ + c.SoftIRQ + c.Steal
}

// Busy is total time not spent idle.
func (c CPUTimes) Busy() uint64 { return c.Total() - c.Idle }

// Delta subtracts an earlier sample, clamping at zero. A field can legitimately
// go backwards when a CPU is offlined, and a counter can wrap; neither should
// produce a huge bogus spike.
func (c CPUTimes) Delta(prev CPUTimes) CPUTimes {
	sub := func(a, b uint64) uint64 {
		if a < b {
			return 0
		}
		return a - b
	}
	return CPUTimes{
		User:    sub(c.User, prev.User),
		Nice:    sub(c.Nice, prev.Nice),
		System:  sub(c.System, prev.System),
		Idle:    sub(c.Idle, prev.Idle),
		IOWait:  sub(c.IOWait, prev.IOWait),
		IRQ:     sub(c.IRQ, prev.IRQ),
		SoftIRQ: sub(c.SoftIRQ, prev.SoftIRQ),
		Steal:   sub(c.Steal, prev.Steal),
	}
}

// ReadCPU reads the aggregate "cpu " line from /proc/stat.
func ReadCPU(r Root) (CPUTimes, error) {
	data, err := r.ReadFile("proc/stat")
	if err != nil {
		return CPUTimes{}, fmt.Errorf("read proc/stat: %w", err)
	}

	var out CPUTimes
	found := false
	err = lines(data, func(line string) error {
		// The aggregate line is exactly "cpu " followed by the counters.
		// Per-core lines are "cpu0", "cpu1", ... and must not match.
		if !strings.HasPrefix(line, "cpu ") {
			return nil
		}
		fields := strings.Fields(line)[1:]
		if len(fields) < 8 {
			return fmt.Errorf("proc/stat cpu line has %d fields, want at least 8", len(fields))
		}
		var vals [8]uint64
		for i := 0; i < 8; i++ {
			v, err := atou(fields[i])
			if err != nil {
				return fmt.Errorf("proc/stat cpu field %d (%q): %w", i, fields[i], err)
			}
			vals[i] = v
		}
		out = CPUTimes{
			User:    vals[0],
			Nice:    vals[1],
			System:  vals[2],
			Idle:    vals[3],
			IOWait:  vals[4],
			IRQ:     vals[5],
			SoftIRQ: vals[6],
			Steal:   vals[7],
		}
		found = true
		return errStop
	})
	if err != nil {
		return CPUTimes{}, err
	}
	if !found {
		return CPUTimes{}, fmt.Errorf("proc/stat: no aggregate cpu line")
	}
	return out, nil
}

// CountCores returns the number of logical CPUs the kernel schedules onto.
//
// runtime.NumCPU() would be the obvious source and is the wrong one: it
// reports the CPUs visible to the process, which inside a container is the
// host's count rather than the cgroup limit. A container pinned to two cores
// on a sixty-four-core host would then have its load average divided by
// sixty-four and look thirty-two times healthier than it is — precisely the
// situation this tool exists to detect.
//
// Counting the per-CPU lines in /proc/stat gives the number the scheduler is
// actually working with. (A cgroup-limited container still reports the host's
// CPUs here; reading cgroup cpu.max is the correct refinement, and is not yet
// implemented.)
func CountCores(r Root) (int, error) {
	data, err := r.ReadFile("proc/stat")
	if err != nil {
		return 0, fmt.Errorf("read proc/stat: %w", err)
	}
	n := 0
	err = lines(data, func(line string) error {
		if !strings.HasPrefix(line, "cpu") {
			return nil
		}
		rest := line[3:]
		if rest == "" || rest[0] < '0' || rest[0] > '9' {
			return nil // the aggregate "cpu " line, not a per-core line
		}
		n++
		return nil
	})
	if err != nil {
		return 0, err
	}
	if n == 0 {
		return 0, fmt.Errorf("proc/stat: no per-cpu lines")
	}
	return n, nil
}
