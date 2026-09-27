package probe

// CPUCore is one logical CPU's cumulative counters, from a "cpuN" line of
// /proc/stat.
type CPUCore struct {
	ID    int      `json:"id"`
	Times CPUTimes `json:"times"`
}

// Delta computes this core's counters across the sample window.
func (c CPUCore) Delta(prev CPUCore) CPUTimes { return c.Times.Delta(prev.Times) }

// CoreLoad is one core's utilisation over the sample window, in percent.
type CoreLoad struct {
	ID     int     `json:"id"`
	Busy   float64 `json:"busy"`
	IOWait float64 `json:"iowait"`
	Steal  float64 `json:"steal"`
}

// Peaked reports whether this core is effectively saturated.
func (c CoreLoad) Peaked() bool { return c.Busy >= 90 }

// CoreLoads computes per-core utilisation between two samples.
//
// Cores are matched by ID rather than by position: a CPU can be offlined and
// re-onlined between samples, which renumbers the list, and comparing position
// i to position i would then silently mix up two different cores.
func CoreLoads(prev, cur []CPUCore) []CoreLoad {
	index := make(map[int]CPUCore, len(prev))
	for _, c := range prev {
		index[c.ID] = c
	}

	out := make([]CoreLoad, 0, len(cur))
	for _, c := range cur {
		p, ok := index[c.ID]
		if !ok {
			continue // this core did not exist in the previous sample
		}
		d := c.Delta(p)
		total := d.Total()
		if total == 0 {
			continue // the core was offlined for the whole window
		}
		out = append(out, CoreLoad{
			ID:     c.ID,
			Busy:   pct(d.Busy(), total),
			IOWait: pct(d.IOWait, total),
			Steal:  pct(d.Steal, total),
		})
	}
	return out
}
