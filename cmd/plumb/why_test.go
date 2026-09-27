package main

import (
	"bytes"
	"io"
	"strings"
	"testing"

	"github.com/Oxen112774/plumb/internal/knowledge"
)

// -why touches nothing but the compiled-in tree: no sampling, no /proc, no
// network. That is the whole reason run answers it before the proc check, and
// this test is what keeps it that way.
func TestWhyNeedsNothingButTheTree(t *testing.T) {
	var buf bytes.Buffer
	if err := writeWhy(&buf, "io_schedule", false); err != nil {
		t.Fatalf("writeWhy: %v", err)
	}
	out := buf.String()
	for _, want := range []string{"io_schedule", "它是什么", "怎么处置", "出处"} {
		if !strings.Contains(out, want) {
			t.Errorf("the leaf for io_schedule does not print %q:\n%s", want, out)
		}
	}
}

func TestCatalogueListsEveryLeaf(t *testing.T) {
	var buf bytes.Buffer
	if err := writeWhy(&buf, "", false); err != nil {
		t.Fatalf("writeWhy: %v", err)
	}
	out := buf.String()
	for _, l := range knowledge.All() {
		if !strings.Contains(out, l.Key) {
			t.Errorf("%s is missing from the catalogue", l.Key)
		}
	}
}

// Nothing in either mode may exceed the page width. An overflowing line gets
// wrapped by the terminal back to column zero, which is exactly what makes
// hand-formatted output look broken — and it breaks hardest on the narrow
// terminals where a reader most needs the layout to hold.
func TestWhyNeverOverflowsThePage(t *testing.T) {
	check := func(name, needle string) {
		var buf bytes.Buffer
		if err := writeWhy(&buf, needle, false); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		for i, line := range strings.Split(buf.String(), "\n") {
			// The two columns of slack absorb the wrapper's line-start
			// punctuation rule, which prefers a one-glyph overflow to letting a
			// closing mark open a line.
			if w := displayWidth(line); w > whyWidth+2 {
				t.Errorf("%s: line %d is %d columns, over the %d budget:\n%s",
					name, i+1, w, whyWidth, line)
			}
		}
	}
	check("catalogue", "")
	check("longest key", "try_to_free_mem_cgroup_pages")
	check("a leaf with no fix", "wait_for_completion")
	check("a leaf with a single source", "/proc/pressure/cpu")
	check("a multi-match", "shrink_")
}

// A miss has to be a miss: nothing on the page, an explanation on the error, and
// exit code 1 through main. Printing a leaf-looking page and succeeding would
// teach a reader to trust output that means nothing.
func TestWhyReportsAMissAsAnError(t *testing.T) {
	var buf bytes.Buffer
	err := writeWhy(&buf, "zzzz_not_a_symbol", false)
	if err == nil {
		t.Fatal("a key that is not in the tree was reported as found")
	}
	if buf.Len() != 0 {
		t.Errorf("a miss wrote %q to the page; it belongs on stderr with the other "+
			"errors", buf.String())
	}
}

func TestBareWhyMeansTheWholeTree(t *testing.T) {
	cases := []struct {
		in   []string
		want []string
	}{
		{[]string{"-why"}, []string{"-why="}},
		{[]string{"-why", "-no-color"}, []string{"-why=", "-no-color"}},
		{[]string{"--why"}, []string{"-why="}},
		{[]string{"-why", "io_schedule"}, []string{"-why", "io_schedule"}},
		{[]string{"-why", "/proc/pressure/io"}, []string{"-why", "/proc/pressure/io"}},
		{[]string{"-json", "-why"}, []string{"-json", "-why="}},
		{nil, nil},
	}
	for _, c := range cases {
		got := bareWhy(c.in)
		if len(got) != len(c.want) {
			t.Errorf("bareWhy(%q) = %q, want %q", c.in, got, c.want)
			continue
		}
		for i := range got {
			if got[i] != c.want[i] {
				t.Errorf("bareWhy(%q) = %q, want %q", c.in, got, c.want)
				break
			}
		}
	}
}

func TestBareWhyReachesTheCatalogue(t *testing.T) {
	// The catalogue prints to os.Stdout, which go test swallows unless it fails;
	// what is being checked here is that the flag parses at all and that run
	// does not then want a /proc tree.
	for _, args := range [][]string{{"-why", "-no-color"}, {"-why=", "-no-color"}, {"--why"}} {
		if err := run(args, io.Discard); err != nil {
			t.Errorf("run(%q): %v", args, err)
		}
	}
}

func TestWhyRefusesToBeCombinedWithAMode(t *testing.T) {
	for _, extra := range []string{"-json", "-voice", "-watch", "-assert", "-ai"} {
		if err := run([]string{"-why", "io_schedule", extra}, io.Discard); err == nil {
			t.Errorf("plumb -why io_schedule %s was accepted; -why is on its own "+
				"and silently ignoring a flag someone typed is not an option", extra)
		}
	}
	// -root takes a value, so it is spelled out rather than looped.
	if err := run([]string{"-why", "io_schedule", "-root", "/tmp"}, io.Discard); err == nil {
		t.Error("plumb -why io_schedule -root /tmp was accepted")
	}
}
