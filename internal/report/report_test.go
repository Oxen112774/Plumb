package report

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/Oxen112774/plumb/internal/diagnose"
)

// Each ideograph occupies two terminal cells, so a rune count would let a
// Chinese headline overflow its column.
func TestWrapCountsWideRunesAsTwoColumns(t *testing.T) {
	got := wrap("中文中文中文", 4)
	want := []string{"中文", "中文", "中文"}
	if len(got) != len(want) {
		t.Fatalf("wrap = %q, want %q", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("wrap = %q, want %q", got, want)
		}
	}
}

// Breaking wherever the limit lands turns English advice into noise
// ("...fs" / "ync 过频"), so a break at the last space wins.
func TestWrapBreaksAtSpaceRatherThanSplittingWords(t *testing.T) {
	got := wrap("hello world foo", 12)
	want := []string{"hello world", "foo"}
	if len(got) != len(want) {
		t.Fatalf("wrap = %q, want %q", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("wrap = %q, want %q", got, want)
		}
	}
}

// A single word longer than the column budget still has to be emitted, or the
// report would lose text.
func TestWrapEmitsOverlongWord(t *testing.T) {
	got := wrap("abcdefghijklmnop", 6)
	if len(got) < 2 {
		t.Fatalf("wrap = %q, want the word split across lines", got)
	}
	if strings.Join(got, "") != "abcdefghijklmnop" {
		t.Fatalf("wrap lost characters: %q", got)
	}
}

// A full stop at the start of a line is a visible defect in Chinese
// typography; overflowing by one glyph is the lesser evil.
func TestWrapDoesNotOrphanClosingPunctuation(t *testing.T) {
	got := wrap("中文中文。", 8)
	if len(got) != 1 {
		t.Fatalf("wrap = %q, want the full stop kept with the text it closes", got)
	}
	if got[0] != "中文中文。" {
		t.Fatalf("wrap = %q, want %q", got, "中文中文。")
	}
}

func TestTextRendersEvidenceCulpritsAndAdvice(t *testing.T) {
	rep := diagnose.Report{
		StartedAt: time.Now(),
		Findings: []diagnose.Finding{{
			Area:     "磁盘 IO",
			Severity: diagnose.SeverityCritical,
			Headline: "磁盘让整机停摆",
			Evidence: []diagnose.Evidence{
				{Source: "/proc/pressure/io", Detail: "full avg10=17.40"},
			},
			Culprits: []diagnose.Culprit{
				{PID: 1234, Name: "mysqld", Note: "读 40.0 MiB/s"},
			},
			Advice: "先确认是谁在写。",
		}},
	}

	var b strings.Builder
	if err := Text(&b, rep, false); err != nil {
		t.Fatalf("Text: %v", err)
	}
	out := b.String()

	for _, want := range []string{
		"磁盘 IO", "磁盘让整机停摆", "/proc/pressure/io", "full avg10=17.40",
		"mysqld", "1234", "先确认是谁在写",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("rendered report is missing %q\n---\n%s", want, out)
		}
	}
	if strings.Contains(out, "\x1b[") {
		t.Error("ANSI escapes present with color=false")
	}
}

func TestTextReportsAMeasuredAllClear(t *testing.T) {
	var b strings.Builder
	if err := Text(&b, diagnose.Report{StartedAt: time.Now()}, false); err != nil {
		t.Fatalf("Text: %v", err)
	}
	if !strings.Contains(b.String(), "未发现明显瓶颈") {
		t.Errorf("a report with no findings should say so:\n%s", b.String())
	}
}

func TestTextListsUnmeasuredProbes(t *testing.T) {
	rep := diagnose.Report{
		StartedAt: time.Now(),
		Errors:    []string{"disk space for /: not supported on this platform"},
	}
	var b strings.Builder
	if err := Text(&b, rep, false); err != nil {
		t.Fatalf("Text: %v", err)
	}
	if !strings.Contains(b.String(), "not supported on this platform") {
		t.Errorf("unmeasured probes must be disclosed:\n%s", b.String())
	}
}

func TestJSONRoundTrips(t *testing.T) {
	rep := diagnose.Report{
		StartedAt: time.Now(),
		Findings: []diagnose.Finding{{
			Area:     "磁盘 IO",
			Severity: diagnose.SeverityCritical,
			Headline: "磁盘让整机停摆",
		}},
	}
	var b strings.Builder
	if err := JSON(&b, rep); err != nil {
		t.Fatalf("JSON: %v", err)
	}
	var back diagnose.Report
	if err := json.Unmarshal([]byte(b.String()), &back); err != nil {
		t.Fatalf("output is not valid JSON: %v\n%s", err, b.String())
	}
	if len(back.Findings) != 1 || back.Findings[0].Area != "磁盘 IO" {
		t.Fatalf("round trip lost data: %+v", back)
	}
}
