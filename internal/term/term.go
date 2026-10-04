// Package term probes the terminal plumb is writing to.
//
// The question is not "is this a terminal" — os.Stdout.Stat() answers that —
// but "what can this terminal render". A Chinese headline printed to an ASCII
// terminal becomes a wall of question marks; a Unicode box border printed to
// Windows cmd becomes mojibake; colour codes written to a pipe become noise in
// a log file. Each capability is detected independently so the report can
// degrade one axis at a time rather than switching between "full" and "none".
package term

import (
	"os"
	"strings"
)

// Capability is what the output layer may use.
type Capability struct {
	// IsTerminal is true when stdout is a character device, not a pipe or file.
	IsTerminal bool

	// UTF8 is true when the terminal's locale is UTF-8. When false, Chinese
	// text and Unicode box-drawing are unsafe: the reader sees question marks.
	UTF8 bool

	// Color is true when ANSI escape codes will be rendered. A pipe never
	// gets colour even if the user's terminal supports it.
	Color bool

	// Width is the terminal column count, or 80 when it cannot be determined.
	Width int
}

// Probe inspects stdout and the environment.
func Probe() Capability {
	fi, err := os.Stdout.Stat()
	isTerm := err == nil && fi.Mode()&os.ModeCharDevice != 0

	return Capability{
		IsTerminal: isTerm,
		UTF8:       localeIsUTF8(),
		Color:      isTerm, // colour follows the device; -no-color overrides later
		Width:      terminalWidth(),
	}
}

// localeIsUTF8 checks LC_ALL/LC_CTYPE/LANG for a UTF-8 suffix. The C and
// POSIX locales are ASCII. A missing locale on a Linux server is treated as
// UTF-8: every mainstream distribution defaults to it, and treating "not set"
// as ASCII would needlessly degrade the common case.
func localeIsUTF8() bool {
	for _, v := range []string{
		os.Getenv("LC_ALL"),
		os.Getenv("LC_CTYPE"),
		os.Getenv("LANG"),
	} {
		if v == "" {
			continue
		}
		if v == "C" || v == "POSIX" {
			return false
		}
		low := strings.ToLower(v)
		if strings.Contains(low, "utf-8") || strings.Contains(low, "utf8") {
			return true
		}
		// A non-UTF-8 locale was explicitly set (e.g. zh_CN.GB18030, C.UTF-8
		// is caught above). Treat as non-UTF-8 only when a charset is present
		// and it is not UTF-8.
		if strings.Contains(low, ".") {
			return false
		}
	}
	return true
}

// terminalWidth reads COLUMNS (set by interactive shells and some terminals),
// falling back to 80. Getting the real width via ioctl would need a syscall
// that varies by OS, and 80 is the width every terminal guarantees.
func terminalWidth() int {
	if s := os.Getenv("COLUMNS"); s != "" {
		if n := atoiSimple(s); n > 0 {
			return n
		}
	}
	return 80
}

func atoiSimple(s string) int {
	n := 0
	for _, c := range s {
		if c < '0' || c > '9' {
			return 0
		}
		n = n*10 + int(c-'0')
	}
	return n
}

// Glyphs is the box-drawing character set for the current terminal mode.
type Glyphs struct {
	HD, VD, TL, TR, BL, BR, LC, RC, TC, BC, Cross          string
	BlockFull, BlockThree, BlockHalf, BlockOne, BlockEmpty string
	ArrowRight                                             string
}

// Unicode returns the Unicode box-drawing set.
func Unicode() Glyphs {
	return Glyphs{
		HD: "─", VD: "│", TL: "┌", TR: "┐", BL: "└", BR: "┘",
		LC: "├", RC: "┤", TC: "┬", BC: "┴", Cross: "┼",
		BlockFull: "█", BlockThree: "▊", BlockHalf: "▌", BlockOne: "▏", BlockEmpty: "░",
		ArrowRight: "→",
	}
}

// ASCII returns the pure-ASCII fallback set for terminals that cannot render
// Unicode box drawing (old cmd, serial consoles, some SSH clients).
func ASCII() Glyphs {
	return Glyphs{
		HD: "-", VD: "|", TL: "+", TR: "+", BL: "+", BR: "+",
		LC: "+", RC: "+", TC: "+", BC: "+", Cross: "+",
		BlockFull: "#", BlockThree: "#", BlockHalf: "=", BlockOne: ".", BlockEmpty: " ",
		ArrowRight: "->",
	}
}
