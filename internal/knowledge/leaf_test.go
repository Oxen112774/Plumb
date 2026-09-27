package knowledge

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// The tree is hand-written, so a test has to be the thing that notices a leaf
// with no sources, a duplicated key, or a catalogue line that has quietly grown
// into a paragraph. Those are the mistakes that are invisible while writing and
// obvious to every reader afterwards.
func TestEveryLeafIsWellFormed(t *testing.T) {
	all := All()
	if len(all) < 50 {
		t.Fatalf("the tree has only %d leaves; if shrinking it was deliberate, "+
			"lower this bar on purpose rather than by accident", len(all))
	}

	seen := map[string]bool{}
	for _, l := range all {
		if l.Key == "" {
			t.Error("a leaf has no key")
			continue
		}
		switch l.Kind {
		case KindWChan, KindArea, KindProc:
		default:
			t.Errorf("%s: invalid kind %d", l.Key, l.Kind)
		}
		switch l.Confidence {
		case Certain, Rough, NameOnly:
		default:
			t.Errorf("%s: invalid confidence %d", l.Key, l.Confidence)
		}
		if seen[l.Key] {
			t.Errorf("%s: duplicate key — Find would have to pick one, and "+
				"picking one quietly is the thing this tree must not do", l.Key)
		}
		seen[l.Key] = true

		if l.What == "" {
			t.Errorf("%s: no What", l.Key)
		}
		if len(l.Sources) == 0 {
			t.Errorf("%s: no sources — a leaf nobody can check is exactly what "+
				"this project exists to avoid", l.Key)
		}
		for _, s := range l.Sources {
			if strings.TrimSpace(s) == "" {
				t.Errorf("%s: an empty source line", l.Key)
			}
		}
		for _, a := range l.Alias {
			if strings.TrimSpace(a) == "" {
				t.Errorf("%s: an empty alias", l.Key)
			}
			if strings.EqualFold(a, l.Key) {
				t.Errorf("%s: an alias repeats the key", l.Key)
			}
		}

		// Certain means the fix is the standard one, so there has to be one.
		// If the honest answer is "do nothing", the leaf has to say that.
		if l.Confidence == Certain && len(l.Fix) == 0 {
			t.Errorf("%s: Certain but with no Fix — write 「不用处置」 if that is "+
				"the answer, but do not leave it blank", l.Key)
		}
		// A leaf that offers no fix and does not say where its knowledge stops
		// is a leaf that leaves the reader with nothing and no explanation.
		if l.Confidence != Certain && len(l.Fix) == 0 && l.Unknown == "" {
			t.Errorf("%s: not Certain, no Fix and no Unknown — a leaf with nothing "+
				"to offer still has to say why", l.Key)
		}
		// The catalogue line is the first sentence of What, so What has to open
		// with a short one. This keeps the listing scannable without a second
		// summary field that would drift out of step with the body.
		if n := utf8.RuneCountInString(l.Line()); n > 38 {
			t.Errorf("%s: catalogue line is %d runes, which is a paragraph not a "+
				"line: %q", l.Key, n, l.Line())
		}
	}
}

// Every leaf has to be reachable by its own key. A leaf shadowed by a looser
// match on another leaf is a bug that reports as "the tree has never heard of
// it", which is the worst way for a reference to fail.
func TestEveryKeyFindsItsOwnLeaf(t *testing.T) {
	for _, l := range All() {
		found := Find(l.Key)
		if len(found) == 0 {
			t.Errorf("%s: Find cannot find its own key", l.Key)
			continue
		}
		if found[0].Key != l.Key {
			t.Errorf("%s: Find(%q) returned %q first", l.Key, l.Key, found[0].Key)
		}
	}
}

func TestFindIsCaseInsensitiveAndPrefixFriendly(t *testing.T) {
	for _, needle := range []string{"IO_SCHEDULE", "io_schedule", "o_schedule", "IO_SCHED"} {
		if len(Find(needle)) == 0 {
			t.Errorf("Find(%q) found nothing", needle)
		}
	}
}

func TestFindReturnsEveryMatchRatherThanChoosing(t *testing.T) {
	got := Find("shrink_")
	if len(got) < 2 {
		t.Fatalf("Find(\"shrink_\") = %d leaves; the shrink family has more than "+
			"one member and all of them are legitimate answers", len(got))
	}
}

func TestFindRejectsNonsense(t *testing.T) {
	if got := Find("zzzz_not_a_kernel_symbol"); len(got) != 0 {
		t.Errorf("Find accepted nonsense and returned %d leaves", len(got))
	}
	if got := Find(""); len(got) != 0 {
		t.Errorf("Find(\"\") returned %d leaves", len(got))
	}
}

// The userspace-lock leaf has to keep saying that futex waits are an application
// problem. It is the one place where the tree's most likely reader — someone
// chasing a slow service — would otherwise be pointed at kernel tunables that
// cannot possibly help.
func TestFutexLeafPointsAtTheApplication(t *testing.T) {
	found := Find("futex_wait")
	if len(found) == 0 {
		t.Fatal("no leaf for futex_wait")
	}
	joined := strings.Join(found[0].Fix, " ") + found[0].What
	if !strings.Contains(joined, "应用") {
		t.Errorf("the futex leaf no longer says the problem is in the application: %q", joined)
	}
}

// Every leaf that is not Certain must carry a note, because the note is the only
// thing standing between "plumb is unsure" and a reader assuming plumb is sure.
func TestUncertainLeavesExplainThemselves(t *testing.T) {
	for _, l := range All() {
		if l.Confidence == Certain && l.Confidence.Note() != "" {
			t.Errorf("%s: a Certain leaf should not carry an uncertainty note", l.Key)
		}
		if l.Confidence != Certain && l.Confidence.Note() == "" {
			t.Errorf("%s: confidence %s has no note to print", l.Key, l.Confidence)
		}
	}
}
