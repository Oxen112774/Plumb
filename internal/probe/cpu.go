package probe

import (
	"fmt"
	"strconv"
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

// Busy is the share of the window the CPUs spent actually executing
// instructions — user, nice, system, irq and softirq.
//
// It deliberately excludes IOWait and Steal, matching what top(1) prints as
// "used" (its %Cpu(s) line is 100-idle and does not include wa or st).
// Counting those two as busy makes a storage-stalled machine or a
// oversubscribed VM report "CPU 90%" while the real instruction count is
// 20% — and the steal case produces a report that contradicts itself, saying
// both "the hypervisor took 60%" and "CPU is 90% busy". IOWait and Steal are
// reported as their own fields precisely because they are different problems
// with different fixes; folding them into Busy throws that away.
func (c CPUTimes) Busy() uint64 {
	// Guard before subtracting, not after: these are uint64, so an
	// intermediate result below zero wraps to ~1.8e19 rather than going
	// negative, and a clamp written as "if b < 0" would never fire.
	// Idle+IOWait+Steal can exceed Total only on a short window across a
	// counter discontinuity, but then the number is meaningless anyway.
	if c.Idle+c.IOWait+c.Steal >= c.Total() {
		return 0
	}
	return c.Total() - c.Idle - c.IOWait - c.Steal
}

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
	agg, _, err := ReadCPUAll(r)
	return agg, err
}

// ReadCPUAll reads the aggregate line and every per-core line in one pass.
//
// Both come out of the same file, so reading it twice would copy the same
// kernel data twice for no reason.
//
// The per-core data is not decoration. The average hides the failure that
// matters most on a multi-core box: one core pinned at 100% while the rest
// idle. That machine reports "CPU 15%" and looks healthy while every request
// touching the single-threaded component queues behind that one core.
func ReadCPUAll(r Root) (CPUTimes, []CPUCore, error) {
	data, err := r.ReadFile("proc/stat")
	if err != nil {
		return CPUTimes{}, nil, fmt.Errorf("read proc/stat: %w", err)
	}

	var (
		agg   CPUTimes
		cores []CPUCore
		found bool
	)
	err = lines(data, func(line string) error {
		if !strings.HasPrefix(line, "cpu") {
			return nil
		}
		rest := line[3:]
		if rest == "" || rest[0] < '0' || rest[0] > '9' {
			// The aggregate line is exactly "cpu " followed by the counters.
			// Per-core lines are "cpu0", "cpu1", ... and must not match.
			fields := strings.Fields(line)[1:]
			// A modern kernel always prints all eight counters; fewer means
			// the line is not what we think it is.
			if len(fields) < 8 {
				return fmt.Errorf("proc/stat cpu line has %d fields, want at least 8", len(fields))
			}
			vals, err := parseCPUCounterFields(fields, "cpu")
			if err != nil {
				return err
			}
			agg, found = vals, true
			return nil
		}

		fields := strings.Fields(rest)
		if len(fields) < 5 {
			return fmt.Errorf("proc/stat core line %q has %d fields, want an id and at least 4 counters", line, len(fields))
		}
		id, err := strconv.Atoi(fields[0])
		if err != nil {
			return fmt.Errorf("proc/stat core id %q: %w", fields[0], err)
		}
		// A kernel older than 2.6.11 prints four counters instead of eight.
		// Missing columns are genuinely zero, not unparseable, so they are
		// padded rather than treated as an error.
		vals, err := parseCPUCounterFields(fields[1:], "cpu"+fields[0])
		if err != nil {
			return err
		}
		cores = append(cores, CPUCore{ID: id, Times: vals})
		return nil
	})
	if err != nil {
		return CPUTimes{}, nil, err
	}
	if !found {
		return CPUTimes{}, nil, fmt.Errorf("proc/stat: no aggregate cpu line")
	}
	return agg, cores, nil
}

// parseCPUCounterFields maps the user..steal columns onto CPUTimes.
func parseCPUCounterFields(fields []string, what string) (CPUTimes, error) {
	var vals [8]uint64
	for i := 0; i < 8 && i < len(fields); i++ {
		v, err := atou(fields[i])
		if err != nil {
			return CPUTimes{}, fmt.Errorf("%s field %d (%q): %w", what, i, fields[i], err)
		}
		vals[i] = v
	}
	return CPUTimes{
		User:    vals[0],
		Nice:    vals[1],
		System:  vals[2],
		Idle:    vals[3],
		IOWait:  vals[4],
		IRQ:     vals[5],
		SoftIRQ: vals[6],
		Steal:   vals[7],
	}, nil
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
	_, cores, err := ReadCPUAll(r)
	if err != nil {
		return 0, err
	}
	if len(cores) == 0 {
		return 0, fmt.Errorf("proc/stat: no per-cpu lines")
	}
	return len(cores), nil
}
