package main

import (
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/Oxen112774/plumb/internal/knowledge"
	"github.com/Oxen112774/plumb/internal/report"
)

// whyWidth is wider than the report's sixty-six. This is a page to read rather
// than a status line to scan, and a page that wraps early reads like a telegram.
const whyWidth = 74

const (
	whyReset = "\x1b[0m"
	whyBold  = "\x1b[1m"
	whyDim   = "\x1b[2m"
)

// bareWhy rewrites a value-less -why into -why=.
//
// The flag package cannot express an optional value: a string flag with nothing
// after it is a parse error, so "plumb -why" — the most obvious way there is to
// ask for the catalogue — would fail, and "plumb -why -no-color" would quietly
// take "-no-color" as the key. Four lines before parsing keep both spellings
// working, which matters because the catalogue's own footer tells the reader to
// run exactly this.
//
// A key never begins with a dash: they are kernel symbols, check names and /proc
// paths. So "the next argument is a flag" is unambiguous.
func bareWhy(args []string) []string {
	out := make([]string, 0, len(args))
	for i, a := range args {
		if (a == "-why" || a == "--why") &&
			(i+1 == len(args) || strings.HasPrefix(args[i+1], "-")) {
			out = append(out, "-why=")
			continue
		}
		out = append(out, a)
	}
	return out
}

// writeWhy answers -why.
//
// With no key it prints the catalogue, because a tree nobody can enumerate is a
// tree nobody can use — the first question about any reference is "what is in
// it". With a key it prints every leaf that key matches. Nothing here needs
// /proc, a server, or a network: the tree is compiled in, so this works on the
// laptop someone reads it on.
func writeWhy(w io.Writer, needle string, color bool) error {
	if strings.TrimSpace(needle) == "" {
		return writeCatalogue(w, color)
	}

	leaves := knowledge.Find(needle)
	if len(leaves) == 0 {
		return unknownKey(needle)
	}

	for i, l := range leaves {
		if i > 0 {
			if _, err := fmt.Fprintln(w); err != nil {
				return err
			}
		}
		if len(leaves) > 1 {
			if _, err := fmt.Fprintf(w, "%s（%s 匹配到 %d 片，下面每一片都算）%s\n\n",
				dim(color), needle, len(leaves), reset(color)); err != nil {
				return err
			}
		}
		if err := writeLeaf(w, l, color); err != nil {
			return err
		}
	}
	return nil
}

func writeLeaf(w io.Writer, l knowledge.Leaf, color bool) error {
	var b strings.Builder

	head := l.Key
	if color {
		head = whyBold + head + whyReset
	}
	sep := " · "
	if color {
		sep = whyDim + " · " + whyReset
	}
	fmt.Fprintf(&b, "%s%s%s%s可信度：%s\n",
		head, sep, l.Kind, sep, l.Confidence)

	section(&b, "它是什么", color)
	body(&b, []string{l.What}, color, false)

	if len(l.Why) > 0 {
		section(&b, "为什么会出现", color)
		body(&b, l.Why, color, true)
	}

	if len(l.Fix) > 0 {
		section(&b, "怎么处置", color)
		body(&b, l.Fix, color, true)
	}

	if l.Unknown != "" {
		section(&b, "看不到的地方", color)
		body(&b, []string{l.Unknown}, color, false)
	}

	section(&b, "出处", color)
	body(&b, l.Sources, color, true)

	// The confidence note goes last, after the evidence, because that is where a
	// reader is when they have to decide what to do. The label in the header has
	// already told them what to expect.
	if note := l.Confidence.Note(); note != "" {
		fmt.Fprintf(&b, "\n%s\n", wrapBlock(note, ""))
	}
	fmt.Fprintf(&b, "\n%s\n",
		dimIf(color, "这一片来自内置的知识树：没有联网，也没有动你的系统。"))

	_, err := io.WriteString(w, b.String())
	return err
}

func writeCatalogue(w io.Writer, color bool) error {
	var b strings.Builder
	all := knowledge.All()
	fmt.Fprintf(&b, "%s\n", boldIf(color, fmt.Sprintf("plumb 的知识树里有 %d 片叶子。", len(all))))
	for _, line := range report.Wrap(
		"查一片：plumb -why <键>。键可以是内核等待点符号、判定项的名字，或者 /proc 里的文件名。"+
			"大小写随意，前缀能对上就行。", whyWidth) {
		fmt.Fprintf(&b, "%s\n", dimIf(color, line))
	}
	b.WriteString("\n")

	width := 0
	for _, l := range all {
		if n := displayWidth(l.Key); n > width {
			width = n
		}
	}
	if width > 34 {
		width = 34
	}

	for _, kind := range []knowledge.Kind{knowledge.KindWChan, knowledge.KindArea, knowledge.KindProc} {
		var group []knowledge.Leaf
		for _, l := range all {
			if l.Kind == kind {
				group = append(group, l)
			}
		}
		if len(group) == 0 {
			continue
		}
		fmt.Fprintf(&b, "%s\n", boldIf(color, fmt.Sprintf("%s（%d）", kind, len(group))))
		// The summary gets the rest of the line and wraps onto continuations that
		// line up under it.
		indent := 2 + width + 2
		avail := whyWidth - indent
		for _, l := range group {
			if pad := width - displayWidth(l.Key); pad >= 1 {
				writeEntry(&b, "  "+l.Key+strings.Repeat(" ", pad)+"  ", indent, avail, l)
				continue
			}
			// A key too long for the column takes its summary on the next line
			// rather than pushing the column right for everyone.
			fmt.Fprintf(&b, "  %s\n", l.Key)
			writeEntry(&b, strings.Repeat(" ", indent), indent, avail, l)
		}
		b.WriteString("\n")
	}
	fmt.Fprintf(&b, "%s\n", dimIf(color,
		"不确定的地方，每一片里都写着 —— 这一棵树不含糊其辞。\n"+
			"看一片的全文：plumb -why <键>"))

	_, err := io.WriteString(w, b.String())
	return err
}

// writeEntry prints one catalogue line, wrapping the summary onto continuation
// lines that line up under it.
//
// Wrapping here rather than leaving it to the terminal is not cosmetic. A Chinese
// summary is about twice as wide as its rune count suggests, so most of them
// overflow — and a terminal wraps an overflowing line back to column zero, which
// turns a tidy two-column list into a wall where the eye can no longer tell a key
// from its summary.
func writeEntry(b *strings.Builder, prefix string, indent, avail int, l knowledge.Leaf) {
	lines := report.Wrap(l.Line(), avail)
	if len(lines) == 0 {
		fmt.Fprintf(b, "%s\n", prefix)
		return
	}
	fmt.Fprintf(b, "%s%s\n", prefix, lines[0])
	for _, line := range lines[1:] {
		fmt.Fprintf(b, "%s%s\n", strings.Repeat(" ", indent), line)
	}
}

// unknownKey explains a miss.
//
// A bare "not found" would be the wrong answer here: the whole point of a keyed
// tree is that the reader has a name in hand and wants to know whether it is in
// it. So the message says what to try instead, and reminds them that the
// catalogue exists.
func unknownKey(needle string) error {
	near := knowledge.Nearby(needle, 8)
	var b strings.Builder
	fmt.Fprintf(&b, "知识树里没有匹配 %q 的叶子。", needle)
	if len(near) > 0 {
		sort.Strings(near)
		b.WriteString("\n\n相近的有：\n")
		for _, k := range near {
			fmt.Fprintf(&b, "  %s\n", k)
		}
	}
	b.WriteString("\n列出全部：plumb -why")
	return fmt.Errorf("%s", b.String())
}

// section prints a section heading.
func section(b *strings.Builder, title string, color bool) {
	fmt.Fprintf(b, "\n%s\n", dimIf(color, title))
}

// body prints a block of text, wrapped and indented.
//
// bullets is passed rather than inferred from the item count. Inferring it looks
// tidy until a leaf has exactly one source or one reason, and then the same
// section prints as a paragraph in one entry and a bullet in the next — which
// reads as though something went missing.
func body(b *strings.Builder, items []string, color, bullets bool) {
	if !bullets {
		for _, item := range items {
			for _, line := range report.Wrap(item, whyWidth-2) {
				fmt.Fprintf(b, "  %s\n", line)
			}
		}
		return
	}
	for _, item := range items {
		lines := report.Wrap(item, whyWidth-4)
		for i, line := range lines {
			if i == 0 {
				fmt.Fprintf(b, "  · %s\n", line)
				continue
			}
			// Continuation lines line up under the text, not under the bullet.
			fmt.Fprintf(b, "    %s\n", line)
		}
	}
}

// wrapBlock wraps a standalone sentence with no indent.
func wrapBlock(s string, indent string) string {
	return strings.Join(report.Wrap(s, whyWidth-len(indent)), "\n"+indent)
}

func reset(color bool) string { return tone(color, whyReset) }

func dim(color bool) string { return tone(color, whyDim) }

func boldIf(color bool, s string) string { return tone(color, whyBold) + s + reset(color) }

func dimIf(color bool, s string) string { return tone(color, whyDim) + s + reset(color) }

func tone(color bool, code string) string {
	if !color {
		return ""
	}
	return code
}

// displayWidth estimates how many terminal columns a string occupies: a CJK
// character or full-width punctuation takes two, anything else one.
//
// It exists because the leaf keys mix Chinese and ASCII, and a column padded by
// byte count comes out visibly ragged on exactly the ten entries that are
// Chinese. An estimate is enough here — the keys are short, and being two
// columns out on an exotic glyph costs nothing.
func displayWidth(s string) int {
	w := 0
	for _, r := range s {
		switch {
		case r >= 0x1100 && r <= 0x115f, // Hangul Jamo
			r >= 0x2e80 && r <= 0xa4cf, // CJK radicals through Yi
			r >= 0xac00 && r <= 0xd7a3, // Hangul syllables
			r >= 0xf900 && r <= 0xfaff, // CJK compatibility ideographs
			r >= 0xfe30 && r <= 0xfe6f, // CJK compatibility forms
			r >= 0xff00 && r <= 0xff60, // full-width forms
			r >= 0xffe0 && r <= 0xffe6: // full-width signs
			w += 2
		default:
			w++
		}
	}
	return w
}
