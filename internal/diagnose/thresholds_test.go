package diagnose

import (
	"math"
	"testing"
)

// An inverted severity ladder makes the higher level unreachable, so a
// catastrophic number reports as a mere warning. It still appears, which is
// exactly why nobody would notice.
func TestWithDefaultsKeepsSeverityLaddersOrdered(t *testing.T) {
	cases := []struct {
		name string
		in   Thresholds
		want func(Thresholds) float64
	}{
		{"critical below warn", Thresholds{IOFullWarn: 80, IOFullCritical: 5},
			func(t Thresholds) float64 { return t.IOFullCritical }},
		{"warn below watch", Thresholds{CPUThrottleWatch: 50, CPUThrottleWarn: 10},
			func(t Thresholds) float64 { return t.CPUThrottleWarn }},
		{"memory critical below warn", Thresholds{MemFullWarn: 90, MemFullCritical: 20},
			func(t Thresholds) float64 { return t.MemFullCritical }},
		{"cpu some warn below watch", Thresholds{CPUSomeWatch: 60, CPUSomeWarn: 5},
			func(t Thresholds) float64 { return t.CPUSomeWarn }},
	}
	for _, c := range cases {
		got := c.in.WithDefaults()
		lo, hi := c.want(c.in), c.want(got)
		if hi < lo {
			t.Errorf("%s: higher level %v ended below the lower level %v — it can never fire",
				c.name, hi, lo)
		}
	}
}

// A correctly ordered pair must survive untouched, including the exact values.
func TestWithDefaultsPreservesValidLadders(t *testing.T) {
	in := Thresholds{
		IOFullWarn: 30, IOFullCritical: 60,
		CPUThrottleWatch: 10, CPUThrottleWarn: 25, CPUThrottleCritical: 50,
	}
	got := in.WithDefaults()
	if got.IOFullWarn != 30 || got.IOFullCritical != 60 {
		t.Errorf("io ladder changed: %v / %v", got.IOFullWarn, got.IOFullCritical)
	}
	if got.CPUThrottleWatch != 10 || got.CPUThrottleWarn != 25 || got.CPUThrottleCritical != 50 {
		t.Errorf("throttle ladder changed: %v / %v / %v",
			got.CPUThrottleWatch, got.CPUThrottleWarn, got.CPUThrottleCritical)
	}
}

// Storing a negative threshold would print "已设置" and then judge against
// the default on the next run, because WithDefaults replaces anything <= 0.
func TestSetRejectsNegativeAndNonFinite(t *testing.T) {
	var t1 Thresholds
	if t1.Set("io_full_warn", -5) {
		t.Error("Set accepted -5; WithDefaults would silently replace it with the default")
	}
	if t1.Set("cpu_some_warn", math.NaN()) {
		t.Error("Set accepted NaN")
	}
	if t1.Set("cpu_some_warn", math.Inf(1)) {
		t.Error("Set accepted +Inf")
	}
	if got, _ := t1.Get("io_full_warn"); got != 0 {
		t.Errorf("a rejected value was stored: %v", got)
	}
	// Zero stays legal: it is the documented "not configured" marker.
	if !t1.Set("io_full_warn", 0) {
		t.Error("Set rejected 0, which is a valid \"not configured\" value")
	}
	// A known key must still be accepted with a real value.
	if !t1.Set("io_full_warn", 42.5) {
		t.Error("Set rejected a valid value")
	}
	if got, _ := t1.Get("io_full_warn"); got != 42.5 {
		t.Errorf("io_full_warn = %v, want 42.5", got)
	}
}

func TestSetRejectsUnknownKey(t *testing.T) {
	var t1 Thresholds
	if t1.Set("no_such_threshold", 10) {
		t.Error("Set accepted an unknown key")
	}
}
