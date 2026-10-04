package report

import (
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/Oxen112774/plumb/internal/diagnose"
	"github.com/Oxen112774/plumb/internal/i18n"
	"github.com/Oxen112774/plumb/internal/probe"
	"github.com/Oxen112774/plumb/internal/term"
)

// RenderOptions controls how a report is drawn.
type RenderOptions struct {
	Lang    i18n.Lang
	Glyphs  term.Glyphs
	Color   bool
	Verbose bool
	Width   int
}

// Hardcore writes the dense, professional report: header status bar, PSI
// sparklines, a blocked-thread table, then findings and overview.
//
// This is the default view. It is designed to be scanned in under five seconds:
// the top line says who and how loaded, the sparklines say which resource is
// stalled, and the table says exactly which threads are stuck and where.
func Hardcore(w io.Writer, r diagnose.Report, opts RenderOptions) error {
	var b strings.Builder

	writeHeader(&b, r, opts)
	writeSparklines(&b, r, opts)
	writeThreadTable(&b, r, opts)
	writeFindings(&b, r, opts)
	writeOverview(&b, r, opts)

	if len(r.Errors) > 0 {
		writeUnmeasured(&b, r, opts)
	}

	_, err := io.WriteString(w, b.String())
	return err
}

// Raw writes the report as plain text with no box drawing, no colour, no
// sparklines — suitable for grepping, logging, or piping into another tool.
func Raw(w io.Writer, r diagnose.Report, opts RenderOptions) error {
	opts.Glyphs = term.ASCII()
	opts.Color = false
	return Hardcore(w, r, opts)
}

// Paste writes the report in a format designed to be copied into a forum
// post, a chat message, or a ticket. It wraps the whole thing in marker
// lines and uses ASCII-only characters so nothing breaks in a web form.
func Paste(w io.Writer, r diagnose.Report, opts RenderOptions) error {
	opts.Glyphs = term.ASCII()
	opts.Color = false
	var b strings.Builder
	b.WriteString(i18n.Get(opts.Lang, i18n.PasteHeader) + "\n\n")
	if err := Hardcore(&b, r, opts); err != nil {
		return err
	}
	b.WriteString("\n" + i18n.Get(opts.Lang, i18n.PasteFooter) + "\n")
	_, err := io.WriteString(w, b.String())
	return err
}

// --- header ---

func writeHeader(b *strings.Builder, r diagnose.Report, opts RenderOptions) {
	g := opts.Glyphs
	t := opts.Lang

	// Title line: plumb vX | hostname | kernel | cores | uptime
	var parts []string
	parts = append(parts, paint("plumb", ansiBold, opts.Color))
	if r.System.Hostname != "" {
		parts = append(parts, r.System.Hostname)
	}
	if r.System.Kernel != "" {
		parts = append(parts, "Linux "+r.System.Kernel)
	}
	if r.System.Cores > 0 {
		parts = append(parts, fmt.Sprintf("%d %s", r.System.Cores, i18n.Get(t, i18n.Cores)))
	}
	if r.System.Uptime > 0 {
		parts = append(parts, "up "+uptime(t, time.Duration(r.System.Uptime)*time.Second))
	}
	b.WriteString(strings.Join(parts, " "+g.VD+" "))
	b.WriteString("\n")

	// Load line: load1 load5 load15 | per-core
	if r.System.Cores > 0 {
		load := fmt.Sprintf("load %.2f %.2f %.2f | %.2f/core",
			r.System.Load[0], r.System.Load[1], r.System.Load[2],
			r.System.LoadPerCore())
		b.WriteString(paint(load, ansiDim, opts.Color))
		b.WriteString("\n")
	}

	// Pod identity, when a kubelet placed this process in one.
	//
	// It gets its own line rather than a slot in the title: the title already
	// carries four fields, and "which pod" is a different kind of fact from
	// "which kernel" — one describes the machine, the other the tenant sitting
	// on it. Loading this line into a report collected from a hundred pods is
	// the whole point of reading it: without it, twenty snapshots come back
	// and none of them can be told apart.
	if id := r.K8s.Identity(); id != "" {
		b.WriteString(paint(id, ansiDim, opts.Color))
		b.WriteString("\n")
	}

	// Sampling info
	mode := r.StartedAt.Format("2006-01-02 15:04:05")
	if r.SingleSample {
		mode += " | " + i18n.Get(t, i18n.SingleSample)
	} else {
		mode += fmt.Sprintf(" | %s %s", r.Interval, i18n.Get(t, i18n.TwoSamples))
	}
	b.WriteString(paint(mode, ansiDim, opts.Color))
	b.WriteString("\n")
}

// uptime renders an uptime in the report's language.
//
// diagnose.HumanDuration is Chinese-only and that is fine for its own callers,
// but a header reading "2 cores │ up 12 天 0 小时" is the kind of half-
// translated line that makes a reader distrust every other number on the page.
// The unit suffix is the one part of the duration that is prose rather than
// measurement, so it belongs in i18n with the rest of the chrome.
func uptime(t i18n.Lang, d time.Duration) string {
	if t != i18n.EN {
		return diagnose.HumanDuration(d)
	}
	days := int(d.Hours()) / 24
	hours := int(d.Hours()) % 24
	mins := int(d.Minutes()) % 60
	switch {
	case days > 0:
		return fmt.Sprintf("%dd %dh", days, hours)
	case hours > 0:
		return fmt.Sprintf("%dh %dm", hours, mins)
	default:
		return fmt.Sprintf("%dm", mins)
	}
}

// --- sparklines ---

func writeSparklines(b *strings.Builder, r diagnose.Report, opts RenderOptions) {
	g := opts.Glyphs
	b.WriteString("\n")

	barWidth := 20
	for _, e := range pressureRows(r) {
		bar := sparkbar(e.value, barWidth, g)
		line := fmt.Sprintf("%s %s %5.1f%%", e.label, bar, e.value)
		if e.other > 0 {
			line += fmt.Sprintf("  (%s %.1f%%)", e.otherName, e.other)
		}
		b.WriteString("  " + paint(line, severityANSI(e.sev), opts.Color) + "\n")
	}
}

// pressureRow is one bar: the number the verdict rests on, and the other PSI
// line shown beside it for context.
type pressureRow struct {
	label     string
	value     float64
	other     float64
	otherName string
	sev       diagnose.Severity
}

// pressureRows picks, for each resource, the number the verdict is judged by
// and the severity that number earns — the same number and the same cut-offs
// the findings use.
//
// The bar and the finding were once computed from different PSI lines against
// different cut-offs, which put a yellow bar above a red finding for the same
// disk. Two verdicts on one screen make the reader arbitrate between them,
// and nothing on the screen says which one to trust. One number, one set of
// lines, nothing to arbitrate.
func pressureRows(r diagnose.Report) []pressureRow {
	th := r.Thresholds
	p := r.Pressure
	if c := r.Cgroup; c.V2 && c.Pressure.Supported {
		p = c.Pressure
	}

	var rows []pressureRow

	// CPU PSI has no `full` line: a CPU cannot stall, it can only be
	// oversubscribed, so `some` is the whole story.
	if p.CPU.HasSome {
		rows = append(rows, pressureRow{
			label: "CPU",
			value: p.CPU.Some.Avg10,
			sev:   cut(p.CPU.Some.Avg10, th.CPUSomeWarn, th.CPUSomeWatch),
		})
	}
	// Memory and IO both have a `full` line, and both rules lead with it:
	// full means every task waited, which is the claim worth making.
	if p.Memory.HasFull || p.Memory.HasSome {
		rows = append(rows, fullFirstRow("MEM", p.Memory,
			th.MemFullWarn, th.MemFullCritical, th.MemSomeWatch))
	}
	if p.IO.HasFull || p.IO.HasSome {
		rows = append(rows, fullFirstRow("IO ", p.IO,
			th.IOFullWarn, th.IOFullCritical, th.IOSomeWatch))
	}
	return rows
}

// fullFirstRow judges a resource by its `full` line when the kernel reports
// one, and falls back to `some` when it does not — the same order the rules
// use, so the bar cannot disagree with the finding for the same reason the
// finding would.
func fullFirstRow(label string, psi probe.PSIStall, warn, crit, someWatch float64) pressureRow {
	if !psi.HasFull {
		row := pressureRow{label: label, value: psi.Some.Avg10}
		if row.value >= someWatch {
			row.sev = diagnose.SeverityWatch
		}
		return row
	}
	row := pressureRow{
		label:     label,
		value:     psi.Full.Avg10,
		other:     psi.Some.Avg10,
		otherName: "some",
	}
	switch {
	case row.value >= crit:
		row.sev = diagnose.SeverityCritical
	case row.value >= warn:
		row.sev = diagnose.SeverityWarning
	}
	return row
}

// cut applies a two-line threshold to a value, worst-first.
func cut(v, warn, watch float64) diagnose.Severity {
	switch {
	case v >= warn:
		return diagnose.SeverityWarning
	case v >= watch:
		return diagnose.SeverityWatch
	}
	return diagnose.SeverityOK
}

// sparkbar renders a 0-100 percentage as a row of block characters.
func sparkbar(pct float64, width int, g term.Glyphs) string {
	if pct < 0 {
		pct = 0
	}
	if pct > 100 {
		pct = 100
	}
	full := int(pct) / (100 / width)
	if full > width {
		full = width
	}
	remainder := (int(pct) % (100 / width)) * 8 / (100 / width)
	var b strings.Builder
	for i := 0; i < full; i++ {
		b.WriteString(g.BlockFull)
	}
	if full < width {
		switch {
		case remainder >= 6:
			b.WriteString(g.BlockThree)
		case remainder >= 4:
			b.WriteString(g.BlockHalf)
		case remainder >= 2:
			b.WriteString(g.BlockOne)
		default:
			b.WriteString(g.BlockEmpty)
		}
		for i := full + 1; i < width; i++ {
			b.WriteString(g.BlockEmpty)
		}
	}
	return b.String()
}

// --- thread table ---

func writeThreadTable(b *strings.Builder, r diagnose.Report, opts RenderOptions) {
	if r.Threads.Total == 0 {
		return
	}
	t := opts.Lang
	g := opts.Glyphs

	blocked := r.Threads.BlockedProcesses()
	if len(blocked) == 0 {
		b.WriteString("\n")
		b.WriteString(paint(fmt.Sprintf("%s %d %s%s",
			i18n.Get(t, i18n.Threads),
			r.Threads.Total,
			i18n.Get(t, i18n.ThreadsTotal),
			i18n.Get(t, i18n.ThreadsNone)), ansiDim, opts.Color))
		b.WriteString("\n")
		return
	}

	b.WriteString("\n")
	b.WriteString(paint(fmt.Sprintf("%s %d/%d %s",
		i18n.Get(t, i18n.Threads),
		r.Threads.Blocked, r.Threads.Total,
		i18n.Get(t, i18n.ThreadsBlocked)), ansiBold, opts.Color))
	b.WriteString("\n")

	// Table header
	//
	// The SUBSYS column is the payoff of reading the whole kernel stack rather
	// than just wchan: wchan names one symbol, the subsystem names the thing
	// being waited on — and that is what a reader can act on. It is printed
	// only when the classifier recognised something, so a row without it is
	// visibly "not classified" rather than silently blank.
	subLabel := i18n.Get(t, i18n.ThreadSubsystem)
	if subLabel == "" {
		subLabel = "SUBSYS"
	}
	header := fmt.Sprintf("  %-7s %-7s %-16s %-24s %-5s %s",
		"PID", "TID", "COMM", "WCHAN", "STATE", subLabel)
	b.WriteString(paint(header, ansiDim, opts.Color) + "\n")
	b.WriteString("  " + strings.Repeat(g.HD, 70) + "\n")

	for _, pt := range blocked {
		for _, th := range pt.BlockedOn {
			comm := th.Comm
			if DisplayWidth(comm) > 16 {
				comm = truncateDisplay(comm, 15) + "…"
			}
			wchan := th.WChan
			if wchan == "" {
				wchan = "(running)"
			}
			if DisplayWidth(wchan) > 24 {
				wchan = truncateDisplay(wchan, 23) + "…"
			}
			state := th.State
			if state == "D" {
				state = paint("D", "\x1b[1;31m", opts.Color)
			}

			// The subsystem, in the report's language, with the deciding frame
			// kept alongside it: naming ext4's journal without the frame that
			// proved it would be a claim instead of a reading.
			subsys := ""
			if s := th.Stack.Subsystem; s != "" {
				subsys = subsystemDisplay(s, t)
				if d := th.Stack.DecidedBy; d != "" {
					subsys += " ← " + d
				}
			}

			line := fmt.Sprintf("  %-7d %-7d %-16s %-24s %-5s %s",
				pt.PID, th.TID, padRight(comm, 16), padRight(wchan, 24), padRight(state, 5), subsys)
			b.WriteString(line + "\n")

			if opts.Verbose && len(th.Stack.Frames) > 0 {
				for _, frame := range th.Stack.Frames {
					b.WriteString("        " + paint(frame, ansiDim, opts.Color) + "\n")
				}
			}
		}
		// Open files for this blocked process
		if opts.Verbose {
			if files, ok := r.OpenFiles[pt.PID]; ok && len(files.Paths) > 0 {
				for _, p := range files.Paths {
					b.WriteString("        " + paint("fd: "+p, ansiDim, opts.Color) + "\n")
				}
			}
		}
	}
}

// subsystemDisplay names a kernel-stack subsystem in the report's language.
// The classifier's key is the Chinese name, so the English report has to take
// NameEN from the table or the line ends up half-translated.
func subsystemDisplay(key string, t i18n.Lang) string {
	if t == i18n.EN {
		if sub := diagnose.TranslateSubsystem(key, t); sub.NameEN != "" {
			return sub.NameEN
		}
	}
	return key
}

func truncateDisplay(s string, width int) string {
	if DisplayWidth(s) <= width {
		return s
	}
	var out []rune
	col := 0
	for _, r := range s {
		w := runeWidth(r)
		if col+w > width {
			break
		}
		out = append(out, r)
		col += w
	}
	return string(out)
}

// --- findings ---

func writeFindings(b *strings.Builder, r diagnose.Report, opts RenderOptions) {
	t := opts.Lang
	b.WriteString("\n")
	b.WriteString(paint(fmt.Sprintf("%s (%d)", i18n.Get(t, i18n.Findings), len(r.Findings)), ansiBold, opts.Color))
	b.WriteString("\n")

	if len(r.Findings) == 0 {
		b.WriteString("  " + paint("+  "+i18n.Get(t, i18n.NoBottleneck), severityANSI(diagnose.SeverityOK), opts.Color) + "\n")
		return
	}

	for _, f := range r.Findings {
		b.WriteString("\n")
		label := fmt.Sprintf("%s %s", f.Severity.Marker(), f.Area)
		b.WriteString("  " + paint(label, severityANSI(f.Severity), opts.Color) + "\n")

		for _, line := range Wrap(f.Headline, 66) {
			b.WriteString("      " + line + "\n")
		}

		if len(f.Culprits) > 0 {
			b.WriteString("\n      " + paint(i18n.Get(t, i18n.Culprits), ansiDim, opts.Color) + "\n")
			for _, c := range f.Culprits {
				name := c.Name
				if c.User != "" {
					name = padRight(name, 18) + " [" + c.User + "]"
				}
				b.WriteString(fmt.Sprintf("        PID %-7d %s\n", c.PID, name))
				for _, line := range noteLines(c.Note) {
					b.WriteString(fmt.Sprintf("        %-12s %s\n", "", line))
				}
				if opts.Verbose && c.Cmdline != "" && c.Cmdline != c.Name {
					b.WriteString(fmt.Sprintf("        %-12s %s\n", "", paint(c.Cmdline, ansiDim, opts.Color)))
				}
			}
		}

		if len(f.Evidence) > 0 {
			b.WriteString("\n      " + paint(i18n.Get(t, i18n.Evidence), ansiDim, opts.Color) + "\n")
			for _, e := range f.Evidence {
				for i, line := range Wrap(e.Detail, 60) {
					src := ""
					if i == 0 {
						src = e.Source
					}
					b.WriteString(fmt.Sprintf("        %-18s %s\n", src, line))
				}
			}
		}

		if f.Advice != "" {
			b.WriteString("\n      " + paint(i18n.Get(t, i18n.Advice), ansiDim, opts.Color) + "\n")
			for _, line := range Wrap(f.Advice, 66) {
				b.WriteString("        " + line + "\n")
			}
		}
	}
}

// --- overview ---

func writeOverview(b *strings.Builder, r diagnose.Report, opts RenderOptions) {
	t := opts.Lang
	b.WriteString("\n")
	b.WriteString(paint(i18n.Get(t, i18n.Overview), ansiBold, opts.Color))
	b.WriteString("\n")

	for _, line := range overviewLinesLang(r, t) {
		b.WriteString("  " + line + "\n")
	}
}

func writeUnmeasured(b *strings.Builder, r diagnose.Report, opts RenderOptions) {
	t := opts.Lang
	b.WriteString("\n")
	b.WriteString(paint(i18n.Get(t, i18n.Unmeasured), ansiBold, opts.Color))
	b.WriteString("\n")
	for _, e := range r.Errors {
		for _, line := range Wrap("· "+e, 66) {
			b.WriteString("  " + line + "\n")
		}
	}
}

// overviewLinesLang is the language-aware version of overviewLines. It keeps
// the existing Chinese labels when lang is ZH and uses English keys otherwise.
func overviewLinesLang(r diagnose.Report, t i18n.Lang) []string {
	// For ZH we delegate to the existing overviewLines (which has hardcoded
	// Chinese labels). For EN we build English labels.
	if t == i18n.ZH {
		return overviewLines(r)
	}

	var out []string
	if r.CPUMeasured {
		// Same reasoning as the Chinese line: "busy" excludes iowait and
		// steal, and the separator says so rather than leaving the reader
		// to add five numbers into something over 100.
		out = append(out, fmt.Sprintf(
			"CPU      busy %.1f%% (user %.1f%% sys %.1f%%) | not counted as busy: iowait %.1f%% steal %.1f%%",
			r.CPUUsage.Busy, r.CPUUsage.User, r.CPUUsage.System,
			r.CPUUsage.IOWait, r.CPUUsage.Steal))
	}
	if len(r.Cores) > 0 {
		out = append(out, "cores    "+coreLineEN(r.Cores))
	}
	if r.Memory.TotalKB > 0 {
		line := fmt.Sprintf("memory   %.1f%% used (%s / %s)",
			r.Memory.UsedPercent(),
			diagnose.HumanBytes(r.Memory.UsedKB()*1024),
			diagnose.HumanBytes(r.Memory.TotalKB*1024))
		if r.Memory.SwapTotalKB > 0 {
			line += fmt.Sprintf("   swap %.1f%%", r.Memory.SwapPercent())
		}
		out = append(out, line)
	}
	if r.System.Cores > 0 {
		out = append(out, fmt.Sprintf("load     %.2f on %d cores (%.2f/core)",
			r.System.Load[0], r.System.Cores, r.System.LoadPerCore()))
	}
	if r.Threads.Total > 0 {
		line := fmt.Sprintf("threads  %d total", r.Threads.Total)
		if r.Threads.Blocked > 0 {
			line += fmt.Sprintf(", %d in D state (uninterruptible)", r.Threads.Blocked)
		} else {
			line += ", none blocked"
		}
		out = append(out, line)
	}
	if c := r.Cgroup; c.V2 && (c.CPUQuotaCores > 0 || c.MemoryMaxKB > 0) {
		var parts []string
		if c.CPUQuotaCores > 0 {
			parts = append(parts, fmt.Sprintf("CPU %.1f cores (host %d)", c.CPUQuotaCores, c.HostCores))
		}
		if c.MemoryMaxKB > 0 {
			parts = append(parts, fmt.Sprintf("mem %s, %.1f%% used",
				diagnose.HumanBytes(c.MemoryMaxKB*1024), c.MemoryUsedPercent()))
		}
		out = append(out, "quota    "+strings.Join(parts, "   "))
	}
	if line := throttleLine(r, t); line != "" {
		out = append(out, line)
	}
	if r.Net.Measured && r.Net.OutSegs > 0 {
		line := fmt.Sprintf("network  %s segs sent, %.1f%% retrans",
			diagnose.HumanCountIn(r.Net.OutSegs, t), r.Net.RetransPct)
		if r.Net.ListenDrops > 0 {
			line += fmt.Sprintf(", %d listen-queue drops", r.Net.ListenDrops)
		}
		out = append(out, line)
	}
	out = append(out, pressureLinesEN(r)...)
	return out
}

func coreLineEN(cores []probe.CoreLoad) string {
	const small = 8
	if len(cores) <= small {
		parts := make([]string, 0, len(cores))
		for _, c := range cores {
			parts = append(parts, fmt.Sprintf("CPU%d %.0f%%", c.ID, c.Busy))
		}
		return strings.Join(parts, "  ")
	}
	hot := 0
	var parts []string
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
		return fmt.Sprintf("%d cores, none above 80%%", len(cores))
	}
	s := strings.Join(parts, "  ")
	if hot > len(parts) {
		s += fmt.Sprintf(", %d more above 80%%", hot-len(parts))
	}
	return s
}

// throttleLine summarises CFS throttling for the overview, or "" when no
// quota is being enforced.
//
// It is printed unconditionally once the counter is non-zero, including at
// 1%: throttling is the one symptom in this report that every other line
// actively hides, so a reader needs to know it is happening at all before
// they need to know how bad it is.
func throttleLine(r diagnose.Report, t i18n.Lang) string {
	c := r.Cgroup
	if !c.V2 || !c.Throttle.Known || c.Throttle.Periods == 0 || c.Throttle.Throttled == 0 {
		return ""
	}
	// The same numbers the finding judged by. The bar-and-verdict split that
	// once put a yellow PSI bar above a red finding would be just as wrong
	// here: 31% in the overview next to 80% in the verdict is two answers to
	// one question.
	periods, throttled, p := c.Throttle.Periods, c.Throttle.Throttled, c.Throttle.ThrottledPct()
	if r.Throttle.Measured {
		periods, throttled, p = r.Throttle.Periods, r.Throttle.Throttled, r.Throttle.Pct()
	}
	total := c.Throttle.ThrottledSeconds()
	if t == i18n.EN {
		when := "cumulative"
		if r.Throttle.Measured {
			when = "this window"
		}
		return fmt.Sprintf("throttle %d of %d periods stopped (%s, %.1f%%), %.1fs total",
			throttled, periods, when, p, total)
	}
	when := "累计"
	if r.Throttle.Measured {
		when = "本窗口"
	}
	return fmt.Sprintf("限流     %s %d 个周期有 %d 个被停（%.1f%%），累计被停 %.1f 秒",
		when, periods, throttled, p, total)
}

func pressureLinesEN(r diagnose.Report) []string {
	parts := func(p probe.Pressure) []string {
		var out []string
		if p.CPU.HasSome {
			out = append(out, fmt.Sprintf("cpu some %.2f%%", p.CPU.Some.Avg10))
		}
		if p.IO.HasFull {
			out = append(out, fmt.Sprintf("io full %.2f%%", p.IO.Full.Avg10))
		}
		if p.Memory.HasFull {
			out = append(out, fmt.Sprintf("mem full %.2f%%", p.Memory.Full.Avg10))
		}
		return out
	}
	c := r.Cgroup
	if !(c.V2 && c.Pressure.Supported) {
		if p := parts(r.Pressure); len(p) > 0 {
			// A cgroup with no PSI files of its own had to borrow the
			// node's. Saying so here as well as in the finding keeps the
			// summary from contradicting the verdict below it.
			suffix := ""
			if c.V2 {
				suffix = " (node-wide, other tenants included)"
			}
			return []string{"pressure " + strings.Join(p, "   ") + suffix}
		}
		return nil
	}
	var out []string
	if p := parts(c.Pressure); len(p) > 0 {
		out = append(out, "pressure "+strings.Join(p, "   ")+" (this cgroup)")
	}
	if p := parts(r.Pressure); len(p) > 0 {
		out = append(out, "pressure "+strings.Join(p, "   ")+" (system-wide)")
	}
	return out
}
