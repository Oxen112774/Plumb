package report

import (
	"strings"
	"testing"
	"time"

	"github.com/Oxen112774/plumb/internal/diagnose"
	"github.com/Oxen112774/plumb/internal/i18n"
	"github.com/Oxen112774/plumb/internal/probe"
	"github.com/Oxen112774/plumb/internal/term"
)

// psi builds one resource's PSI reading for a test.
func psi(full, some float64, hasFull bool) probe.PSIStall {
	return probe.PSIStall{
		Full:    probe.PSIValues{Avg10: full},
		Some:    probe.PSIValues{Avg10: some},
		HasFull: hasFull,
		HasSome: true,
	}
}

func TestPressureRowJudgesByFullNotSome(t *testing.T) {
	// The defect this locks: the bar was drawn from `some` against 50/20/5
	// while the finding judged `full` against 2/10, so one disk was yellow
	// above and red below on the same screen.
	rep := diagnose.Report{
		Thresholds: diagnose.DefaultThresholds(),
		Pressure: probe.Pressure{
			IO: psi(24.5, 44.8, true),
		},
	}
	rows := pressureRows(rep)
	if len(rows) != 1 {
		t.Fatalf("got %d rows, want 1", len(rows))
	}
	if rows[0].value != 24.5 {
		t.Errorf("row judges %.1f, want the full line 24.5", rows[0].value)
	}
	if rows[0].sev != diagnose.SeverityCritical {
		t.Errorf("row severity = %q, want critical (24.5 >= 10)", rows[0].sev)
	}
	if rows[0].other != 44.8 {
		t.Errorf("some line = %.1f, want 44.8 shown as context", rows[0].other)
	}
}

// TestPressureRowFollowsMovedThresholds is the other half: the bar must read
// its cut-offs from the report, not from the constants in the rules. A bar
// that keeps the built-in lines while the finding honours the config is the
// same disagreement in a new place.
func TestPressureRowFollowsMovedThresholds(t *testing.T) {
	th := diagnose.DefaultThresholds()
	th.IOFullCritical = 99
	th.IOFullWarn = 99
	rep := diagnose.Report{
		Thresholds: th,
		Pressure:   probe.Pressure{IO: psi(24.5, 44.8, true)},
	}
	if got := pressureRows(rep)[0].sev; got == diagnose.SeverityCritical {
		t.Errorf("with io_full_critical=99 the row is still %q", got)
	}
}

func TestPressureRowFallsBackToSomeWithoutFull(t *testing.T) {
	rep := diagnose.Report{
		Thresholds: diagnose.DefaultThresholds(),
		Pressure:   probe.Pressure{IO: psi(0, 44.8, false)},
	}
	rows := pressureRows(rep)
	if len(rows) != 1 {
		t.Fatalf("got %d rows, want 1", len(rows))
	}
	if rows[0].value != 44.8 {
		t.Errorf("row judges %.1f, want the some line when there is no full", rows[0].value)
	}
	if rows[0].sev != diagnose.SeverityWatch {
		t.Errorf("severity = %q, want watch (44.8 >= io_some_watch)", rows[0].sev)
	}
}

func TestSparkbarBounds(t *testing.T) {
	g := term.Unicode()
	full := sparkbar(100, 10, g)
	if strings.ContainsRune(full, '░') {
		t.Errorf("100%% bar is not full: %q", full)
	}
	empty := sparkbar(0, 10, g)
	if strings.ContainsRune(empty, '█') {
		t.Errorf("0%% bar is not empty: %q", empty)
	}
	// Out-of-range input is clamped rather than drawn off the end: a
	// counter that went backwards must not produce a bar with no blocks or
	// a bar longer than the column.
	over := sparkbar(250, 10, g)
	under := sparkbar(-5, 10, g)
	if over != full {
		t.Errorf("250%% != 100%%: %q vs %q", over, full)
	}
	if under != empty {
		t.Errorf("-5%% != 0%%: %q vs %q", under, empty)
	}
}

// TestPasteAndRawArePlainText guards the two modes meant for a log line or a
// chat box. ANSI escapes survive copy-paste as literal garbage, and that is
// the one place nobody can clean it up.
func TestPasteAndRawArePlainText(t *testing.T) {
	rep := diagnose.Report{
		StartedAt:  time.Now(),
		Thresholds: diagnose.DefaultThresholds(),
		Findings: []diagnose.Finding{{
			Area:     "磁盘 IO",
			Severity: diagnose.SeverityCritical,
			Headline: "磁盘让整机停摆",
			Advice:   "先确认是谁在写",
		}},
	}
	opts := RenderOptions{Lang: i18n.ZH, Glyphs: term.ASCII(), Color: true, Width: 80}
	for _, tc := range []struct {
		name string
		fn   func(sb *strings.Builder) error
	}{
		{"raw", func(sb *strings.Builder) error { return Raw(sb, rep, opts) }},
		{"paste", func(sb *strings.Builder) error { return Paste(sb, rep, opts) }},
	} {
		var sb strings.Builder
		if err := tc.fn(&sb); err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		if strings.Contains(sb.String(), "\x1b[") {
			t.Errorf("%s output contains ANSI escapes despite Color=true", tc.name)
		}
		if !strings.Contains(sb.String(), "磁盘让整机停摆") {
			t.Errorf("%s output is missing the headline", tc.name)
		}
	}
}
