// Package voice turns a diagnosis into something addressed to a person.
//
// The technical report answers "what is wrong". This answers the question
// someone actually has at 2am, which is "do I have to get up". The difference
// is not decoration: the same facts land completely differently as
// "io full avg10=17.40" and as "磁盘这边堵住了，但已经知道是谁，先别重启".
//
// Two rules hold everywhere here, and they are the reason the text can be
// trusted at all:
//
//  1. It never claims an action this program did not take. plumb reads /proc
//     and exits: it does not restart, kill, or reconfigure anything. So it
//     never says "我们正在处理" — a lie told during an incident is worse than a
//     cold number, because someone will act on it. The reassurance has to come
//     from the facts being specific and bounded, never from being told not to
//     worry.
//
//  2. It never softens a fact. A calm voice is not an optimistic one. The
//     urgency line is calibrated to severity and says plainly when something
//     needs attention now.
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

	// Situation is the kind of problem this is, in words that do not require
	// knowing what PSI stands for.
	Situation string

	// Facts are the hard numbers, copied out of the report verbatim.
	Facts []string

	// Stance states what plumb did, which is nothing.
	Stance string

	// First is the one thing worth doing before anything else.
	First string

	// Urgency answers "do I have to get up", calibrated to severity.
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
type script struct {
	situation string
	first     string
}

var scripts = map[string]script{
	"磁盘 IO": {
		situation: "磁盘这边堵住了。不是盘坏了 —— 是有东西把刷盘队列塞满了，" +
			"后面所有要落盘的动作都在排队。",
		first: "先别重启。重启会把现场一起清掉，而现场是唯一能告诉你原因的东西。" +
			"先用 pidstat -d 1 或 iotop -o 确认是谁在写。",
	},
	"内存": {
		situation: "内存不够了，系统正在花时间回收。" +
			"它的表现是停顿，不是那个百分比本身。",
		first: "先看下面那个常驻内存最大的进程能不能限制工作集。" +
			"限不住就只能加内存 —— 这个没有别的解法。",
	},
	"被堵住的线程": {
		situation: "有几个线程被内核按住了。它们没在干活，也没法被杀掉，" +
			"得先弄明白它们在等什么。",
		first: "别反复 kill。D 状态的线程会一直挂到 IO 完成为止，" +
			"先看它停在哪个内核函数、手里拿着哪个文件。",
	},
	"CPU": {
		situation: "CPU 是瓶颈，机器确实在忙。",
		first: "先看下面谁在吃 CPU。如果 softirq 高，那就是网络中断，" +
			"不是你的业务代码。",
	},
	"CPU 被抢占": {
		situation: "CPU 被别人抢走了。抢它的不是你的程序，" +
			"是同一台宿主机上的其他租户。",
		first: "在你自己的机器里做什么都没用 —— 这部分时间根本没到你手上。" +
			"换机型，或者找服务商要宿主机超售比例。",
	},
	"交换分区": {
		situation: "有 swap 被真正用起来了。这是内存不够的确定信号，" +
			"不是那种说不清的“看起来有点慢”。",
		first: "先找出工作集超过物理内存的那个进程。" +
			"换入换出会让响应时间上升一个数量级，而且表现为随机的慢。",
	},
	"单核跑满": {
		situation: "一个核心已经满了，其余核心在闲着。" +
			"这不是 CPU 不够，是一个线程把活全干了。",
		first: "别急着加核心 —— 对这个结构没有帮助。" +
			"要动的是并发方式：多开 worker，或者把请求分片。",
	},
	"磁盘空间": {
		situation: "磁盘要满了。这件事和别的不一样：它有确定的时间表，可以算出来还剩多久。",
		first: "先清日志和临时文件，再把占用最高的目录找出来。" +
			"写入放不下的时候，机器不会变慢，它会直接开始报错。",
	},
	"脏页": {
		situation: "脏页堆起来了。内核快要被迫同步刷盘，" +
			"而那一瞬间所有写操作都会一起卡住几秒。",
		first: "先找出写入突增的进程。如果这就是正常业务量，" +
			"调低 vm.dirty_ratio 能让刷盘变平滑，而不是攒到最后一起写。",
	},
}

// fallback is used for an area that has no script. A finding the message has no
// words for still gets reported; it just gets reported plainly.
var fallback = script{
	situation: "这里有一件事值得看一眼。",
	first:     "先看完整报告里这一条的证据，再决定要不要动。",
}

var urgency = map[diagnose.Severity]string{
	diagnose.SeverityCritical: "急不急：现在就在影响线上。建议先看一眼，别等它自己好。",
	diagnose.SeverityWarning:  "急不急：现在还不致命，但它在往坏的方向走。今天之内处理掉。",
	diagnose.SeverityWatch:    "急不急：不用马上动手，先记住它。要是连续几天都在，那就不是巧合了。",
	diagnose.SeverityInfo:     "急不急：这一条只是记录，不需要动手。",
}

const stance = "我没有动你的系统。plumb 只读 /proc，不重启、不 kill、不改配置 —— " +
	"所以上面每一句你都可以自己去核。"

// Compose builds the message for a report.
func Compose(r diagnose.Report) Message {
	m := Message{Stance: stance}

	if !r.HasProblems() {
		m.Situation = "这台机器现在没什么值得你半夜起来的事。"
		if len(r.Findings) > 0 {
			m.Others = "只记录了几条不需要动手的观察。"
		}
		return m
	}

	worst := r.Worst()
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

	line := fmt.Sprintf("最直接的是 %s（PID %d）", c.Name, c.PID)
	if detail != "" {
		line += "：" + strings.TrimSuffix(detail, "。")
	}
	line += "。"
	if n := len(f.Culprits) - 1; n > 0 {
		line += fmt.Sprintf("另外还有 %d 个在做同一件事。", n)
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
			detail += fmt.Sprintf("，另外 %d 个也在等", n)
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
// They are named and not elaborated: the reader who cares follows the pointer to
// the full report, and the reader who does not is not made to read six more
// paragraphs at 2am.
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
	return fmt.Sprintf("另外还有 %d 项：%s。完整报告：plumb", len(names), strings.Join(names, "、"))
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
	b.WriteString(paint("plumb", ansiBold, color))
	if r.System.Hostname != "" {
		b.WriteString(paint(" · "+r.System.Hostname, ansiDim, color))
	}
	b.WriteString("\n")
	b.WriteString(paint("采样 "+r.StartedAt.Format("2006-01-02 15:04:05")+
		" · 只读，不会改动这台机器", ansiDim, color))
	b.WriteString("\n\n")

	if m.Advice != "" {
		b.WriteString(paint("（下面这段由模型读完整份报告后写成，只基于本次数据；"+
			"事实以本文中的数字为准）", ansiDim, color))
		b.WriteString("\n")
		paragraph(&b, m.Advice, color)
	}

	paragraph(&b, m.Situation, color)
	paragraph(&b, joinAll(m.Facts), color)

	paragraph(&b, m.Stance, color)

	if m.First != "" {
		paragraph(&b, "现在最该做的："+m.First, color)
	}
	if m.Urgency != "" {
		paragraph(&b, m.Urgency, color)
	}
	if m.Others != "" {
		paragraph(&b, m.Others, color)
	}

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
	if text == "" {
		return
	}
	// Blank lines inside a paragraph are dropped: the caller decides spacing.
	for _, line := range report.Wrap(strings.ReplaceAll(text, "\n\n", "\n"), width) {
		if strings.TrimSpace(line) == "" {
			continue
		}
		b.WriteString(line)
		b.WriteString("\n")
	}
	b.WriteString("\n")
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
	ansiReset = "\x1b[0m"
	ansiBold  = "\x1b[1m"
	ansiDim   = "\x1b[90m"
)

func paint(s, code string, color bool) string {
	if !color || s == "" {
		return s
	}
	return code + s + ansiReset
}
