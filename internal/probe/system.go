package probe

import (
	"fmt"
	"strings"
)

// System holds identity and load information.
type System struct {
	Hostname string     `json:"hostname"`
	Kernel   string     `json:"kernel"`
	Cores    int        `json:"cores"`
	Uptime   float64    `json:"uptime_seconds"`
	Load     [3]float64 `json:"load"`
}

// LoadPerCore normalises the 1-minute load average by core count.
//
// Raw load average is close to meaningless on its own: 4.0 is an idle machine
// with 16 cores and a machine in serious trouble with 2. The number that
// carries information is how many runnable tasks are competing per core.
func (s System) LoadPerCore() float64 {
	if s.Cores <= 0 {
		return s.Load[0]
	}
	return s.Load[0] / float64(s.Cores)
}

// ReadSystem reads hostname, kernel, uptime and load average.
func ReadSystem(r Root, cores int) (System, error) {
	s := System{Cores: cores}

	if data, err := r.ReadFile("proc/sys/kernel/hostname"); err == nil {
		s.Hostname = strings.TrimSpace(string(data))
	}
	if data, err := r.ReadFile("proc/sys/kernel/osrelease"); err == nil {
		s.Kernel = strings.TrimSpace(string(data))
	}

	data, err := r.ReadFile("proc/loadavg")
	if err != nil {
		return s, fmt.Errorf("read proc/loadavg: %w", err)
	}
	fields := strings.Fields(string(data))
	if len(fields) < 3 {
		return s, fmt.Errorf("proc/loadavg: %d fields, want at least 3", len(fields))
	}
	for i := 0; i < 3; i++ {
		v, err := atof(fields[i])
		if err != nil {
			return s, fmt.Errorf("proc/loadavg field %d (%q): %w", i, fields[i], err)
		}
		s.Load[i] = v
	}

	if data, err := r.ReadFile("proc/uptime"); err == nil {
		if fields := strings.Fields(string(data)); len(fields) > 0 {
			if v, err := atof(fields[0]); err == nil {
				s.Uptime = v
			}
		}
	}

	return s, nil
}
