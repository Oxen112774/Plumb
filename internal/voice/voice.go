// Package voice turns a diagnosis into something addressed to a person.
//
// The technical report answers "what is wrong". This answers the question
// someone actually has at 2am, which is "do I have to get up". The difference is
// not decoration: the same facts land completely differently as
// "io full avg10=17.40" and as "不是盘坏了 —— 刷盘队列被塞满".
//
// The tone is engineered, not accidental, and it is short on purpose. At 2am a
// long paragraph is a paragraph nobody finishes; what makes the text trustworthy
// is that every sentence is either a fact, an instruction, or a boundary — and
// nothing else. Terseness here is a form of respect for the reader's attention,
// which is the scarcest thing on the machine at that hour.
//
// Two rules hold everywhere here, and they are the reason the text can be
// trusted at all:
//
//  1. It never claims an action this program did not take. plumb reads /proc and
//     exits: it does not restart, kill, or reconfigure anything. So it never says
//     "我们正在处理" — a lie told during an incident is worse than a cold number,
//     because someone will act on it. The reassurance has to come from the facts
//     being specific and bounded, never from being told not to worry.
//
//  2. It never softens a fact. A calm voice is not an optimistic one. The
//     severity is in the first line of the output, before any prose, and the
//     instruction says plainly when something needs attention now.
//
// Nothing here computes a number. Every figure in the message is copied out of
// the diagnosis, so the reassuring sentence and the evidence cannot disagree.
package voice

import (
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/Oxen112774/plumb/internal/diagnose"
	"github.com/Oxen112774/plumb/internal/report"
)

// width is the column budget for body text. It matches the report so the two
// outputs sit in the same terminal without one looking like it came from a
// different program.
const width = 66

// Message is a diagnosis rewritten for a person.
type Message struct {
	// Advice is the model's synthesis paragraph, when one is configured. It is
	// always optional: an empty Advice means the message is complete without it.
	Advice string

	// Severity is the worst finding's severity. It goes in the header, before
	// any prose, because "do I have to get up" is the question being asked and
	// the answer should not require reading a paragraph to find.
	Severity diagnose.Severity

	// Situation is the kind of problem this is: the mechanism, without the
	// magnitude, which the facts already carry.
	Situation string

	// Facts are the hard numbers, copied out of the report verbatim.
	Facts []string

	// Stance states what plumb did, which is nothing.
	Stance string

	// First is the one thing worth doing before anything else.
	First string

	// Urgency answers "do I have to get up", calibrated to severity. It runs as
	// the last sentence of the instruction block, because "what do I do" and
	// "does it have to be now" are one question, not two.
	Urgency string

	// Others names the remaining findings without elaborating on them.
	Others string
}

// script is the hand-written half of the message: what kind of trouble this is,
// and what to do about it first.
//
// It is a table rather than a generated paragraph on purpose. The set of things
// that can be wrong with a Linux server is small and known, and a sentence
// written by someone who has been woken up by each of them is better than a
// sentence generated fresh every time. A model, when one is configured, adds a
// synthesis paragraph on top; it does not replace these.
//
// Both halves obey the same rule: say the thing that changes what the reader
// does next, then stop. "先别重启" stays because restarting destroys the
// evidence; "磁盘可能存在问题" does not, because it changes nothing.
type script struct {
	situation string
	first     string
}

var scripts = map[string]script{
	"磁盘 IO": {
		situation: "问题在磁盘：不是盘坏了 —— 刷盘队列被塞满，写操作全在排队。",
		first:     "先别重启：现场是唯一能告诉你原因的东西。用 pidstat -d 1 看是谁在写。",
	},
	"内存": {
		situation: "内存不够，系统在花时间回收 —— 卡住的是回收，不是你的程序。",
		first:     "先限制常驻最大的那个进程的工作集。限不住就只能加内存。",
	},
	"被堵住的线程": {
		situation: "有线程被内核按住：没在干活，也杀不掉。",
		first:     "别反复 kill —— D 状态会挂到 IO 完成为止。先看它停在哪个内核函数、拿着哪个文件。",
	},
	"CPU": {
		situation: "CPU 是瓶颈，机器确实在忙。",
		first:     "看下面谁在吃 CPU。softirq 高就是网络中断，不是业务代码。",
	},
	"CPU 被抢占": {
		situation: "CPU 被宿主机抢走了 —— 抢它的不是你的程序。",
		first:     "在你自己机器里调什么都是白费。换机型，或者找服务商要超售比例。",
	},
	"交换分区": {
		situation: "swap 被真正用起来了 —— 内存不够的确定信号。",
		first:     "找出工作集超过物理内存的那个进程。换入换出会把响应时间抬一个数量级。",
	},
	"单核跑满": {
		situation: "一个核心满了，其余在闲着 —— 这不是 CPU 不够。",
		first:     "别加核心：这结构加核心没用。要改的是并发方式。",
	},
	"磁盘空间": {
		situation: "磁盘空间要满了。这件事有时间表，能算出还剩多久。",
		first:     "先清日志和临时文件。写不进去的时候机器不会变慢，它直接报错。",
	},
	"脏页": {
		situation: "脏页堆起来了 —— 内核快要被迫同步刷盘，那一瞬间所有写都会卡住几秒。",
		first:     "找出写入突增的进程。是正常业务量就调低 vm.dirty_ratio。",
	},
}

// fallback is used for an area that has no script. A finding the message has no
// words for still gets reported; it just gets reported plainly.
var fallback = script{
	situation: "这一条值得看一眼。",
	first:     "先看完整报告里这一条的证据，再决定要不要动。",
}

// urgency is deliberately blunt about time. "建议关注一下" would be softer and
// useless: the reader is trying to decide whether to get out of bed.
var urgency = map[diagnose.Severity]string{
	diagnose.SeverityCritical: "现在就在影响线上，别等它自己好。",
	diagnose.SeverityWarning:  "现在还不致命，但在往坏的方向走 —— 今天之内处理掉。",
	diagnose.SeverityWatch:    "不用马上动手，先记住；连着几天都在就不是巧合。",
	diagnose.SeverityInfo:     "只是记录，不用动手。",
}

const stance = "我只读了 /proc，没动你的系统 —— 上面每一句都能自己去核。"

// Compose builds the message for a report.
func Compose(r diagnose.Report) Message {
	worst := r.Worst()
	m := Message{Stance: stance, Severity: worst.Severity}

	if !r.HasProblems() {
		m.Situation = "现在没事 —— 没有需要你半夜起来的东西。"
		if len(r.Findings) > 0 {
			m.Others = "有几条只记录、不需要动手的观察。"
		}
		return m
	}

	s, ok := scripts[worst.Area]
	if !ok {
		s = fallback
	}
	m.Situation = s.situation
	m.First = s.first
	m.Facts = factLines(worst)
	m.Urgency = urgency[worst.Severity]
	m.Others = othersLine(r, worst)
	return m
}

// factLines copies the hard facts out of a finding.
//
// Nothing is re-derived here. The headline and the culprit note are used as they
// were built, so the message cannot disagree with the report it summarises: if
// they ever did, the report would be right and the reassuring sentence would be
// the lie.
func factLines(f diagnose.Finding) []string {
	var out []string
	if f.Headline != "" {
		out = append(out, f.Headline)
	}
	if len(f.Culprits) == 0 {
		return out
	}

	c := f.Culprits[0]
	detail, held := splitNote(c.Note)

	line := fmt.Sprintf("元凶 %s（PID %d）", c.Name, c.PID)
	if detail != "" {
		line += "：" + strings.TrimSuffix(detail, "。")
	}
	line += "。"
	if n := len(f.Culprits) - 1; n > 0 {
		line += fmt.Sprintf("另有 %d 个在干同一件事。", n)
	}
	out = append(out, line)

	if held != "" {
		out = append(out, "它手里拿着 "+held+"。")
	}
	return out
}

// splitNote separates a culprit note into its inline part and the files it says
// the process holds.
//
// A note uses explicit newlines to separate independent facts: one line per
// blocked thread, then a header, then one line per held file. Inline, the
// newlines have to become punctuation, and a file list has to stay a list — so
// the two halves are returned separately rather than flattened together.
func splitNote(note string) (detail, held string) {
	var inline, files []string
	seenHeader := false
	for _, raw := range strings.Split(note, "\n") {
		line := strings.TrimSpace(raw)
		if line == "" {
			continue
		}
		switch {
		case line == diagnose.HeldFilesHeader:
			seenHeader = true
		case seenHeader:
			files = append(files, line)
		default:
			// A note is written for a fixed-width column, where the symbol is
			// set off by two spaces. Inline that reads as a typo.
			inline = append(inline, strings.ReplaceAll(line, "  [", " ["))
		}
	}

	if len(inline) > 0 {
		detail = inline[0]
		if n := len(inline) - 1; n > 0 {
			detail += fmt.Sprintf("，另有 %d 个也在等", n)
		}
	}
	switch len(files) {
	case 0:
	case 1:
		held = files[0]
	default:
		held = fmt.Sprintf("%s 等 %d 个文件", files[0], len(files))
	}
	return detail, held
}

// othersLine names the findings the message did not lead with.
//
// Named, not elaborated: six more paragraphs is how a 2am message stops being
// read. Whoever wants the detail runs plumb without -voice.
func othersLine(r diagnose.Report, worst diagnose.Finding) string {
	var names []string
	for _, f := range r.Findings {
		if f.Area == worst.Area {
			continue
		}
		names = append(names, f.Area)
	}
	if len(names) == 0 {
		return ""
	}
	return fmt.Sprintf("另有 %d 项：%s。", len(names), strings.Join(names, "、"))
}

// Write renders the message.
//
// model may be nil, which is the normal case: the message is complete without
// it, and a model that fails — no network, no key, a timeout, an answer that
// invents a number — degrades to exactly the text this function would have
// produced anyway. A failure is reported on stderr rather than in the message:
// what a model did is not part of the diagnosis.
func Write(w io.Writer, r diagnose.Report, color bool, model *Model) error {
	m := Compose(r)
	if model != nil {
		if p, err := model.ParagraphFor(Digest(r)); err == nil {
			m.Advice = p
		} else {
			model.note("模型那一段没写成（%v），这一次只用模板", err)
		}
	}

	var b strings.Builder

	// The severity is in the header, before any prose. It is the answer to the
	// only question the reader has, and making them read a paragraph to reach it
	// would be a strange way to show respect for their time.
	b.WriteString(paint("plumb", ansiBold, color))
	if r.System.Hostname != "" {
		b.WriteString(paint(" · "+r.System.Hostname, ansiDim, color))
	}
	b.WriteString(" · " + paint(m.Severity.Label(), severityColor(m.Severity), color))
	b.WriteString(paint(" · "+r.StartedAt.Format("2006-01-02 15:04:05"), ansiDim, color))
	b.WriteString("\n\n")

	if m.Advice != "" {
		b.WriteString(paint("〔模型综合〕", ansiDim, color))
		b.WriteString("\n")
		paragraph(&b, m.Advice, color)
	}

	// Kind of problem, then the facts for it. One block: the numbers are the
	// evidence for the sentence above them, not a separate topic.
	//
	// Each fact gets its own line rather than being run together. A fact that
	// starts mid-line can have its two-word label orphaned by a line break
	// ("…都在等它。元凶" / "mysqld（PID 1234）"), which reads as a typo; a fact
	// that starts at column zero cannot, because the wrapper never breaks before
	// half the line.
	footer(&b, append([]string{m.Situation}, m.Facts...)...)

	// What to do, and whether it has to be now.
	if m.First != "" {
		paragraph(&b, joinAll([]string{m.First, m.Urgency}), color)
	}

	// The footer: what else is there, and what plumb did not do. No blank line
	// between them — they close the message, they do not continue it.
	footer(&b, m.Others, m.Stance)

	_, err := io.WriteString(w, strings.TrimRight(b.String(), "\n")+"\n")
	return err
}

// joinAll folds several facts into one paragraph, ending each with a full stop.
//
// A headline is written as a fragment because in the report it is a heading, not
// a sentence. Run together with the next fact it produced
// "…都在等它最直接的是 mysqld", so the join supplies the punctuation the
// heading did not have.
func joinAll(parts []string) string {
	out := ""
	for _, p := range parts {
		if p == "" {
			continue
		}
		if out == "" {
			out = p
			continue
		}
		if r := []rune(out); !strings.ContainsRune("。！？；：", r[len(r)-1]) {
			out += "。"
		}
		out += p
	}
	return out
}

// paragraph writes one wrapped block followed by a blank line.
func paragraph(b *strings.Builder, text string, color bool) {
	if wrapped(b, text) {
		b.WriteString("\n")
	}
}

// footer writes closing lines as one block, without blank lines between them.
func footer(b *strings.Builder, parts ...string) {
	wrote := false
	for _, p := range parts {
		if wrapped(b, p) {
			wrote = true
		}
	}
	if wrote {
		b.WriteString("\n")
	}
}

// wrapped writes one wrapped block and reports whether it wrote anything.
func wrapped(b *strings.Builder, text string) bool {
	if text == "" {
		return false
	}
	// Blank lines inside a paragraph are dropped: the caller owns spacing.
	wrote := false
	for _, line := range report.Wrap(strings.ReplaceAll(text, "\n\n", "\n"), width) {
		if strings.TrimSpace(line) == "" {
			continue
		}
		b.WriteString(line)
		b.WriteString("\n")
		wrote = true
	}
	return wrote
}

// State is the set of findings worth watching, by area.
type State map[string]diagnose.Severity

// StateOf reduces a report to what change detection cares about.
//
// Info-severity findings are left out. One or two D-state threads is a normal
// instant in the life of a database, so an info-level 被堵住的线程 appears and
// disappears constantly; tracking it would make the watcher talk on every
// round, which is how a watcher gets muted and then stops being a watcher.
func StateOf(r diagnose.Report) State {
	s := State{}
	for _, f := range r.Findings {
		if f.Severity.Rank() > diagnose.SeverityWatch.Rank() {
			continue
		}
		s[f.Area] = f.Severity
	}
	return s
}

// Change describes what moved between two observations, or returns "" when
// nothing did.
func Change(prev, cur State) string {
	var appeared, worsened, cleared []string

	for area, sev := range cur {
		old, seen := prev[area]
		switch {
		case !seen:
			appeared = append(appeared, fmt.Sprintf("%s（%s）", area, sev.Label()))
		case sev.Rank() < old.Rank():
			worsened = append(worsened, fmt.Sprintf("%s %s → %s", area, old.Label(), sev.Label()))
		}
	}
	for area := range prev {
		if _, ok := cur[area]; !ok {
			cleared = append(cleared, area)
		}
	}

	sort.Strings(appeared)
	sort.Strings(worsened)
	sort.Strings(cleared)

	var parts []string
	if len(appeared) > 0 {
		parts = append(parts, "新出现 "+strings.Join(appeared, "、"))
	}
	if len(worsened) > 0 {
		parts = append(parts, "变严重 "+strings.Join(worsened, "、"))
	}
	if len(cleared) > 0 {
		parts = append(parts, "已经好了 "+strings.Join(cleared, "、"))
	}
	return strings.Join(parts, "；")
}

// --- colour ---

const (
	ansiReset  = "\x1b[0m"
	ansiBold   = "\x1b[1m"
	ansiDim    = "\x1b[90m"
	ansiRed    = "\x1b[31m"
	ansiYellow = "\x1b[33m"
	ansiCyan   = "\x1b[36m"
	ansiGreen  = "\x1b[32m"
)

func severityColor(s diagnose.Severity) string {
	switch s {
	case diagnose.SeverityCritical:
		return ansiRed
	case diagnose.SeverityWarning:
		return ansiYellow
	case diagnose.SeverityWatch:
		return ansiCyan
	default:
		return ansiGreen
	}
}

func paint(s, code string, color bool) string {
	if !color || s == "" {
		return s
	}
	return code + s + ansiReset
}
