package term

import "testing"

// TestASCIIIsAscii is the property the fallback exists for: a glyph set is
// only useful as a fallback if every glyph survives a terminal that cannot
// render anything above 0x7F. One stray box character in a set of twenty is
// one line of mojibake, which is the entire thing the set is there to avoid.
func TestASCIIIsAscii(t *testing.T) {
	g := ASCII()
	for name, s := range map[string]string{
		"HD": g.HD, "VD": g.VD, "TL": g.TL, "TR": g.TR, "BL": g.BL, "BR": g.BR,
		"LC": g.LC, "RC": g.RC, "TC": g.TC, "BC": g.BC, "Cross": g.Cross,
		"BlockFull": g.BlockFull, "BlockThree": g.BlockThree, "BlockHalf": g.BlockHalf,
		"BlockOne": g.BlockOne, "BlockEmpty": g.BlockEmpty, "ArrowRight": g.ArrowRight,
	} {
		for _, r := range s {
			if r > 127 {
				t.Errorf("%s = %q contains a non-ASCII rune", name, s)
				break
			}
		}
	}
}

// TestASCIIDoesNotCollapseEveryBlockIntoOneGlyph guards a subtle failure: a
// fallback where all five block levels print the same character makes every
// bar look empty or full regardless of the value, which is a wrong number
// wearing the costume of a rendering choice.
func TestASCIIDoesNotCollapseEveryBlockIntoOneGlyph(t *testing.T) {
	g := ASCII()
	if g.BlockFull == g.BlockEmpty {
		t.Error("full and empty blocks are the same character")
	}
	if g.BlockFull == g.BlockHalf || g.BlockHalf == g.BlockEmpty {
		t.Error("half block is indistinguishable from full or empty")
	}
}

func TestUnicodeBlocksAreDistinct(t *testing.T) {
	g := Unicode()
	seen := map[string]bool{}
	for _, s := range []string{g.BlockFull, g.BlockThree, g.BlockHalf, g.BlockOne, g.BlockEmpty} {
		if seen[s] {
			t.Errorf("duplicate block glyph %q", s)
		}
		seen[s] = true
	}
}

// TestLocaleIsUTF8 pins the encoding question, which is a different question
// from the language one: C means ASCII, C.UTF-8 means UTF-8, and a locale
// that names another charset means not UTF-8.
func TestLocaleIsUTF8(t *testing.T) {
	cases := []struct {
		locale string
		want   bool
	}{
		{"C", false},
		{"POSIX", false},
		{"C.UTF-8", true},
		{"en_US.UTF-8", true},
		{"zh_CN.GB18030", false},
		{"zh_CN.UTF-8", true},
	}
	for _, tc := range cases {
		t.Setenv("LC_ALL", tc.locale)
		t.Setenv("LC_CTYPE", "")
		t.Setenv("LANG", "")
		if got := localeIsUTF8(); got != tc.want {
			t.Errorf("localeIsUTF8 with %q = %v, want %v", tc.locale, got, tc.want)
		}
	}
}

func TestTerminalWidthFallsBack(t *testing.T) {
	t.Setenv("COLUMNS", "")
	if got := terminalWidth(); got != 80 {
		t.Errorf("terminalWidth with no COLUMNS = %d, want 80", got)
	}
	t.Setenv("COLUMNS", "132")
	if got := terminalWidth(); got != 132 {
		t.Errorf("terminalWidth = %d, want 132", got)
	}
	// A malformed value is 80, not a panic and not zero: a width of zero
	// would wrap every line at every character.
	t.Setenv("COLUMNS", "wide")
	if got := terminalWidth(); got != 80 {
		t.Errorf("terminalWidth with COLUMNS=wide = %d, want 80", got)
	}
}
