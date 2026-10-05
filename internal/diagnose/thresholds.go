package diagnose

import (
	"math"
	"strings"
)

// Thresholds are the severity cut-offs the rules judge by.
//
// They are data rather than constants because a threshold is an opinion, and
// an opinion the user cannot change is one the user cannot disagree with.
// Every field defaults to the value documented in DefaultThresholds; a zero
// or negative field means "not set", and WithDefaults replaces it.
//
// The set is deliberately the PSI lines plus the cgroup memory wall — the
// numbers whose right value depends on the workload. Thresholds that are
// physical facts rather than opinions (a disk at 100% is full; a swap that is
// touched at all is a symptom) stay constants in rules.go, because offering a
// knob for them only invites a number nobody can justify.
type Thresholds struct {
	// IOFullWarn / IOFullCritical cut PSI io `full` avg10: the share of the
	// window in which every runnable task was blocked on storage.
	IOFullWarn     float64 `json:"io_full_warn"`
	IOFullCritical float64 `json:"io_full_critical"`
	// IOSomeWatch / IOSomeWarn cut PSI io `some` avg10: at least one task
	// blocked. The bar is higher because a single waiter is normal.
	IOSomeWatch float64 `json:"io_some_watch"`
	IOSomeWarn  float64 `json:"io_some_warn"`

	MemFullWarn     float64 `json:"mem_full_warn"`
	MemFullCritical float64 `json:"mem_full_critical"`
	MemSomeWatch    float64 `json:"mem_some_watch"`

	// CPUSomeWatch / CPUSomeWarn cut PSI cpu `some` avg10: runnable but
	// unscheduled. There is no critical — a saturated CPU is reported by the
	// core-skew and steal rules, which look at where the time went.
	CPUSomeWatch float64 `json:"cpu_some_watch"`
	CPUSomeWarn  float64 `json:"cpu_some_warn"`

	// CgroupMemWatch / CgroupMemWarn cut usage against memory.max. These are
	// percentages of a hard limit, not of a machine, so they read like disk
	// space: 90% of a wall you will hit is late, not early.
	CgroupMemWatch float64 `json:"cgroup_mem_watch"`
	CgroupMemWarn  float64 `json:"cgroup_mem_warn"`

	// CPUThrottleWatch / Warn / Critical cut the share of CFS enforcement
	// periods that ended with this cgroup stopped for using its quota up.
	//
	// Unlike every other line here this one is not a share of wall-clock
	// time — it is a share of periods, and a throttled period is one in
	// which the workload was stopped for however long was left in it. With
	// the default 100 ms period, 2% of periods is already a stall every few
	// seconds, and 30% means the workload is being stopped for a third of
	// its scheduling opportunities.
	CPUThrottleWatch    float64 `json:"cpu_throttle_watch"`
	CPUThrottleWarn     float64 `json:"cpu_throttle_warn"`
	CPUThrottleCritical float64 `json:"cpu_throttle_critical"`
}

// DefaultThresholds returns the built-in cut-offs.
//
// Each one is argued for in the comment above the constants they replaced:
// the point of writing the reasoning down is that a threshold nobody can
// defend is a guess wearing a uniform.
func DefaultThresholds() Thresholds {
	return Thresholds{
		IOFullWarn:     psiIOFullWarn,
		IOFullCritical: psiIOFullCritical,
		IOSomeWatch:    psiIOSomeWatch,
		IOSomeWarn:     psiIOSomeWarn,

		MemFullWarn:     psiMemFullWarn,
		MemFullCritical: psiMemFullCritical,
		MemSomeWatch:    psiMemSomeWatch,

		CPUSomeWatch: psiCPUSomeWatch,
		CPUSomeWarn:  psiCPUSomeWarn,

		CgroupMemWatch: cgroupMemWatchPct,
		CgroupMemWarn:  cgroupMemWarnPct,

		CPUThrottleWatch:    cpuThrottleWatchPct,
		CPUThrottleWarn:     cpuThrottleWarnPct,
		CPUThrottleCritical: cpuThrottleCriticalPct,
	}
}

// WithDefaults replaces every unset field with its built-in default.
//
// A threshold of zero can mean two things — "no limit" and "not configured" —
// and the rules cannot tell them apart without this. Callers that build a
// partial Thresholds from a config file must pass it through here.
func (t Thresholds) WithDefaults() Thresholds {
	d := DefaultThresholds()
	if t.IOFullWarn <= 0 {
		t.IOFullWarn = d.IOFullWarn
	}
	if t.IOFullCritical <= 0 {
		t.IOFullCritical = d.IOFullCritical
	}
	if t.IOSomeWatch <= 0 {
		t.IOSomeWatch = d.IOSomeWatch
	}
	if t.IOSomeWarn <= 0 {
		t.IOSomeWarn = d.IOSomeWarn
	}
	if t.MemFullWarn <= 0 {
		t.MemFullWarn = d.MemFullWarn
	}
	if t.MemFullCritical <= 0 {
		t.MemFullCritical = d.MemFullCritical
	}
	if t.MemSomeWatch <= 0 {
		t.MemSomeWatch = d.MemSomeWatch
	}
	if t.CPUSomeWatch <= 0 {
		t.CPUSomeWatch = d.CPUSomeWatch
	}
	if t.CPUSomeWarn <= 0 {
		t.CPUSomeWarn = d.CPUSomeWarn
	}
	if t.CgroupMemWatch <= 0 {
		t.CgroupMemWatch = d.CgroupMemWatch
	}
	if t.CgroupMemWarn <= 0 {
		t.CgroupMemWarn = d.CgroupMemWarn
	}
	if t.CPUThrottleWatch <= 0 {
		t.CPUThrottleWatch = d.CPUThrottleWatch
	}
	if t.CPUThrottleWarn <= 0 {
		t.CPUThrottleWarn = d.CPUThrottleWarn
	}
	if t.CPUThrottleCritical <= 0 {
		t.CPUThrottleCritical = d.CPUThrottleCritical
	}
	t.enforceOrder()
	return t
}

// ordered lists the (lower, higher) field pairs that must stay in order.
//
// Each pair is a severity ladder: a rule escalates from the lower threshold
// to the higher one. If the higher is set below the lower, the higher
// severity becomes unreachable — the rule fires the lower one and can never
// reach the next, and nothing in the report says the ceiling was removed. A
// catastrophic number then reports as a mere warning, which is the worst way
// for a threshold to be wrong: it still shows up, so nobody notices.
//
// Config files are hand-edited, so an inverted pair is a plausible typo
// (swap two numbers, drop a digit). It is corrected here rather than
// rejected: a config that is slightly odd should still produce a working
// report, and the corrected value is visible in `plumb config`.
func (t *Thresholds) enforceOrder() {
	pairs := []struct {
		lo, hi         *float64
		loName, hiName string
	}{
		{&t.IOSomeWatch, &t.IOSomeWarn, "io_some_watch", "io_some_warn"},
		{&t.IOFullWarn, &t.IOFullCritical, "io_full_warn", "io_full_critical"},
		// No mem_some_watch -> mem_full_warn pair: those are different
		// measures (some counts tasks waiting, full counts all tasks
		// stalled) with different scales, so putting one above the other
		// means nothing. The memory ladder is warn -> critical only.
		{&t.MemFullWarn, &t.MemFullCritical, "mem_full_warn", "mem_full_critical"},
		{&t.CPUSomeWatch, &t.CPUSomeWarn, "cpu_some_watch", "cpu_some_warn"},
		{&t.CgroupMemWatch, &t.CgroupMemWarn, "cgroup_mem_watch", "cgroup_mem_warn"},
		{&t.CPUThrottleWatch, &t.CPUThrottleWarn, "cpu_throttle_watch", "cpu_throttle_warn"},
		{&t.CPUThrottleWarn, &t.CPUThrottleCritical, "cpu_throttle_warn", "cpu_throttle_critical"},
	}
	for _, p := range pairs {
		if *p.hi < *p.lo {
			*p.hi = *p.lo
		}
	}
}

// Names returns every field's config key, in the order plumb config prints
// them. Config parsing and the display share one list so a field cannot be
// settable but unprintable.
func (Thresholds) Names() []string {
	return []string{
		"io_full_warn", "io_full_critical", "io_some_watch", "io_some_warn",
		"mem_full_warn", "mem_full_critical", "mem_some_watch",
		"cpu_some_watch", "cpu_some_warn",
		"cgroup_mem_watch", "cgroup_mem_warn",
		"cpu_throttle_watch", "cpu_throttle_warn", "cpu_throttle_critical",
	}
}

// Get reads a field by config key.
func (t Thresholds) Get(name string) (float64, bool) {
	switch name {
	case "io_full_warn":
		return t.IOFullWarn, true
	case "io_full_critical":
		return t.IOFullCritical, true
	case "io_some_watch":
		return t.IOSomeWatch, true
	case "io_some_warn":
		return t.IOSomeWarn, true
	case "mem_full_warn":
		return t.MemFullWarn, true
	case "mem_full_critical":
		return t.MemFullCritical, true
	case "mem_some_watch":
		return t.MemSomeWatch, true
	case "cpu_some_watch":
		return t.CPUSomeWatch, true
	case "cpu_some_warn":
		return t.CPUSomeWarn, true
	case "cgroup_mem_watch":
		return t.CgroupMemWatch, true
	case "cgroup_mem_warn":
		return t.CgroupMemWarn, true
	case "cpu_throttle_watch":
		return t.CPUThrottleWatch, true
	case "cpu_throttle_warn":
		return t.CPUThrottleWarn, true
	case "cpu_throttle_critical":
		return t.CPUThrottleCritical, true
	}
	return 0, false
}

// Set writes a field by config key, reporting false for an unknown name.
//
// A negative or non-finite value is rejected rather than stored. Every field
// goes through WithDefaults, which replaces anything <= 0 with the built-in
// default — so accepting -5 would print "已设置" and then judge against 2.0 on
// the next run. That is the specific failure this project promises not to
// ship: a setting that looks like it took effect and did not.
func (t *Thresholds) Set(name string, v float64) bool {
	if v < 0 || math.IsNaN(v) || math.IsInf(v, 0) {
		return false
	}
	switch name {
	case "io_full_warn":
		t.IOFullWarn = v
	case "io_full_critical":
		t.IOFullCritical = v
	case "io_some_watch":
		t.IOSomeWatch = v
	case "io_some_warn":
		t.IOSomeWarn = v
	case "mem_full_warn":
		t.MemFullWarn = v
	case "mem_full_critical":
		t.MemFullCritical = v
	case "mem_some_watch":
		t.MemSomeWatch = v
	case "cpu_some_watch":
		t.CPUSomeWatch = v
	case "cpu_some_warn":
		t.CPUSomeWarn = v
	case "cgroup_mem_watch":
		t.CgroupMemWatch = v
	case "cgroup_mem_warn":
		t.CgroupMemWarn = v
	case "cpu_throttle_watch":
		t.CPUThrottleWatch = v
	case "cpu_throttle_warn":
		t.CPUThrottleWarn = v
	case "cpu_throttle_critical":
		t.CPUThrottleCritical = v
	default:
		return false
	}
	return true
}

// Delta is one threshold the user moved away from the built-in value.
type Delta struct {
	Name  string  `json:"name"`
	Value float64 `json:"value"`
	Built float64 `json:"builtin"`
}

// Changed lists the thresholds that differ from the built-in defaults.
func (t Thresholds) Changed() []Delta {
	d := DefaultThresholds()
	var out []Delta
	for _, n := range t.Names() {
		v, _ := t.Get(n)
		b, _ := d.Get(n)
		if v > 0 && v != b {
			out = append(out, Delta{Name: n, Value: v, Built: b})
		}
	}
	return out
}

// ChangedAmong is Changed filtered to the names a rule actually consulted.
func (t Thresholds) ChangedAmong(names ...string) []Delta {
	all := t.Changed()
	var out []Delta
	for _, d := range all {
		for _, n := range names {
			if d.Name == n {
				out = append(out, d)
				break
			}
		}
	}
	return out
}

func (a *analysis) thresholdNote(names ...string) []Evidence {
	ds := a.th.ChangedAmong(names...)
	if len(ds) == 0 {
		return nil
	}
	parts := make([]string, 0, len(ds))
	for _, d := range ds {
		parts = append(parts, a.msg(
			"%s=%s（内置 %s）", "%s=%s (built-in %s)",
			d.Name, trimFloat(d.Value), trimFloat(d.Built)))
	}
	joiner := a.msg("，", ", ")
	return []Evidence{{
		Source: "config",
		Detail: a.msg(
			"判定用的阈值已被配置改动："+strings.Join(parts, joiner)+" —— 这条结论的分界线不是 plumb 的默认线",
			"judged against thresholds changed in config: "+strings.Join(parts, joiner)+
				" — this verdict's cut-offs are not plumb's defaults",
		),
	}}
}
