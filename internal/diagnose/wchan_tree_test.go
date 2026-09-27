package diagnose

import (
	"testing"

	"github.com/Oxen112774/plumb/internal/knowledge"
)

// Every symbol the report can print has to be answerable by -why.
//
// Without this the translation table and the knowledge tree drift apart, and the
// failure is the worst kind: a reader sees a symbol in the output, asks plumb
// what it means, and is told the tree has never heard of it. Offering a way to
// look something up and then having nothing to show is worse than never offering
// it, so the two lists are tied together here rather than by discipline.
func TestEveryTranslatedSymbolHasALeaf(t *testing.T) {
	for _, e := range waitTable {
		if len(knowledge.Find(e.prefix)) == 0 {
			t.Errorf("wchan prefix %q is translated in the report but has no leaf "+
				"in the knowledge tree", e.prefix)
		}
	}
}
