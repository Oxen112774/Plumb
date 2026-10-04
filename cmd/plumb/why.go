package main

import (
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/Oxen112774/plumb/internal/i18n"
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
func writeWhy(w io.Writer, needle string, color bool, lang i18n.Lang) error {
	if strings.TrimSpace(needle) == "" {
		return writeCatalogue(w, color, lang)
	}

	leaves := knowledge.Find(needle)
	if len(leaves) == 0 {
		return unknownKey(needle, lang)
	}

	for i, l := range leaves {
		if i > 0 {
			if _, err := fmt.Fprintln(w); err != nil {
				return err
			}
		}
		if len(leaves) > 1 {
			multi := "（%s 匹配到 %d 片，下面每一片都算）"
			if lang == i18n.EN {
				multi = "(%s matched %d leaves; each one counts)"
			}
			if _, err := fmt.Fprintf(w, "%s"+multi+"%s\n\n",
				dim(color), needle, len(leaves), reset(color)); err != nil {
				return err
			}
		}
		if err := writeLeaf(w, l, color, lang); err != nil {
			return err
		}
	}
	return nil
}

func writeLeaf(w io.Writer, l knowledge.Leaf, color bool, lang i18n.Lang) error {
	var b strings.Builder

	head := l.Key
	if color {
		head = whyBold + head + whyReset
	}
	sep := " · "
	if color {
		sep = whyDim + " · " + whyReset
	}
	kindLabel := l.Kind.String()
	confLabel := l.Confidence.String()
	if lang == i18n.EN {
		kindLabel = kindEN(l.Kind)
		confLabel = confEN(l.Confidence)
	}
	fmt.Fprintf(&b, "%s%s%s%s%s: %s\n",
		head, sep, kindLabel, sep,
		i18n.Get(lang, i18n.ConfidenceLabel), confLabel)

	section(&b, i18n.Get(lang, i18n.WhatIsIt), color)
	body(&b, []string{l.What}, color, false)

	if len(l.Why) > 0 {
		section(&b, i18n.Get(lang, i18n.WhyItHappens), color)
		body(&b, l.Why, color, true)
	}

	if len(l.Fix) > 0 {
		section(&b, i18n.Get(lang, i18n.HowToFix), color)
		body(&b, l.Fix, color, true)
	}

	if l.Unknown != "" {
		section(&b, i18n.Get(lang, i18n.UnknownParts), color)
		body(&b, []string{l.Unknown}, color, false)
	}

	section(&b, i18n.Get(lang, i18n.Sources), color)
	body(&b, l.Sources, color, true)

	if note := l.Confidence.Note(); note != "" {
		fmt.Fprintf(&b, "\n%s\n", wrapBlock(note, ""))
	}
	footer := "这一片来自内置的知识树：没有联网，也没有动你的系统。"
	if lang == i18n.EN {
		footer = "This leaf comes from the built-in knowledge tree: no network, no system changes."
	}
	fmt.Fprintf(&b, "\n%s\n", dimIf(color, footer))

	_, err := io.WriteString(w, b.String())
	return err
}

func writeCatalogue(w io.Writer, color bool, lang i18n.Lang) error {
	var b strings.Builder
	all := knowledge.All()
	hasLabel := "plumb 的知识树里有"
	hasLabelEN := "plumb's knowledge tree has"
	leavesLabel := "片叶子。"
	leavesLabelEN := "leaves."
	if lang == i18n.EN {
		fmt.Fprintf(&b, "%s\n", boldIf(color, fmt.Sprintf("%s %d %s", hasLabelEN, len(all), leavesLabelEN)))
	} else {
		fmt.Fprintf(&b, "%s\n", boldIf(color, fmt.Sprintf("%s %d %s", hasLabel, len(all), leavesLabel)))
	}
	for _, line := range report.Wrap(i18n.Get(lang, i18n.TreeHowToQuery), whyWidth) {
		fmt.Fprintf(&b, "%s\n", dimIf(color, line))
	}
	b.WriteString("\n")

	width := 0
	for _, l := range all {
		if n := report.DisplayWidth(l.Key); n > width {
			width = n
		}
	}
	if width > 34 {
		width = 34
	}

	for _, kind := range knowledge.Kinds() {
		var group []knowledge.Leaf
		for _, l := range all {
			if l.Kind == kind {
				group = append(group, l)
			}
		}
		if len(group) == 0 {
			continue
		}
		kindLabel := kind.String()
		if lang == i18n.EN {
			kindLabel = kindEN(kind)
		}
		fmt.Fprintf(&b, "%s\n", boldIf(color, fmt.Sprintf("%s（%d）", kindLabel, len(group))))
		indent := 2 + width + 2
		avail := whyWidth - indent
		for _, l := range group {
			if pad := width - report.DisplayWidth(l.Key); pad >= 1 {
				writeEntry(&b, "  "+l.Key+strings.Repeat(" ", pad)+"  ", indent, avail, l)
				continue
			}
			fmt.Fprintf(&b, "  %s\n", l.Key)
			writeEntry(&b, strings.Repeat(" ", indent), indent, avail, l)
		}
		b.WriteString("\n")
	}
	footer := i18n.Get(lang, i18n.TreeFooter) + "\n" + i18n.Get(lang, i18n.TreeFullEntry)
	fmt.Fprintf(&b, "%s\n", dimIf(color, footer))

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
func unknownKey(needle string, lang i18n.Lang) error {
	near := knowledge.Nearby(needle, 8)
	var b strings.Builder
	if lang == i18n.EN {
		fmt.Fprintf(&b, "No leaf in the knowledge tree matches %q.", needle)
	} else {
		fmt.Fprintf(&b, "知识树里没有匹配 %q 的叶子。", needle)
	}
	if len(near) > 0 {
		sort.Strings(near)
		if lang == i18n.EN {
			b.WriteString("\n\nNearby keys:\n")
		} else {
			b.WriteString("\n\n相近的有：\n")
		}
		for _, k := range near {
			fmt.Fprintf(&b, "  %s\n", k)
		}
	}
	if lang == i18n.EN {
		b.WriteString("\nList all: plumb -why")
	} else {
		b.WriteString("\n列出全部：plumb -why")
	}
	return fmt.Errorf("%s", b.String())
}

func kindEN(k knowledge.Kind) string {
	switch k {
	case knowledge.KindWChan:
		return "kernel wait-point"
	case knowledge.KindArea:
		return "check"
	case knowledge.KindProc:
		return "data source"
	case knowledge.KindStack:
		return "wait subsystem"
	}
	return "unknown"
}

func confEN(c knowledge.Confidence) string {
	switch c {
	case knowledge.Certain:
		return "evidence-backed"
	case knowledge.Rough:
		return "direction only"
	case knowledge.NameOnly:
		return "symptom only"
	}
	return "unknown"
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
