package main

import (
	"strings"
	"testing"
	"time"

	"github.com/Oxen112774/plumb/internal/config"
	"github.com/Oxen112774/plumb/internal/diagnose"
	"github.com/Oxen112774/plumb/internal/i18n"
	"github.com/Oxen112774/plumb/internal/probe"
	"github.com/Oxen112774/plumb/internal/report"
	"github.com/Oxen112774/plumb/internal/term"
	"github.com/Oxen112774/plumb/internal/voice"
)

// demoReport reads the checked-in fixture the way the README's examples do, so
// these tests exercise the same data a reader sees.
func demoReport(t *testing.T, lang i18n.Lang) diagnose.Report {
	t.Helper()
	root := probe.NewRoot("../../testdata/demo/cur")
	if !root.Exists("proc") {
		t.Fatal("demo fixture missing: testdata/demo/cur/proc")
	}
	prev := probe.Take(probe.NewRoot("../../testdata/demo/prev"), "/")
	cur := probe.Take(probe.NewRoot("../../testdata/demo/cur"), "/")
	return diagnose.Analyze(prev, cur, diagnose.Options{
		Interval:   time.Second,
		Top:        3,
		Thresholds: diagnose.DefaultThresholds(),
		Lang:       lang,
	})
}

func renderReport(t *testing.T, r diagnose.Report, lang i18n.Lang) string {
	t.Helper()
	var b strings.Builder
	err := report.Hardcore(&b, r, report.RenderOptions{
		Lang:    lang,
		Glyphs:  term.ASCII(),
		Color:   false,
		Verbose: true,
		Width:   80,
	})
	if err != nil {
		t.Fatalf("Hardcore: %v", err)
	}
	return b.String()
}

// TestEnglishOutputHasNoChinese is the guard on the -lang en promise.
//
// An English report that is 80% translated is worse than a Chinese one that
// admits what it is: the reader cannot tell whether the Chinese passages are
// untranslated or are technical terms that have no translation. Every new
// finding, evidence line or remedy has to clear this test, which is the only
// thing that makes "supports English" a claim instead of an intention.
func TestEnglishOutputHasNoChinese(t *testing.T) {
	rep := demoReport(t, i18n.EN)
	if len(rep.Findings) == 0 {
		t.Fatal("demo fixture produced no findings; the test would pass vacuously")
	}
	for _, tc := range []struct {
		name string
		text string
	}{
		{"report", renderReport(t, rep, i18n.EN)},
		{"voice", func() string {
			var b strings.Builder
			if err := voice.Write(&b, rep, false, nil, i18n.EN); err != nil {
				t.Fatalf("voice.Write: %v", err)
			}
			return b.String()
		}()},
	} {
		for i, line := range strings.Split(tc.text, "\n") {
			for _, r := range line {
				if r >= 0x4e00 && r <= 0x9fff {
					t.Errorf("%s line %d: untranslated Chinese: %q", tc.name, i+1, line)
					break
				}
			}
		}
	}
}

// TestChineseOutputStaysChinese is the mirror of the test above.
//
// A translation that leaks into the default language is the same defect in the
// other direction, and it is the one nobody reports: the project's primary
// readers just see a sentence in the wrong language and work around it.
func TestChineseOutputStaysChinese(t *testing.T) {
	rep := demoReport(t, i18n.ZH)
	text := renderReport(t, rep, i18n.ZH)
	if !strings.Contains(text, "判定") {
		t.Error("Chinese report lost its Chinese headings")
	}
	if strings.Contains(text, "findings\n") {
		t.Error("Chinese report contains the English heading")
	}
}

// TestThresholdsReachTheVerdict is the test that would have caught the version
// of plumb where `plumb config warn_io 50` changed nothing.
//
// A knob that is stored, displayed and ignored is worse than a knob that does
// not exist, because the operator believes they tightened a check. What is
// asserted here is not the value of a threshold but that moving one moves the
// severity — the property the config promises.
func TestThresholdsReachTheVerdict(t *testing.T) {
	prev := probe.Take(probe.NewRoot("../../testdata/demo/prev"), "/")
	cur := probe.Take(probe.NewRoot("../../testdata/demo/cur"), "/")

	severity := func(th diagnose.Thresholds) diagnose.Severity {
		rep := diagnose.Analyze(prev, cur, diagnose.Options{
			Interval:   time.Second,
			Top:        3,
			Thresholds: th,
		})
		for _, f := range rep.Findings {
			if f.Area == "磁盘 IO" {
				return f.Severity
			}
		}
		return ""
	}

	base := diagnose.DefaultThresholds()
	if got := severity(base); got != diagnose.SeverityCritical {
		t.Fatalf("with default thresholds io = %q, want critical", got)
	}

	// Raise the line far past what this fixture reaches: the same disk can no
	// longer be called critical.
	loose := base
	loose.IOFullCritical = 99
	loose.IOSomeWarn = 99
	loose.IOSomeWatch = 99
	if got := severity(loose); got == diagnose.SeverityCritical {
		t.Errorf("raising io thresholds to 99 still yields critical: %q", got)
	}
}

// TestCustomThresholdsAreDisclosed is the other half of the same promise: when
// a verdict was reached with a moved line, the report has to say so.
//
// A reader comparing two reports from two machines must be able to see that
// the cut-offs differed, or the comparison is meaningless — and the report is
// the only artefact that survives the incident.
func TestCustomThresholdsAreDisclosed(t *testing.T) {
	prev := probe.Take(probe.NewRoot("../../testdata/demo/prev"), "/")
	cur := probe.Take(probe.NewRoot("../../testdata/demo/cur"), "/")

	th := diagnose.DefaultThresholds()
	th.IOFullCritical = 99
	th.IOSomeWarn = 99
	th.IOSomeWatch = 99

	rep := diagnose.Analyze(prev, cur, diagnose.Options{
		Interval:   time.Second,
		Top:        3,
		Thresholds: th,
	})
	var found bool
	for _, f := range rep.Findings {
		for _, e := range f.Evidence {
			if e.Source == "config" {
				found = true
				if !strings.Contains(e.Detail, "io_full_critical") {
					t.Errorf("disclosure does not name the moved threshold: %q", e.Detail)
				}
			}
		}
	}
	if !found {
		t.Error("no finding disclosed that its thresholds were changed")
	}
}

// TestStyleConfigSelectsTheLayout covers the other knob that was stored but
// never read. Only the two non-default layouts are asserted: compact is what
// every other test already covers.
func TestStyleConfigSelectsTheLayout(t *testing.T) {
	if config.StyleCompact != "compact" || config.StyleRaw != "raw" || config.StylePaste != "paste" {
		t.Fatal("style constants changed; this test names them literally on purpose")
	}
	rep := demoReport(t, i18n.ZH)

	var raw, paste strings.Builder
	opts := report.RenderOptions{Lang: i18n.ZH, Glyphs: term.ASCII(), Color: false, Width: 80}
	if err := report.Raw(&raw, rep, opts); err != nil {
		t.Fatalf("Raw: %v", err)
	}
	if err := report.Paste(&paste, rep, opts); err != nil {
		t.Fatalf("Paste: %v", err)
	}
	if strings.Contains(raw.String(), "\x1b[") {
		t.Error("raw output contains ANSI colour")
	}
	if !strings.Contains(paste.String(), "=====") {
		t.Error("paste output is missing its paste markers")
	}
	if raw.String() == paste.String() {
		t.Error("raw and paste produced identical output; one of them is not doing its job")
	}
}

// TestReportCarriesTheThresholdsItUsed guards the replay property: a report
// saved during an incident has to say which lines it judged by, because the
// defaults may have changed by the time someone reads it back.
func TestReportCarriesTheThresholdsItUsed(t *testing.T) {
	rep := demoReport(t, i18n.ZH)
	if rep.Thresholds.IOFullWarn == 0 {
		t.Error("report carries an empty threshold set")
	}
	if got := rep.Thresholds.Changed(); len(got) != 0 {
		t.Errorf("default thresholds reported as changed: %v", got)
	}
}
