// Package report renders a diagnosis for a terminal or for a machine.
package report

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/Oxen112774/plumb/internal/diagnose"
)

const (
	ansiReset = "\x1b[0m"
	ansiBold  = "\x1b[1m"
	ansiDim   = "\x1b[90m"
)

func severityANSI(s diagnose.Severity) string {
	switch s {
	case diagnose.SeverityCritical:
		return "\x1b[1;31m"
	case diagnose.SeverityWarning:
		return "\x1b[1;33m"
	case diagnose.SeverityWatch:
		return "\x1b[36m"
	case diagnose.SeverityOK:
		return "\x1b[32m"
	default:
		return "\x1b[90m"
	}
}

func paint(s, code string, color bool) string {
	if !color || s == "" {
		return s
	}
	return code + s + ansiReset
}

// Text writes a human-readable report.
func Text(w io.Writer, r diagnose.Report, color bool) error {
	var b strings.Builder

	b.WriteString(paint("plumb · 服务器卡顿归因", ansiBold, color))
	b.WriteString("\n")

	var ident []string
	if r.System.Hostname != "" {
		ident = append(ident, r.System.Hostname)
	}
	if r.System.Kernel != "" {
		ident = append(ident, "Linux "+r.System.Kernel)
	}
	if r.System.Cores > 0 {
		ident = append(ident, fmt.Sprintf("%d 核", r.System.Cores))
	}
	if r.System.Uptime > 0 {
		ident = append(ident,
			"已运行 "+diagnose.HumanDuration(time.Duration(r.System.Uptime)*time.Second))
	}
	if len(ident) > 0 {
		b.WriteString(paint(strings.Join(ident, " · "), ansiDim, color))
		b.WriteString("\n")
	}

	mode := fmt.Sprintf("采样 %s", r.StartedAt.Format("2006-01-02 15:04:05"))
	if r.SingleSample {
		mode += " · 单次采样（无速率）"
	} else {
		mode += fmt.Sprintf(" · 间隔 %s · 两次采样", r.Interval)
	}
	b.WriteString(paint(mode, ansiDim, color))
	b.WriteString("\n")

	// --- findings ---
	b.WriteString("\n")
	b.WriteString(paint(fmt.Sprintf("判定（%d 项）", len(r.Findings)), ansiBold, color))
	b.WriteString("\n")

	if len(r.Findings) == 0 {
		b.WriteString("  ")
		b.WriteString(paint("+  未发现明显瓶颈", severityANSI(diagnose.SeverityOK), color))
		b.WriteString("\n")
	}

	for _, f := range r.Findings {
		b.WriteString("\n")
		label := fmt.Sprintf("%s  %s", f.Severity.Marker(), f.Area)
		b.WriteString("  ")
		b.WriteString(paint(label, severityANSI(f.Severity), color))
		b.WriteString("\n")

		// headline, wrapped to keep the report readable in a narrow window
		for _, line := range wrap(f.Headline, 66) {
			b.WriteString("      " + line + "\n")
		}

		if len(f.Culprits) > 0 {
			b.WriteString("\n      " + paint("元凶进程", ansiDim, color) + "\n")
			for _, c := range f.Culprits {
				name := c.Name
				if c.User != "" {
					name = fmt.Sprintf("%-18s %s", name, "["+c.User+"]")
				}
				b.WriteString(fmt.Sprintf("        PID %-7d %s\n", c.PID, name))
				b.WriteString(fmt.Sprintf("        %-12s %s\n", "", c.Note))
				if c.Cmdline != "" && c.Cmdline != c.Name {
					b.WriteString(fmt.Sprintf("        %-12s %s\n", "",
						paint(c.Cmdline, ansiDim, color)))
				}
			}
		}

		if len(f.Evidence) > 0 {
			b.WriteString("\n      " + paint("证据", ansiDim, color) + "\n")
			for _, e := range f.Evidence {
				b.WriteString(fmt.Sprintf("        %-18s %s\n", e.Source, e.Detail))
			}
		}

		if f.Advice != "" {
			b.WriteString("\n      " + paint("怎么办", ansiDim, color) + "\n")
			for _, line := range wrap(f.Advice, 66) {
				b.WriteString("        " + line + "\n")
			}
		}
	}

	// --- overview ---
	b.WriteString("\n")
	b.WriteString(paint("概况", ansiBold, color))
	b.WriteString("\n")
	for _, line := range overviewLines(r) {
		b.WriteString("  " + line + "\n")
	}

	// --- unmeasured ---
	if len(r.Errors) > 0 {
		b.WriteString("\n")
		b.WriteString(paint("未能测到（这些项不参与判定）", ansiBold, color))
		b.WriteString("\n")
		for _, e := range r.Errors {
			for _, line := range wrap("· "+e, 66) {
				b.WriteString("  " + line + "\n")
			}
		}
	}

	b.WriteString("\n")
	_, err := io.WriteString(w, b.String())
	return err
}

func overviewLines(r diagnose.Report) []string {
	var out []string

	if !r.SingleSample {
		out = append(out, fmt.Sprintf(
			"CPU      繁忙 %.1f%%   用户 %.1f%%   系统 %.1f%%   等磁盘 %.1f%%   被抢占 %.1f%%",
			r.CPUUsage.Busy, r.CPUUsage.User, r.CPUUsage.System,
			r.CPUUsage.IOWait, r.CPUUsage.Steal))
	}

	if r.Memory.TotalKB > 0 {
		line := fmt.Sprintf("内存     已用 %.1f%%（%s / %s）",
			r.Memory.UsedPercent(),
			diagnose.HumanBytes(r.Memory.UsedKB()*1024),
			diagnose.HumanBytes(r.Memory.TotalKB*1024))
		if r.Memory.SwapTotalKB > 0 {
			line += fmt.Sprintf("   交换 %.1f%%", r.Memory.SwapPercent())
		}
		out = append(out, line)
	}

	if r.System.Cores > 0 {
		out = append(out, fmt.Sprintf("负载     load1 %.2f，%d 核，每核 %.2f",
			r.System.Load[0], r.System.Cores, r.System.LoadPerCore()))
	}

	if r.Pressure.Supported {
		out = append(out, fmt.Sprintf("压力     cpu some %.2f%%   io full %.2f%%   memory full %.2f%%",
			r.Pressure.CPU.Some.Avg10,
			r.Pressure.IO.Full.Avg10,
			r.Pressure.Memory.Full.Avg10))
	}

	return out
}

// wrap breaks s into lines of at most width display columns, counting East
// Asian wide runes as two columns and preferring to break at a space.
//
// Counting runes instead of columns would let a Chinese headline overflow the
// terminal, since each ideograph occupies two cells. Breaking wherever the
// limit lands turns English advice text into noise, so a break at the last
// space wins when one is available.
func wrap(s string, width int) []string {
	if s == "" {
		return nil
	}
	var (
		lines     []string
		cur       []rune
		col       int
		lastSpace = -1
	)

	for _, r := range s {
		w := runeWidth(r)
		if col+w > width && len(cur) > 0 {
			// A closing punctuation mark may not open a line (行首禁则).
			// Overflowing by one glyph is the lesser defect.
			if noLineStart(r) {
				cur = append(cur, r)
				col += w
				continue
			}
			if lastSpace > 0 {
				lines = append(lines, string(cur[:lastSpace]))
				cur = append([]rune(nil), cur[lastSpace+1:]...)
			} else {
				lines = append(lines, string(cur))
				cur = cur[:0]
			}
			col = displayWidth(cur)
			lastSpace = lastSpaceIndex(cur)
		}
		if r == ' ' {
			lastSpace = len(cur)
		}
		cur = append(cur, r)
		col += w
	}
	if len(cur) > 0 {
		lines = append(lines, string(cur))
	}
	return lines
}

// noLineStart reports whether r is a character that Chinese typography forbids
// at the start of a line.
func noLineStart(r rune) bool {
	switch r {
	case '。', '，', '、', '；', '：', '？', '！', '·', '…', '—',
		'）', '》', '」', '』', '】', '〕', '｝', '］':
		return true
	}
	return false
}

func displayWidth(rs []rune) int {
	n := 0
	for _, r := range rs {
		n += runeWidth(r)
	}
	return n
}

func lastSpaceIndex(rs []rune) int {
	for i := len(rs) - 1; i >= 0; i-- {
		if rs[i] == ' ' {
			return i
		}
	}
	return -1
}

// runeWidth returns the display width of a rune: 2 for CJK and fullwidth
// forms, 1 otherwise.
func runeWidth(r rune) int {
	switch {
	case r >= 0x1100 && r <= 0x115F, // Hangul Jamo
		r >= 0x2E80 && r <= 0xA4CF, // CJK radicals .. Yi
		r >= 0xAC00 && r <= 0xD7A3, // Hangul syllables
		r >= 0xF900 && r <= 0xFAFF, // CJK compatibility ideographs
		r >= 0xFE30 && r <= 0xFE6F, // CJK compatibility forms
		r >= 0xFF00 && r <= 0xFF60, // fullwidth forms
		r >= 0xFFE0 && r <= 0xFFE6:
		return 2
	default:
		return 1
	}
}

// JSON writes the report as indented JSON.
func JSON(w io.Writer, r diagnose.Report) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(r)
}
