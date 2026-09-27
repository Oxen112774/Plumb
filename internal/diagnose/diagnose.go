// Package diagnose turns raw probe data into ranked, attributed findings.
//
// The distinction from a monitoring dashboard is deliberate. A dashboard
// answers "what are the numbers"; it leaves the operator to look at six
// graphs, notice that one is high, and work out what caused it. This package
// answers "what is wrong and who did it", which is the question someone
// actually types into a search box at 2am.
//
// Every finding carries the source of the measurement it rests on, so a
// conclusion can be checked rather than trusted.
package diagnose

import (
	"fmt"
	"os"
	"sort"
	"time"

	"github.com/Oxen112774/plumb/internal/probe"
)

// Severity ranks how much attention a finding deserves.
type Severity string

const (
	SeverityCritical Severity = "critical"
	SeverityWarning  Severity = "warning"
	SeverityWatch    Severity = "watch"
	SeverityOK       Severity = "ok"
	SeverityInfo     Severity = "info"
)

// Rank orders severities worst-first.
func (s Severity) Rank() int {
	switch s {
	case SeverityCritical:
		return 0
	case SeverityWarning:
		return 1
	case SeverityWatch:
		return 2
	case SeverityOK:
		return 3
	default:
		return 4
	}
}

// Marker is a short ASCII tag for terminals without emoji support.
func (s Severity) Marker() string {
	switch s {
	case SeverityCritical:
		return "!!!"
	case SeverityWarning:
		return " ! "
	case SeverityWatch:
		return " · "
	case SeverityOK:
		return " + "
	default:
		return " i "
	}
}

// Label is the Chinese display name.
func (s Severity) Label() string {
	switch s {
	case SeverityCritical:
		return "严重"
	case SeverityWarning:
		return "警告"
	case SeverityWatch:
		return "注意"
	case SeverityOK:
		return "正常"
	default:
		return "信息"
	}
}

// Evidence is one supporting measurement and where it came from.
type Evidence struct {
	Source string `json:"source"`
	Detail string `json:"detail"`
}

// Culprit is a process identified as a source of a finding.
type Culprit struct {
	PID     int    `json:"pid"`
	Name    string `json:"name"`
	User    string `json:"user,omitempty"`
	Cmdline string `json:"cmdline,omitempty"`
	Note    string `json:"note"`
}

// Finding is one attributed conclusion.
type Finding struct {
	Area     string     `json:"area"`
	Severity Severity   `json:"severity"`
	Headline string     `json:"headline"`
	Evidence []Evidence `json:"evidence"`
	Culprits []Culprit  `json:"culprits,omitempty"`
	Advice   string     `json:"advice,omitempty"`
}

// CPUUsage is the CPU time breakdown across the sample window, in percent of
// total capacity.
type CPUUsage struct {
	Busy    float64 `json:"busy"`
	User    float64 `json:"user"`
	System  float64 `json:"system"`
	IOWait  float64 `json:"iowait"`
	Steal   float64 `json:"steal"`
	SoftIRQ float64 `json:"softirq"`
	Idle    float64 `json:"idle"`
}

// Report is the complete output of one diagnosis.
//
// Every field carries an explicit json tag: this struct is the documented
// interface for anything consuming -json, and Go's default is to emit the Go
// field name, which would put "Findings" next to "open_files" in the same
// object.
type Report struct {
	StartedAt time.Time      `json:"started_at"`
	Interval  time.Duration  `json:"interval_ns"`
	System    probe.System   `json:"system"`
	CPUUsage  CPUUsage       `json:"cpu_usage"`
	Memory    probe.Memory   `json:"memory"`
	Pressure  probe.Pressure `json:"pressure"`
	Findings  []Finding      `json:"findings"`
	Errors    []string       `json:"errors,omitempty"`

	// Cores is per-core utilisation over the window, in the kernel's own core
	// order. It is carried in the report rather than left to be recomputed
	// because the machine average cannot express the one thing per-core data
	// is for: a single saturated core next to idle ones.
	Cores []probe.CoreLoad `json:"cores,omitempty"`

	// Threads summarises the thread tables as of the later sample, and
	// OpenFiles lists the files held open by processes that had a thread
	// blocked. Both are empty in single-sample mode.
	Threads   probe.ThreadScan          `json:"threads,omitempty"`
	OpenFiles map[int]probe.OpenFileSet `json:"open_files,omitempty"`

	// SingleSample is true when only one snapshot was available, so anything
	// requiring a rate — process attribution in particular — is unavailable.
	SingleSample bool `json:"single_sample"`
}

// Worst returns the most severe finding, or a synthetic OK finding when the
// machine looks healthy.
func (r Report) Worst() Finding {
	for _, f := range r.Findings {
		if f.Severity != SeverityOK && f.Severity != SeverityInfo {
			return f
		}
	}
	if len(r.Findings) > 0 {
		return r.Findings[0]
	}
	return Finding{
		Area:     "整体",
		Severity: SeverityOK,
		Headline: "未发现明显瓶颈",
	}
}

// HasProblems reports whether anything at warning severity or worse was found.
// Callers use it to fail a health check without parsing the text output.
func (r Report) HasProblems() bool {
	for _, f := range r.Findings {
		if f.Severity.Rank() <= SeverityWarning.Rank() {
			return true
		}
	}
	return false
}

// Options controls a diagnosis run.
type Options struct {
	// Interval is the wall-clock gap between the two snapshots. It is used to
	// convert cumulative counters into rates; it must match the real gap.
	Interval time.Duration
	// Top is how many culprit processes to name per finding.
	Top int
}

// Analyze compares two snapshots and produces a ranked report.
//
// When prev is the zero value the run degrades to single-sample mode: the
// findings that read an instantaneous state (pressure, free space, swap, dirty
// pages) still work, and the ones that need a delta are skipped rather than
// computed from meaningless numbers.
func Analyze(prev, cur probe.Snapshot, opts Options) Report {
	if opts.Top <= 0 {
		opts.Top = 3
	}
	a := &analysis{
		prev:     prev,
		cur:      cur,
		interval: opts.Interval,
		top:      opts.Top,
		hasPrev:  !prev.At.IsZero() && opts.Interval > 0,
	}
	if a.hasPrev {
		a.cpu = computeCPUUsage(prev.CPU, cur.CPU)
	}

	rep := Report{
		StartedAt:    cur.At,
		Interval:     opts.Interval,
		System:       cur.System,
		CPUUsage:     a.cpu,
		Cores:        cur.CoreLoads(prev),
		Memory:       cur.Memory,
		Pressure:     cur.Pressure,
		Threads:      cur.Threads,
		OpenFiles:    cur.OpenFiles,
		Errors:       cur.Errors,
		SingleSample: !a.hasPrev,
	}
	rep.Findings = sortFindings(a.run())
	return rep
}

// analysis carries the two snapshots and the derived values every rule
// shares.
type analysis struct {
	prev, cur probe.Snapshot
	interval  time.Duration
	top       int
	hasPrev   bool
	cpu       CPUUsage
}

// run evaluates every rule and keeps the ones that fired.
func (a *analysis) run() []Finding {
	rules := []func() *Finding{
		a.ruleIOPressure,
		a.ruleBlockedThreads,
		a.ruleMemoryPressure,
		a.ruleCPUPressure,
		a.ruleCoreSkew,
		a.ruleSteal,
		a.ruleSwap,
		a.ruleDiskSpace,
		a.ruleDirtyPages,
	}
	var out []Finding
	for _, rule := range rules {
		if f := rule(); f != nil {
			out = append(out, *f)
		}
	}
	return out
}

func sortFindings(fs []Finding) []Finding {
	sort.SliceStable(fs, func(i, j int) bool {
		if fs[i].Severity.Rank() != fs[j].Severity.Rank() {
			return fs[i].Severity.Rank() < fs[j].Severity.Rank()
		}
		return fs[i].Area < fs[j].Area
	})
	return fs
}

// computeCPUUsage converts cumulative jiffies into a percentage breakdown
// across the window.
func computeCPUUsage(prev, cur probe.CPUTimes) CPUUsage {
	d := cur.Delta(prev)
	total := d.Total()
	if total == 0 {
		return CPUUsage{}
	}
	p := func(v uint64) float64 { return Round2(float64(v) * 100 / float64(total)) }
	return CPUUsage{
		Busy:    p(d.Busy()),
		User:    p(d.User),
		System:  p(d.System),
		IOWait:  p(d.IOWait),
		Steal:   p(d.Steal),
		SoftIRQ: p(d.SoftIRQ),
		Idle:    p(d.Idle),
	}
}

// --- culprit attribution ---

// culpritFloorDivisor drops candidates below a twentieth of the leader.
//
// Without it, a process moving 10 KB/s is printed next to one moving 40 MB/s
// under the same heading, and a reader reasonably concludes both are suspects.
// Naming someone as the cause of an incident is an accusation; it should carry
// a numeric floor.
const culpritFloorDivisor = 20

// procDiff is one process's activity measured over the sample window.
type procDiff struct {
	proc  probe.Process
	ticks uint64
	read  uint64
	write uint64
	ioOK  bool
}

// diffs pairs each current process with its previous counters.
func (a *analysis) diffs() []procDiff {
	prevByPID := a.prev.ProcessByPID()
	out := make([]procDiff, 0, len(a.cur.Procs))
	for _, p := range a.cur.Procs {
		d := procDiff{proc: p}
		if before, ok := prevByPID[p.PID]; ok {
			d.ticks = sub(p.CPUTicks(), before.CPUTicks())
			if p.IOKnown && before.IOKnown {
				d.read = sub(p.ReadBytes, before.ReadBytes)
				d.write = sub(p.WriteBytes, before.WriteBytes)
				d.ioOK = true
			}
		}
		out = append(out, d)
	}
	return out
}

// TopIO names the processes that moved the most data to or from storage
// during the window.
func (a *analysis) TopIO(n int) []Culprit {
	if !a.hasPrev {
		return nil
	}
	diffs := a.diffs()
	sort.Slice(diffs, func(i, j int) bool {
		return diffs[i].read+diffs[i].write > diffs[j].read+diffs[j].write
	})

	var leader uint64
	for _, d := range diffs {
		if d.ioOK && d.read+d.write > 0 {
			leader = d.read + d.write
			break
		}
	}
	if leader == 0 {
		return nil
	}
	floor := leader / culpritFloorDivisor

	var out []Culprit
	for _, d := range diffs {
		if !d.ioOK || d.read+d.write < floor {
			break
		}
		out = append(out, Culprit{
			PID:     d.proc.PID,
			Name:    d.proc.Name,
			User:    d.proc.User,
			Cmdline: d.proc.Cmdline,
			Note: fmt.Sprintf("读 %s，写 %s",
				HumanRate(d.read, a.interval), HumanRate(d.write, a.interval)),
		})
		if len(out) >= n {
			break
		}
	}
	return out
}

// TopCPU names the processes that consumed the most CPU during the window.
func (a *analysis) TopCPU(n int) []Culprit {
	if !a.hasPrev {
		return nil
	}
	diffs := a.diffs()
	sort.Slice(diffs, func(i, j int) bool { return diffs[i].ticks > diffs[j].ticks })

	var leader uint64
	for _, d := range diffs {
		if d.ticks > 0 {
			leader = d.ticks
			break
		}
	}
	if leader == 0 {
		return nil
	}
	floor := leader / culpritFloorDivisor

	var out []Culprit
	for _, d := range diffs {
		if d.ticks < floor {
			break
		}
		out = append(out, Culprit{
			PID:     d.proc.PID,
			Name:    d.proc.Name,
			User:    d.proc.User,
			Cmdline: d.proc.Cmdline,
			Note: fmt.Sprintf("占用 %s 核",
				trimFloat(CoresUsed(d.ticks, a.interval))),
		})
		if len(out) >= n {
			break
		}
	}
	return out
}

// TopRSS names the processes holding the most resident memory right now.
func (a *analysis) TopRSS(n int) []Culprit {
	procs := append([]probe.Process(nil), a.cur.Procs...)
	sort.Slice(procs, func(i, j int) bool { return procs[i].RSSPages > procs[j].RSSPages })
	if len(procs) == 0 || procs[0].RSSPages == 0 {
		return nil
	}
	floor := procs[0].RSSPages / culpritFloorDivisor

	page := uint64(os.Getpagesize())
	var out []Culprit
	for _, p := range procs {
		if p.RSSPages < floor {
			break
		}
		out = append(out, Culprit{
			PID:     p.PID,
			Name:    p.Name,
			User:    p.User,
			Cmdline: p.Cmdline,
			Note:    fmt.Sprintf("常驻内存 %s", HumanBytes(p.RSSPages*page)),
		})
		if len(out) >= n {
			break
		}
	}
	return out
}

// --- helpers ---

func sub(a, b uint64) uint64 {
	if a < b {
		return 0
	}
	return a - b
}

// PercentOfCore converts a tick delta into utilisation of a single core.
//
// USER_HZ is 100 on every mainstream configuration (it is baked into the
// userspace ABI, and changing it requires a kernel rebuild), so ticks divided
// by the elapsed seconds gives the percentage directly.
func PercentOfCore(ticks uint64, interval time.Duration) float64 {
	if interval <= 0 {
		return 0
	}
	return float64(ticks) / interval.Seconds()
}

// CoresUsed converts a tick delta into how many cores' worth of CPU time it
// represents: 100 ticks in one second is one whole core.
func CoresUsed(ticks uint64, interval time.Duration) float64 {
	return PercentOfCore(ticks, interval) / 100
}
