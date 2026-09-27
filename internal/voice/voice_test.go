package voice

import (
	"strings"
	"testing"
	"time"

	"github.com/Oxen112774/plumb/internal/diagnose"
	"github.com/Oxen112774/plumb/internal/probe"
)

func sampleReport() diagnose.Report {
	return diagnose.Report{
		StartedAt: time.Date(2026, 9, 27, 11, 41, 34, 0, time.UTC),
		Interval:  time.Second,
		System:    probe.System{Hostname: "prod-db-01", Cores: 4, Kernel: "5.15.0"},
		Findings: []diagnose.Finding{
			{
				Area:     "磁盘 IO",
				Severity: diagnose.SeverityCritical,
				Headline: "磁盘让整机停摆 —— 过去 10 秒有 17.4% 的时间所有任务都在等它",
				Culprits: []diagnose.Culprit{
					{PID: 1234, Name: "mysqld", Note: "读 40.1 MiB/s，写 976.6 KiB/s"},
				},
			},
			{Area: "内存", Severity: diagnose.SeverityWarning, Headline: "内存不足"},
			{Area: "脏页", Severity: diagnose.SeverityWatch, Headline: "脏页堆积"},
		},
	}
}

func TestComposeLeadsWithTheWorstFinding(t *testing.T) {
	m := Compose(sampleReport())

	if !strings.Contains(m.Situation, "磁盘") {
		t.Errorf("situation = %q, want the disk script", m.Situation)
	}
	if m.First == "" {
		t.Error("no first step for the worst finding")
	}
	if got, want := m.Facts[0], sampleReport().Findings[0].Headline; got != want {
		t.Errorf("first fact = %q, want the headline verbatim (%q)", got, want)
	}
	if !strings.Contains(m.Urgency, "现在就在影响线上") {
		t.Errorf("urgency = %q, want the critical wording", m.Urgency)
	}
	if !strings.Contains(m.Others, "内存") || strings.Contains(m.Others, "磁盘 IO") {
		t.Errorf("others = %q, want the remaining areas and not the leading one", m.Others)
	}
}

func TestComposeHealthysaysNothingAboutUrgency(t *testing.T) {
	r := sampleReport()
	r.Findings = nil

	m := Compose(r)
	if m.Urgency != "" {
		t.Errorf("urgency = %q, want none on a healthy machine", m.Urgency)
	}
	if m.First != "" {
		t.Errorf("first = %q, want no instruction on a healthy machine", m.First)
	}
	if !strings.Contains(m.Situation, "没什么") {
		t.Errorf("situation = %q, want a plain all-clear", m.Situation)
	}
}

// The single most important contract in this package: whatever the machine is
// doing, the message never claims an action the program did not take.
func TestMessageNeverClaimsAnAction(t *testing.T) {
	forbidden := []string{
		"我们正在", "已经处理", "已经修复", "已介入", "不用担心",
		"无需担心", "已为你", "帮你重启", "已自动",
	}

	var out strings.Builder
	if err := Write(&out, sampleReport(), false, nil); err != nil {
		t.Fatalf("Write: %v", err)
	}
	text := out.String()

	if !strings.Contains(text, "我没有动你的系统") {
		t.Error("the message does not say what plumb did, which is nothing")
	}
	for _, bad := range forbidden {
		if strings.Contains(text, bad) {
			t.Errorf("the message claims %q, which plumb never does", bad)
		}
	}
}

func TestSplitNoteSeparatesHeldFiles(t *testing.T) {
	note := "线程 1240：等 ext4 日志提交  [jbd2_log_wait_commit]\n" +
		"线程 1241：等磁盘 IO 完成  [io_schedule]\n" +
		"持有文件：\n  /var/lib/mysql/ib_logfile0\n  /var/lib/mysql/ibdata1\n"

	detail, held := splitNote(note)

	if !strings.HasPrefix(detail, "线程 1240：") {
		t.Errorf("detail = %q, want the first thread", detail)
	}
	if !strings.Contains(detail, "另外 1 个也在等") {
		t.Errorf("detail = %q, want the remaining thread counted", detail)
	}
	if strings.Contains(detail, "持有文件") {
		t.Errorf("detail = %q, the file header belongs to the other half", detail)
	}
	// Two spaces set the symbol off in a fixed-width column; inline it is a typo.
	if strings.Contains(detail, "  [") {
		t.Errorf("detail = %q, want the double space collapsed", detail)
	}
	if want := "/var/lib/mysql/ib_logfile0 等 2 个文件"; held != want {
		t.Errorf("held = %q, want %q", held, want)
	}
}

func TestSplitNoteWithoutFiles(t *testing.T) {
	detail, held := splitNote("读 40.1 MiB/s，写 976.6 KiB/s")
	if detail != "读 40.1 MiB/s，写 976.6 KiB/s" {
		t.Errorf("detail = %q", detail)
	}
	if held != "" {
		t.Errorf("held = %q, want empty", held)
	}
}

func TestJoinAllSuppliesTheMissingFullStop(t *testing.T) {
	// A headline is a fragment; run straight into the next fact it produced
	// "…都在等它最直接的是 mysqld".
	got := joinAll([]string{"都在等它", "最直接的是 mysqld。"})
	if want := "都在等它。最直接的是 mysqld。"; got != want {
		t.Errorf("joinAll = %q, want %q", got, want)
	}
	if got := joinAll([]string{"已经结束。", "下一句"}); got != "已经结束。下一句" {
		t.Errorf("joinAll added a stop where there already was one: %q", got)
	}
}

func TestStateOfIgnoresInfoLevel(t *testing.T) {
	r := sampleReport()
	r.Findings = append(r.Findings, diagnose.Finding{
		Area: "被堵住的线程", Severity: diagnose.SeverityInfo, Headline: "两个线程在等",
	})

	s := StateOf(r)
	if _, ok := s["被堵住的线程"]; ok {
		t.Error("info-level findings flicker every round and must not be tracked")
	}
	if len(s) != 3 {
		t.Errorf("state = %v, want the three findings at watch or worse", s)
	}
}

func TestChangeReportsAppearWorsenAndClear(t *testing.T) {
	prev := State{"内存": diagnose.SeverityWatch, "脏页": diagnose.SeverityWatch}
	cur := State{"内存": diagnose.SeverityCritical, "磁盘 IO": diagnose.SeverityWarning}

	got := Change(prev, cur)
	for _, want := range []string{"新出现 磁盘 IO", "变严重 内存", "已经好了 脏页"} {
		if !strings.Contains(got, want) {
			t.Errorf("Change = %q, want it to mention %q", got, want)
		}
	}
}

func TestChangeIsEmptyWhenNothingMoved(t *testing.T) {
	s := State{"内存": diagnose.SeverityWarning}
	if got := Change(s, s); got != "" {
		t.Errorf("Change = %q, want silence on an unchanged machine", got)
	}
}

func TestUnknownNumbersRejectsAnInventedPID(t *testing.T) {
	source := "进程 mysqld PID 1234：读 40.1 MiB/s"

	if bad := unknownNumbers("主要是 PID 1234 在读盘。", source); len(bad) != 0 {
		t.Errorf("unknownNumbers rejected a grounded answer: %v", bad)
	}
	if bad := unknownNumbers("问题出在 PID 9999 上。", source); len(bad) == 0 {
		t.Error("unknownNumbers accepted the invented PID 9999")
	}
	// A single digit is counting, not a measurement.
	if bad := unknownNumbers("有 7 个线程在等。", source); len(bad) != 0 {
		t.Errorf("unknownNumbers flagged a counted digit: %v", bad)
	}
	// "40" is grounded by "40.1": rejecting it would reject what a careful
	// model actually writes.
	if bad := unknownNumbers("大约 40 MiB/s。", source); len(bad) != 0 {
		t.Errorf("unknownNumbers rejected a truncated-but-grounded number: %v", bad)
	}
}

func TestSanitizeRejectsUnusableAnswers(t *testing.T) {
	source := "进程 mysqld PID 1234"

	cases := map[string]string{
		"empty":         "   ",
		"code block":    "```\n磁盘堵了\n```",
		"too long":      strings.Repeat("磁", maxParagraph+1),
		"invented PID":  "看 PID 8888。",
		"broken answer": "",
	}
	for name, raw := range cases {
		if _, err := sanitize(raw, source); err == nil {
			t.Errorf("sanitize accepted the %s answer", name)
		}
	}

	got, err := sanitize("磁盘堵了，PID 1234 在读盘。\n另外还有一点。", source)
	if err != nil {
		t.Fatalf("sanitize rejected a good answer: %v", err)
	}
	if strings.Contains(got, "\n") {
		t.Errorf("sanitize left a newline in %q; the output is one paragraph", got)
	}
}

func TestDigestOmitsCommandLines(t *testing.T) {
	r := sampleReport()
	r.Findings[0].Culprits[0].Cmdline = "/usr/sbin/mysqld --password=hunter2"

	d := Digest(r)
	if strings.Contains(d, "hunter2") {
		t.Error("the digest carries a command line; argv routinely holds credentials")
	}
	if !strings.Contains(d, "mysqld") || !strings.Contains(d, "1234") {
		t.Errorf("digest = %q, want the process name and pid", d)
	}
}
