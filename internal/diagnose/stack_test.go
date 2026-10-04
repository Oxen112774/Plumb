package diagnose

import (
	"strings"
	"testing"

	"github.com/Oxen112774/plumb/internal/i18n"
	"github.com/Oxen112774/plumb/internal/probe"
)

// This file tests the part of the stack work that is dangerous rather than
// interesting: a subsystem plumb can name but cannot explain.
//
// The classifier lives in probe, the remedies live here, and the prose lives
// in the knowledge tree. Three tables, one list of names. If they drift apart
// nothing fails — plumb prints "卡在 NFS/RPC" with no advice, or a leaf exists
// that the classifier can never produce. These tests are what makes drift a
// build error.

// Every subsystem the classifier can produce must have a remedy here.
//
// The failure mode this guards against is silent and expensive: plumb says
// "卡在 btrfs" and then has nothing to tell the operator to do. That is worse
// than not saying it at all, because a named problem invites action.
func TestEveryClassifiedSubsystemHasARemedy(t *testing.T) {
	for _, name := range probe.SubsystemNames() {
		sub := TranslateSubsystem(name, i18n.ZH)
		if sub.Short == "" {
			t.Errorf("classifier can produce %q but it has no Short line", name)
		}
		if sub.Advice == "" {
			t.Errorf("classifier can produce %q but it has no advice", name)
		}
	}
}

// And no remedies for subsystems the classifier cannot produce.
//
// A dead entry is not harmful the way a missing one is, but it is a lie about
// coverage: the tree claims a chapter it can never reach.
func TestNoRemedyForAnUnclassifiableSubsystem(t *testing.T) {
	known := map[string]bool{}
	for _, name := range probe.SubsystemNames() {
		known[name] = true
	}
	for name := range subsystemTable {
		if !known[name] {
			t.Errorf("remedy table has %q but no rule in probe classifies it", name)
		}
	}
}

// An unrecognised subsystem must come back empty, not as a plausible
// neighbour. Inventing a remedy for something plumb did not recognise is the
// one mistake this table must never make.
func TestUnknownSubsystemIsNotGuessed(t *testing.T) {
	if got := TranslateSubsystem("not_a_subsystem", i18n.ZH); got != (Subsystem{}) {
		t.Errorf("an unknown subsystem returned %+v, want the zero value", got)
	}
	// A near-miss must also miss: these are names in the table, so a
	// substring match here would be a guess dressed up as a lookup.
	for _, near := range []string{"NFS", "XFS 日志", "ext4", "futex_"} {
		if near == "XFS" || near == "ext4/jbd2 日志" || near == "futex" {
			t.Errorf("%q is an exact key now; pick a different near-miss", near)
			continue
		}
		if got := TranslateSubsystem(near, i18n.ZH); got != (Subsystem{}) {
			t.Errorf("near-miss %q resolved to %+v; lookup must be exact", near, got)
		}
	}
}

// The "this machine is fine" marker is the single most valuable line plumb
// can print, so it must be driven by the table's Local field rather than
// being spelled out per call site — a new remote filesystem gets it for free.
func TestRemoteSubsystemsAreMarkedNotThisMachine(t *testing.T) {
	remote := 0
	for _, name := range probe.SubsystemNames() {
		sub := TranslateSubsystem(name, i18n.ZH)
		if sub.Local {
			continue
		}
		remote++
		a := &analysis{}
		a.cur.Threads.Processes = []probe.ProcessThreads{{
			PID: 1,
			BlockedOn: []probe.Thread{{
				TID:   1,
				Stack: probe.KernelStack{Available: true, Subsystem: name},
			}},
		}}
		line := a.BlockedSubsystemLine()
		if !strings.Contains(line, "不在本机") {
			t.Errorf("%s: Local=false but the line does not say the wait is elsewhere:\n%s",
				name, line)
		}
		if !strings.Contains(line, sub.Short) {
			t.Errorf("%s: the line omits the subsystem's own description:\n%s", name, line)
		}
	}
	if remote == 0 {
		t.Fatal("no remote subsystem in the table; the marker is never exercised")
	}
}

// Most-common-first ordering, because the finding quotes only the first
// subsystem's remedy. If the order were arbitrary, one run would advise
// tuning fsync and the next would advise fixing the NFS server.
func TestBlockedSubsystemsAreOrderedByFrequency(t *testing.T) {
	a := &analysis{}
	a.cur.Threads.Processes = []probe.ProcessThreads{{
		PID: 1,
		BlockedOn: []probe.Thread{
			{TID: 1, Stack: probe.KernelStack{Subsystem: "块设备队列"}},
			{TID: 2, Stack: probe.KernelStack{Subsystem: "NFS/RPC"}},
			{TID: 3, Stack: probe.KernelStack{Subsystem: "NFS/RPC"}},
			// An unclassified stack contributes nothing rather than
			// showing up as an empty subsystem.
			{TID: 4, Stack: probe.KernelStack{Available: true, Frames: []string{"unknown_fn"}}},
		},
	}}
	got := a.blockedSubsystems()
	if len(got) != 2 {
		t.Fatalf("got %d subsystems %v, want 2", len(got), got)
	}
	if got[0] != "NFS/RPC" {
		t.Errorf("first subsystem is %q; the remedy is taken from it, so it must be the commonest", got[0])
	}
}

// With no stacks at all there is nothing to say, and the line must be empty
// rather than a sentence that says nothing. The whole feature degrades to the
// wchan answer in that case, which is tested in blocked_test.go.
func TestBlockedSubsystemLineIsEmptyWithoutStacks(t *testing.T) {
	a := &analysis{}
	if got := a.BlockedSubsystemLine(); got != "" {
		t.Errorf("with no blocked threads the line is %q, want empty", got)
	}
	a.cur.Threads.Processes = []probe.ProcessThreads{{
		PID:       1,
		BlockedOn: []probe.Thread{{TID: 1, WChan: "io_schedule"}},
	}}
	if got := a.BlockedSubsystemLine(); got != "" {
		t.Errorf("a thread with wchan but no stack produced %q, want empty", got)
	}
}
