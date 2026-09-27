package diagnose

import (
	"fmt"
	"math"
	"strings"
	"time"
)

// Round2 rounds to two decimals, leaving NaN and Inf untouched so a bad
// measurement stays visible instead of becoming a plausible-looking number.
func Round2(v float64) float64 {
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return v
	}
	return math.Round(v*100) / 100
}

// trimFloat renders a float without trailing zeros: 12.00 -> "12.00" is
// shortened to "12", 0.34 stays "0.34".
func trimFloat(v float64) string {
	s := fmt.Sprintf("%.2f", v)
	if strings.Contains(s, ".") {
		s = strings.TrimRight(s, "0")
		s = strings.TrimRight(s, ".")
	}
	if s == "" || s == "-" {
		return "0"
	}
	return s
}

// HumanBytes formats a byte count using binary multiples, labelled as such.
//
// The label matters: 41,984,000 bytes is 42.0 MB but 40.0 MiB, and a report
// that prints the binary number under a decimal label is off by 5% — enough to
// make an operator disagree with their provider's console and conclude the tool
// is lying. Linux reports memory and block counters in binary units, so binary
// it is, with the IEC names that say so.
func HumanBytes(b uint64) string {
	const unit = 1024
	if b < unit {
		return fmt.Sprintf("%d B", b)
	}
	const units = "KMGTPE"
	div, exp := uint64(unit), 0
	for n := b / unit; n >= unit && exp < len(units)-1; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(b)/float64(div), units[exp])
}

// HumanRate formats a byte count as a per-second rate over the sample window.
func HumanRate(bytes uint64, d time.Duration) string {
	if d <= 0 {
		return HumanBytes(bytes)
	}
	return HumanBytes(uint64(float64(bytes)/d.Seconds())) + "/s"
}

// HumanDuration renders an uptime in the largest two units that matter.
func HumanDuration(d time.Duration) string {
	if d <= 0 {
		return "0 秒"
	}
	days := int(d.Hours()) / 24
	hours := int(d.Hours()) % 24
	mins := int(d.Minutes()) % 60
	switch {
	case days > 0:
		return fmt.Sprintf("%d 天 %d 小时", days, hours)
	case hours > 0:
		return fmt.Sprintf("%d 小时 %d 分", hours, mins)
	default:
		return fmt.Sprintf("%d 分", mins)
	}
}
