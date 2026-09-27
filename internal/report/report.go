// Package report renders a diagnosis for a terminal or for a machine.
package report

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/Oxen112774/plumb/internal/diagnose"
	"github.com/Oxen112774/plumb/internal/probe"
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
				for _, line := range noteLines(c.Note) {
					b.WriteString(fmt.Sprintf("        %-12s %s\n", "", line))
				}
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

// noteLines breaks a culprit note into display lines. A note uses explicit
// newlines to separate independent facts — one per blocked thread, then the
// files it holds — and each of those is then wrapped to the width left over by
// the note's hanging indent.
func noteLines(note string) []string {
	var out []string
	for _, line := range strings.Split(note, "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		out = append(out, wrap(line, 54)...)
	}
	return out
}

// coreLine summarises per-core utilisation.
//
// On a small machine every core is listed, because seeing one at 99% next to
// three at 20% is exactly the point. On a large one only the loaded cores are,
// since sixty-four numbers is not a summary.
func coreLine(cores []probe.CoreLoad) string {
	const smallMachine = 8

	parts := make([]string, 0, len(cores))
	if len(cores) <= smallMachine {
		for _, c := range cores {
			parts = append(parts, fmt.Sprintf("CPU%d %.0f%%", c.ID, c.Busy))
		}
		return strings.Join(parts, "  ")
	}

	hot := 0
	for _, c := range cores {
		if c.Busy < 80 {
			continue
		}
		hot++
		if len(parts) < 4 {
			parts = append(parts, fmt.Sprintf("CPU%d %.0f%%", c.ID, c.Busy))
		}
	}
	if hot == 0 {
		return fmt.Sprintf("%d 核，没有核心超过 80%%", len(cores))
	}
	s := strings.Join(parts, "  ")
	if hot > len(parts) {
		s += fmt.Sprintf("，另有 %d 个核心超过 80%%", hot-len(parts))
	}
	return s
}

func overviewLines(r diagnose.Report) []string {
	var out []string

	if !r.SingleSample {
		out = append(out, fmt.Sprintf(
			"CPU      繁忙 %.1f%%   用户 %.1f%%   系统 %.1f%%   等磁盘 %.1f%%   被抢占 %.1f%%",
			r.CPUUsage.Busy, r.CPUUsage.User, r.CPUUsage.System,
			r.CPUUsage.IOWait, r.CPUUsage.Steal))
	}

	if len(r.Cores) > 0 {
		out = append(out, "核心     "+coreLine(r.Cores))
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

	if r.Threads.Total > 0 {
		line := fmt.Sprintf("线程     共 %d 个", r.Threads.Total)
		if r.Threads.Blocked > 0 {
			line += fmt.Sprintf("，其中 %d 个处于 D 状态（不可中断，kill 不掉）", r.Threads.Blocked)
		} else {
			line += "，没有被卡住的"
		}
		out = append(out, line)
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
// Asian wide runes as two columns and preferring to break where a line may
// legally end.
//
// Counting runes instead of columns would let a Chinese headline overflow the
// terminal, since each ideograph occupies two cells. Breaking wherever the
// limit happens to land turns English advice into noise ("fs" / "ync") and
// splits file paths mid-component ("/var/lib/mysql/i" / "bdata1"), so a break
// at the last legal point wins when one exists. An explicit newline is always
// a hard break.
func wrap(s string, width int) []string {
	if s == "" {
		return nil
	}
	var (
		lines = []string(nil)
		cur   []rune
		col   int
	)

	for _, r := range s {
		if r == '\n' {
			lines = append(lines, string(cur))
			cur = cur[:0]
			col = 0
			continue
		}
		w := runeWidth(r)
		if col+w > width && len(cur) > 0 {
			// A closing punctuation mark may not open a line (行首禁则).
			// Overflowing by one glyph is the lesser defect.
			if noLineStart(r) {
				cur = append(cur, r)
				col += w
				continue
			}
			lineEnd, restStart := breakPoint(cur, width)
			if lineEnd > 0 {
				lines = append(lines, string(cur[:lineEnd]))
				cur = append([]rune(nil), cur[restStart:]...)
			} else {
				lines = append(lines, string(cur))
				cur = cur[:0]
			}
			col = displayWidth(cur)
		}
		cur = append(cur, r)
		col += w
	}
	if len(cur) > 0 {
		lines = append(lines, string(cur))
	}
	return lines
}

// breakPoint reports where to end the current line and where the next one
// begins, or (0, 0) when the line has to be cut at the column limit.
//
// A separator — a space, CJK punctuation, the path separator — is the best
// break available: it keeps English words and file paths whole. One is only
// worth taking from the second half of the budget onwards, though, because a
// separator near the start would leave most of the line empty. The comparison
// is in display columns, not runes: in a Chinese line the two differ by a
// factor of two, and a rune index would let a separator at the very start of
// the line count as being halfway across it.
//
// Failing that, a break between two ideographs is legal in Chinese, and taking
// it beats leaving a third of every other line blank. Latin text has no such
// freedom, so a word with no separator near it is cut at the limit.
//
// Neither rule may place a break where the script forbids one: not before a
// character that cannot start a line, not after one that cannot end it (行首、
// 行尾禁则), and never in a way that strands a space at either edge.
func breakPoint(rs []rune, width int) (lineEnd, restStart int) {
	if i := lastSeparator(rs); i > 0 && displayWidth(rs[:i]) >= width/2 {
		if rs[i] == ' ' {
			return i, i + 1 // a space is consumed by the break
		}
		return i + 1, i + 1 // a separator ends the line it closes
	}

	for i := len(rs); i > 0; i-- {
		if noLineEnd(rs[i-1]) {
			continue
		}
		if i < len(rs) {
			if rs[i] == ' ' {
				return i, i + 1 // a space must not open the next line either
			}
			if noLineStart(rs[i]) {
				continue
			}
		}
		if runeWidth(rs[i-1]) == 2 {
			// There is an ideograph on the left of the break and no
			// forbidden character on the right, so it is legal here.
			return i, i
		}
	}
	return 0, 0
}

// lastSeparator returns the index of the last preferred break character in rs,
// or -1 when it holds none.
func lastSeparator(rs []rune) int {
	for i := len(rs) - 1; i > 0; i-- {
		if rs[i] == ' ' || strings.ContainsRune("、，；：/", rs[i]) {
			return i
		}
	}
	return -1
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

// noLineEnd reports whether r is a character that Chinese typography forbids at
// the end of a line: an opening bracket or quote with nothing after it yet.
func noLineEnd(r rune) bool {
	switch r {
	case '（', '《', '「', '『', '【', '〔', '｛', '［', '〈', '“', '‘':
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
