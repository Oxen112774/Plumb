package probe

import (
	"fmt"
	"strings"
)

// PSIValues is one line of a PSI (Pressure Stall Information) file.
//
// The averages are percentages of wall-clock time: avg10 = 17.4 means that
// over the last 10 seconds, 17.4% of the time the condition held.
type PSIValues struct {
	Avg10  float64 `json:"avg10"`
	Avg60  float64 `json:"avg60"`
	Avg300 float64 `json:"avg300"`
	Total  uint64  `json:"total_us"`
}

// PSIStall holds the "some" and "full" lines for one resource.
//
//	some — at least one task was stalled waiting on this resource
//	full — every runnable task was stalled; the machine made no forward
//	       progress at all. This is the number that corresponds to what a
//	       user means when they say "the server froze".
type PSIStall struct {
	Some    PSIValues `json:"some"`
	Full    PSIValues `json:"full"`
	HasSome bool      `json:"has_some"`
	HasFull bool      `json:"has_full"`
}

// Pressure is the PSI state of the machine.
//
// This is the measurement a utilisation percentage cannot give you. A disk
// can sit at 100% utilisation processing a deep queue of parallel requests
// without anyone waiting, and it can sit at 12% utilisation while a single
// fsync blocks an entire request path. PSI measures the waiting, which is
// what actually hurts.
type Pressure struct {
	CPU    PSIStall `json:"cpu"`
	IO     PSIStall `json:"io"`
	Memory PSIStall `json:"memory"`

	// Supported is false on kernels before 4.20 and whenever PSI is not
	// enabled (CONFIG_PSI_DEFAULT_DISABLED with no psi=1 on the cmdline).
	// Callers must degrade to whatever else they have rather than treating
	// the zero values as "no pressure".
	Supported bool `json:"supported"`
}

// ReadPressure reads /proc/pressure/{cpu,io,memory}.
func ReadPressure(r Root) Pressure {
	var p Pressure
	p.CPU, _ = readPSI(r, "cpu")
	p.IO, _ = readPSI(r, "io")
	p.Memory, _ = readPSI(r, "memory")
	p.Supported = p.CPU.HasSome || p.IO.HasFull || p.Memory.HasFull
	return p
}

func readPSI(r Root, name string) (PSIStall, error) {
	data, err := r.ReadFile("proc/pressure/" + name)
	if err != nil {
		return PSIStall{}, err
	}
	var st PSIStall
	err = lines(data, func(line string) error {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			return nil
		}
		v, err := parsePSIValues(fields[1:])
		if err != nil {
			return err
		}
		switch fields[0] {
		case "some":
			st.Some = v
			st.HasSome = true
		case "full":
			st.Full = v
			st.HasFull = true
		}
		return nil
	})
	if err != nil {
		return PSIStall{}, fmt.Errorf("parse proc/pressure/%s: %w", name, err)
	}
	return st, nil
}

// parsePSIValues parses the key=value tail of a PSI line. Unknown keys are
// ignored so a newer kernel adding a field does not break the parser, but a
// known key with an unparseable value is an error.
func parsePSIValues(fields []string) (PSIValues, error) {
	var v PSIValues
	for _, f := range fields {
		key, val, ok := strings.Cut(f, "=")
		if !ok {
			return PSIValues{}, fmt.Errorf("field %q is not key=value", f)
		}
		var err error
		switch key {
		case "avg10":
			v.Avg10, err = atof(val)
		case "avg60":
			v.Avg60, err = atof(val)
		case "avg300":
			v.Avg300, err = atof(val)
		case "total":
			v.Total, err = atou(val)
		default:
			continue
		}
		if err != nil {
			return PSIValues{}, fmt.Errorf("%s=%q: %w", key, val, err)
		}
	}
	return v, nil
}
