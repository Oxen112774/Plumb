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

	"github.com/Oxen112774/plumb/internal/i18n"
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

// Label is the severity's display name in lang.
func (s Severity) Label(lang i18n.Lang) string {
	zh, en := "信息", "INFO"
	switch s {
	case SeverityCritical:
		zh, en = "严重", "CRITICAL"
	case SeverityWarning:
		zh, en = "警告", "WARNING"
	case SeverityWatch:
		zh, en = "注意", "WATCH"
	case SeverityOK:
		zh, en = "正常", "OK"
	}
	if lang == i18n.EN {
		return en
	}
	return zh
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

	// LastCPU is the core this process last ran on, from field 39 of
	// /proc/<pid>/stat. It is carried rather than left to be re-read because
	// it is the evidence that ties a per-core finding to a named process:
	// "a core is full" plus "a process is busy" does not license naming that
	// process as the cause — this does.
	LastCPU      int  `json:"last_cpu"`
	LastCPUKnown bool `json:"last_cpu_known"`
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

	// CPUMeasured is false when /proc/stat could not be read. The CPU lines are
	// then omitted rather than printed as a misleading 0.0%.
	CPUMeasured bool `json:"cpu_measured"`

	// Net is the TCP activity across the sample window: retransmit rate and
	// listen-queue drops, both of which slow a server down while every other
	// counter looks clean.
	Net NetUsage `json:"net"`

	// Throttle is the CPU throttling measured inside this sample window, when
	// two samples were taken. The findings and the overview both read it, so
	// the two cannot disagree about whether the pod is being stopped now.
	Throttle ThrottleRate `json:"throttle"`

	// Cgroup is the resource envelope this run sits inside, when a cgroup v2
	// limit applies. It is the context in which Memory and load-per-core must
	// be read on a container.
	Cgroup probe.Cgroup `json:"cgroup,omitempty"`

	// K8s names the pod this run sits in, when a kubelet put it there.
	//
	// It travels with the report for the same reason the hostname does: the
	// reports that matter are the ones collected from twenty nodes by someone
	// who was not there, and a snapshot without an identity cannot be told
	// apart from the nineteen next to it.
	K8s probe.K8sContext `json:"k8s,omitempty"`

	// Thresholds are the cut-offs the findings were judged by.
	//
	// They travel with the report for the same reason a measurement does: a
	// reader holding last month's report must be able to see which line
	// "critical" meant that day, including when someone had moved it.
	Thresholds Thresholds `json:"thresholds"`
}

// ThrottleRate is CPU throttling measured inside the sample window, next to
// the cumulative counters the cgroup itself carries.
//
// The cumulative pair answers "how much has this quota cost in total", which
// on a pod that has been up for a week is dominated by history. The window
// answers the question someone actually has: is it being stopped right now.
// Both are kept because the difference between them is itself informative —
// a pod at 31% cumulative and 80% in the window is in trouble this second,
// while one at 31% and 0% recovered some time ago.
type ThrottleRate struct {
	// Periods is how many enforcement periods elapsed in the window.
	Periods uint64 `json:"periods"`
	// Throttled is how many of them ended with the cgroup stopped.
	Throttled uint64 `json:"throttled"`
	// Measured is false in single-sample mode, and whenever no enforcement
	// period elapsed inside the window — a window containing zero periods
	// has nothing to report, and dividing by it would be meaningless.
	Measured bool `json:"measured"`
}

// Pct is the share of the window's periods that ended with the cgroup
// stopped, or 0 when the window proved nothing.
func (t ThrottleRate) Pct() float64 {
	if !t.Measured || t.Periods == 0 {
		return 0
	}
	return float64(t.Throttled) * 100 / float64(t.Periods)
}

// NetUsage is the TCP accounting turned into rates over the sample window.
//
// The kernel keeps these as counters since boot; a rate needs both samples,
// which is why the whole struct is zero in single-sample mode.
type NetUsage struct {
	// RetransPct is retransmitted segments as a percentage of segments sent.
	// Retransmission means a packet was presumed lost and sent again: the
	// receiver waits, the sender waits for the ack, and every affected request
	// pays a multiple of the round-trip time. It is the TCP-level signature of
	// packet loss, a saturated link, or a far end too slow to keep up.
	RetransPct  float64 `json:"retrans_pct"`
	OutSegs     uint64  `json:"out_segs"`
	RetransSegs uint64  `json:"retrans_segs"`

	// ListenDrops and ListenOverflows count connections refused at the listen
	// queue during the window. Each one is a client that got a timeout or a
	// connection reset, not a slow response — the application never saw it.
	ListenDrops     uint64 `json:"listen_drops"`
	ListenOverflows uint64 `json:"listen_overflows"`

	SyncookiesFailed uint64 `json:"syncookies_failed"`

	// Measured is false in single-sample mode, or when the network counters
	// could not be read at all. It keeps an unread network from reading as a
	// clean one.
	Measured bool `json:"measured"`
}

// Worst returns the most severe finding, or a synthetic OK finding when the
// machine looks healthy.
//
// lang is taken because the synthetic finding is text plumb wrote rather than
// a measurement it read, and it is the one place a report can be empty of
// every other string.
func (r Report) Worst(lang i18n.Lang) Finding {
	for _, f := range r.Findings {
		if f.Severity != SeverityOK && f.Severity != SeverityInfo {
			return f
		}
	}
	if len(r.Findings) > 0 {
		return r.Findings[0]
	}
	area, headline := "整体", "未发现明显瓶颈"
	if lang == i18n.EN {
		area, headline = "overall", "no obvious bottleneck found"
	}
	return Finding{
		Area:     area,
		Severity: SeverityOK,
		Headline: headline,
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
	// Thresholds are the severity cut-offs, already filled in by the caller
	// or empty for the built-in defaults.
	Thresholds Thresholds
	// Lang is the language the finding text is written in.
	Lang i18n.Lang
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
	// The nominal interval is what the caller *asked* for; the real gap
	// between the two snapshots is what the counters were actually measured
	// over. probe.Take walks every /proc entry and reads four files per pid,
	// so on a loaded machine the sampling itself can take a second or more —
	// and it takes longest exactly when the machine is saturated, which is
	// when this tool matters most. Dividing by the nominal interval then
	// reports "10 cores busy" on a machine doing 1 core of work.
	// Both snapshots carry the time they were taken, so use the real gap.
	interval := opts.Interval
	if d := cur.At.Sub(prev.At); d > 0 {
		interval = d
	}
	a := &analysis{
		prev:     prev,
		cur:      cur,
		interval: interval,
		top:      opts.Top,
		hasPrev:  !prev.At.IsZero() && interval > 0,
		th:       opts.Thresholds.WithDefaults(),
		lang:     opts.Lang,
	}
	// A machine that is running always accumulates jiffies, so a zero
	// aggregate means the file could not be read, not that nothing happened.
	a.cpuMeasured = a.hasPrev && cur.CPU.Total() > 0
	if a.cpuMeasured {
		a.cpu = computeCPUUsage(prev.CPU, cur.CPU)
	}
	a.net = computeNetUsage(prev.Net, cur.Net, a.hasPrev)
	a.throttle = computeThrottleRate(prev.Cgroup.Throttle, cur.Cgroup.Throttle, a.hasPrev)

	rep := Report{
		StartedAt:    cur.At,
		// The measured gap, not the requested one — this string is printed
		// as the sample window, so showing the nominal value there would
		// contradict every rate computed from the real one.
		Interval:     interval,
		System:       cur.System,
		CPUUsage:     a.cpu,
		Cores:        cur.CoreLoads(prev),
		Memory:       cur.Memory,
		Pressure:     cur.Pressure,
		Threads:      cur.Threads,
		OpenFiles:    cur.OpenFiles,
		Errors:       cur.Errors,
		SingleSample: !a.hasPrev,
		CPUMeasured:  a.cpuMeasured,
		Net:          a.net,
		Throttle:     a.throttle,
		Cgroup:       cur.Cgroup,
		K8s:          cur.K8s,
		Thresholds:   a.th,
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
	// cpuMeasured is hasPrev plus a readable aggregate: the CPU-derived rules
	// and the CPU report lines key off it, not off hasPrev.
	cpuMeasured bool
	cpu         CPUUsage
	net         NetUsage
	throttle    ThrottleRate

	// th are the cut-offs this run judged by, and lang the language its
	// findings are written in. Both are carried rather than passed down so
	// every rule reads the same values without threading arguments.
	th   Thresholds
	lang i18n.Lang
}

// msg picks the language-appropriate format and fills it.
//
// Both languages sit next to each other at the point of use on purpose. A
// translation table in another file drifts the moment a rule's wording is
// edited, and the drift is invisible: nothing fails, the English just quietly
// describes the old rule. Keeping the pair together means changing one
// without the other is a change a reviewer can see.
func (a *analysis) msg(zh, en string, args ...any) string {
	s := zh
	if a.lang == i18n.EN {
		s = en
	}
	if len(args) == 0 {
		return s
	}
	return fmt.Sprintf(s, args...)
}

// pressure returns the PSI the rules should judge by: this process's own
// cgroup's, when one exists with per-cgroup PSI, otherwise the system-wide
// one.
//
// In a container the system-wide numbers describe everyone's waiting — the
// noisy neighbour included — while the cgroup's own describe this workload's.
// On a host with no limit the two are the same files' contents, so the
// preference changes nothing there.
func (a *analysis) pressure() probe.Pressure {
	if c := a.cur.Cgroup; c.V2 && c.Pressure.Supported {
		return c.Pressure
	}
	return a.cur.Pressure
}

// pressureSource names where pressure() came from, for the evidence line: a
// finding's source must say which file the number was read from, and with two
// possible PSI locations the label is the only thing keeping the claim
// checkable.
//
// The cgroup branch names the file inside the hierarchy root. The numbers
// themselves are read from this process's own cgroup directory (see
// probe.cgroupV2Base), so a pod should be pointed at its own file — but the
// root is where the file exists on every v2 host, including the ones where
// the path is not resolvable, and a source label that says "no such file" is
// worse than one that points at a readable sibling.
func (a *analysis) pressureSource(name string) string {
	if c := a.cur.Cgroup; c.V2 && c.Pressure.Supported {
		return "/sys/fs/cgroup/" + name + ".pressure"
	}
	return "/proc/pressure/" + name
}

// run evaluates every rule and keeps the ones that fired.
func (a *analysis) run() []Finding {
	rules := []func() *Finding{
		a.ruleIOPressure,
		a.ruleBlockedThreads,
		a.ruleMemoryPressure,
		a.ruleCPUPressure,
		a.ruleCPUThrottle,
		a.ruleCoreSkew,
		a.ruleSteal,
		a.ruleSwap,
		a.ruleDiskSpace,
		a.ruleDirtyPages,
		a.ruleNetwork,
		a.ruleCgroupMemory,
	}
	var out []Finding
	for _, rule := range rules {
		if f := rule(); f != nil {
			out = append(out, *f)
		}
	}
	return out
}

// computeNetUsage turns two TCP counter readings into window rates. A counter
// that went backwards — a namespace restart, a module reload — yields zero
// rather than a negative rate, and the caller's Measured flag carries whether
// the reading was there at all.
func computeNetUsage(prev, cur probe.NetTCP, hasPrev bool) NetUsage {
	if !hasPrev || !cur.Known || !prev.Known {
		return NetUsage{}
	}
	out := NetUsage{
		OutSegs:          sub(cur.OutSegs, prev.OutSegs),
		RetransSegs:      sub(cur.RetransSegs, prev.RetransSegs),
		ListenDrops:      sub(cur.ListenDrops, prev.ListenDrops),
		ListenOverflows:  sub(cur.ListenOverflows, prev.ListenOverflows),
		SyncookiesFailed: sub(cur.SyncookiesFailed, prev.SyncookiesFailed),
		Measured:         true,
	}
	if out.OutSegs > 0 {
		out.RetransPct = Round2(float64(out.RetransSegs) * 100 / float64(out.OutSegs))
	}
	return out
}

// computeThrottleRate turns two CFS counter readings into the rate inside the
// window. A window in which no enforcement period elapsed proves nothing and
// is reported as unmeasured rather than as a clean zero: "0 of 0 periods" is
// not a finding, it is an absence of one.
func computeThrottleRate(prev, cur probe.CPUThrottle, hasPrev bool) ThrottleRate {
	if !hasPrev || !prev.Known || !cur.Known {
		return ThrottleRate{}
	}
	periods := sub(cur.Periods, prev.Periods)
	if periods == 0 {
		return ThrottleRate{}
	}
	return ThrottleRate{
		Periods:   periods,
		Throttled: sub(cur.Throttled, prev.Throttled),
		Measured:  true,
	}
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

// floorOf returns the smallest value a candidate must reach to be named: a
// twentieth of the leader, but never zero. A floor of zero lets a process that
// did nothing at all clear it, and the heading then names the idle as suspects.
func floorOf(leader uint64) uint64 {
	if f := leader / culpritFloorDivisor; f > 0 {
		return f
	}
	return 1
}

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
	floor := floorOf(leader)

	var out []Culprit
	for _, d := range diffs {
		if !d.ioOK || d.read+d.write < floor {
			continue
		}
		out = append(out, Culprit{
			PID:     d.proc.PID,
			Name:    d.proc.Name,
			User:    d.proc.User,
			Cmdline: d.proc.Cmdline,
			Note: a.msg("读 %s，写 %s", "read %s, wrote %s",
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
	floor := floorOf(leader)

	var out []Culprit
	for _, d := range diffs {
		if d.ticks < floor {
			continue
		}
		out = append(out, Culprit{
			PID:     d.proc.PID,
			Name:    d.proc.Name,
			User:    d.proc.User,
			Cmdline: d.proc.Cmdline,
			Note: a.msg("占用 %s 核", "using %s cores",
				trimFloat(CoresUsed(d.ticks, a.interval))),
			LastCPU:      d.proc.LastCPU,
			LastCPUKnown: d.proc.LastCPUKnown,
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
	floor := floorOf(procs[0].RSSPages)

	page := uint64(os.Getpagesize())
	var out []Culprit
	for _, p := range procs {
		if p.RSSPages < floor {
			continue
		}
		out = append(out, Culprit{
			PID:     p.PID,
			Name:    p.Name,
			User:    p.User,
			Cmdline: p.Cmdline,
			Note:    a.msg("常驻内存 %s", "%s resident", HumanBytes(p.RSSPages*page)),
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
