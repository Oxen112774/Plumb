package diagnose

import (
	"testing"
	"time"
)

// The label has to match the arithmetic: 41,984,000 bytes is 42.0 MB but
// 40.0 MiB, and a report that prints the binary number under a decimal label
// disagrees with the provider's console by 5%, which reads as a bug.
func TestHumanBytesUsesIECLabels(t *testing.T) {
	cases := []struct {
		in   uint64
		want string
	}{
		{0, "0 B"},
		{512, "512 B"},
		{1024, "1.0 KiB"},
		{1048576, "1.0 MiB"},
		{1073741824, "1.0 GiB"},
		{41984000, "40.0 MiB"},
		{16777216000, "15.6 GiB"},
	}
	for _, c := range cases {
		if got := HumanBytes(c.in); got != c.want {
			t.Errorf("HumanBytes(%d) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestHumanRateDividesByTheWindow(t *testing.T) {
	if got := HumanRate(2*1024*1024, time.Second); got != "2.0 MiB/s" {
		t.Errorf("HumanRate over 1s = %q, want 2.0 MiB/s", got)
	}
	// The same byte count spread over two seconds is half the rate.
	if got := HumanRate(2*1024*1024, 2*time.Second); got != "1.0 MiB/s" {
		t.Errorf("HumanRate over 2s = %q, want 1.0 MiB/s", got)
	}
}

func TestRound2(t *testing.T) {
	if got := Round2(12.3456); got != 12.35 {
		t.Errorf("Round2(12.3456) = %v, want 12.35", got)
	}
}

func TestTrimFloatDropsTrailingZeros(t *testing.T) {
	cases := []struct {
		in   float64
		want string
	}{
		{12, "12"},
		{12.5, "12.5"},
		{1.10, "1.1"},
		{0.25, "0.25"},
		{0, "0"},
	}
	for _, c := range cases {
		if got := trimFloat(c.in); got != c.want {
			t.Errorf("trimFloat(%v) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestHumanDuration(t *testing.T) {
	cases := []struct {
		in   time.Duration
		want string
	}{
		{0, "0 秒"},
		{45 * time.Minute, "45 分"},
		{90 * time.Minute, "1 小时 30 分"},
		{26 * time.Hour, "1 天 2 小时"},
	}
	for _, c := range cases {
		if got := HumanDuration(c.in); got != c.want {
			t.Errorf("HumanDuration(%v) = %q, want %q", c.in, got, c.want)
		}
	}
}
