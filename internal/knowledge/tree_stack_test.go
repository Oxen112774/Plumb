package knowledge

import (
	"testing"

	"github.com/Oxen112774/plumb/internal/probe"
)

// probe is imported here and nowhere else in this package, and only from a
// test: the tree is data and must not depend on the sampler at build time. The
// dependency is worth taking in a test because the two tables name the same
// things from opposite ends — probe decides what a stack is waiting on, this
// tree explains what that means.

// Every subsystem the stack classifier can name must have a leaf here.
//
// The report prints "内核栈：NFS/RPC". If the tree has no leaf for it, the
// reader who follows the instructions at the bottom of the page — run
// `plumb -why <key>` — gets "no such key" for something plumb itself just
// printed. That is the tree failing at the exact moment it is asked for.
func TestStackLeavesCoverEverySubsystem(t *testing.T) {
	for _, name := range probe.SubsystemNames() {
		var found bool
		for _, l := range All() {
			if l.Kind == KindStack && l.Key == name {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("the classifier can produce %q but the tree has no leaf for it", name)
		}
	}
}

// And no leaves for subsystems that cannot be produced, which would be a
// chapter of the tree nothing can ever reach.
func TestNoStackLeafWithoutARule(t *testing.T) {
	known := map[string]bool{}
	for _, name := range probe.SubsystemNames() {
		known[name] = true
	}
	for _, l := range All() {
		if l.Kind == KindStack && !known[l.Key] {
			t.Errorf("leaf %q has no rule in probe that classifies it", l.Key)
		}
	}
}
