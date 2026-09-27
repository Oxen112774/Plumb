package report

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/Oxen112774/plumb/internal/diagnose"
	"github.com/Oxen112774/plumb/internal/probe"
)

// Each ideograph occupies two terminal cells, so a rune count would let a
// Chinese headline overflow its column.
func TestWrapCountsWideRunesAsTwoColumns(t *testing.T) {
	got := Wrap("中文中文中文", 4)
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
	got := Wrap("hello world foo", 12)
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
	got := Wrap("abcdefghijklmnop", 6)
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
	got := Wrap("中文中文。", 8)
	if len(got) != 1 {
		t.Fatalf("wrap = %q, want the full stop kept with the text it closes", got)
	}
	if got[0] != "中文中文。" {
		t.Fatalf("wrap = %q, want %q", got, "中文中文。")
	}
}

// An opening bracket may not end a line either: whatever it opens belongs on
// the same line as it.
func TestWrapDoesNotOrphanOpeningBracket(t *testing.T) {
	got := Wrap("中文中文中文中文中（后面的内容很长很长很长很长很长很长", 20)
	for i, line := range got[:len(got)-1] {
		if strings.HasSuffix(line, "（") {
			t.Errorf("line %d = %q ends with an opening bracket", i, line)
		}
	}
}

// Chinese text may break between any two ideographs. Falling back to the column
// limit keeps a Chinese paragraph from leaving a third of every other line
// empty, which is what happens when only separators count as break points.
func TestWrapFillsChineseLinesToTheColumnBudget(t *testing.T) {
	got := Wrap("这是一个很长的中文句子用来测试换行行为是否填满整行", 20)
	if len(got) < 3 {
		t.Fatalf("wrap = %q, want several lines", got)
	}
	for i, line := range got[:len(got)-1] {
		if w := displayWidth([]rune(line)); w != 20 {
			t.Errorf("line %d = %q is %d columns wide, want a full 20", i, line, w)
		}
	}
}

// The flip side: a separator so early in the budget that honouring it would
// leave the line mostly empty is not worth taking.
func TestWrapDoesNotStopAtAnEarlySeparator(t *testing.T) {
	got := Wrap("短、这是一个很长的中文句子用来测试换行是否填满整行整行", 20)
	if len(got) < 2 {
		t.Fatalf("wrap = %q, want several lines", got)
	}
	if w := displayWidth([]rune(got[0])); w < 10 {
		t.Errorf("first line = %q is only %d columns wide; an early separator "+
			"should not cut the line short", got[0], w)
	}
}

// A separator's position has to be measured in columns. In a Chinese line the
// rune index and the column position differ by a factor of two, so comparing a
// rune index against a column budget makes a separator at the very start of the
// line look like it is halfway across it.
func TestWrapMeasuresSeparatorsInColumns(t *testing.T) {
	// The only separator is at rune 1 — column 2 — nowhere near the halfway
	// point of a 20-column line, so it must not be honoured.
	got := Wrap("短、这是一个很长很长的中文句子需要换行很多次才够", 20)
	if len(got) < 2 {
		t.Fatalf("wrap = %q, want several lines", got)
	}
	if w := displayWidth([]rune(got[0])); w < 16 {
		t.Errorf("first line = %q is %d columns wide, want close to the full 20",
			got[0], w)
	}
}

// A space stranded at either edge of a line is a visible defect, and it is what
// a naive break does when the budget runs out next to one.
func TestWrapNeverStrandsASpaceAtALineEdge(t *testing.T) {
	got := Wrap("说明瓶颈是一个线程干完全部活不是 CPU 不够导致的响应变慢", 20)
	for _, line := range got {
		if strings.HasPrefix(line, " ") || strings.HasSuffix(line, " ") {
			t.Errorf("line %q has a space at its edge", line)
		}
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

// The JSON output is the documented interface for anything consuming -json, so
// its key style has to be uniform. Go's default is to emit the Go field name,
// which put "Findings" next to "open_files" in the same object.
func TestJSONKeysAreUniformlySnakeCase(t *testing.T) {
	rep := diagnose.Report{
		StartedAt:    time.Now(),
		Interval:     time.Second,
		CPUUsage:     diagnose.CPUUsage{Busy: 42.5},
		Cores:        []probe.CoreLoad{{ID: 0, Busy: 99}},
		Threads:      probe.ThreadScan{Total: 1},
		OpenFiles:    map[int]probe.OpenFileSet{1: {Paths: []string{"/x"}}},
		SingleSample: true,
		Findings:     []diagnose.Finding{{Area: "磁盘 IO"}},
	}

	var b strings.Builder
	if err := JSON(&b, rep); err != nil {
		t.Fatalf("JSON: %v", err)
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal([]byte(b.String()), &raw); err != nil {
		t.Fatalf("output is not valid JSON: %v", err)
	}

	for _, want := range []string{
		"started_at", "interval_ns", "cpu_usage", "cores",
		"threads", "open_files", "single_sample", "findings",
	} {
		if _, ok := raw[want]; !ok {
			t.Errorf("JSON is missing key %q", want)
		}
	}
	for k := range raw {
		if first := k[:1]; first == strings.ToUpper(first) {
			t.Errorf("key %q is not snake_case", k)
		}
	}
}
