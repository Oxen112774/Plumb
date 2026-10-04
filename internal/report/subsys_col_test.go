package report

import (
	"strings"
	"testing"

	"github.com/Oxen112774/plumb/internal/diagnose"
	"github.com/Oxen112774/plumb/internal/i18n"
	"github.com/Oxen112774/plumb/internal/probe"
)

// The kernel stack is plumb's differentiating read, and until the SUBSYS
// column existed it was collected, classified, and then thrown away by the
// terminal renderer — visible only in -json. These tests pin it to the table.

// Two threads whose wchan looks alike, waiting on different things. This is
// the case the column exists for: a reader who stopped at WCHAN would lump
// them together, and one of them is not on this machine at all.
func repWithStacks(t *testing.T) diagnose.Report {
	t.Helper()
	stack := func(frames ...string) probe.KernelStack {
		return probe.StackFromFrames(frames)
	}
	return diagnose.Report{
		Threads: probe.ThreadScan{
			Total:   3,
			Blocked: 2,
			Processes: []probe.ProcessThreads{
				{PID: 1234, Name: "mysqld", Total: 2, Blocked: 2, BlockedOn: []probe.Thread{
					{TID: 1240, Comm: "mysqld", State: "D", WChan: "jbd2_log_wait_commit",
						Stack: stack("__schedule", "io_schedule", "jbd2_log_wait_commit",
							"ext4_sync_file", "vfs_fsync_range")},
					{TID: 1241, Comm: "mysqld", State: "D", WChan: "io_schedule",
						Stack: stack("__schedule", "io_schedule", "nfs_wait_bit_killable",
							"nfs_do_readpage", "filemap_fault")},
				}},
			},
		},
	}
}

func TestThreadTableNamesTheSubsystem(t *testing.T) {
	out := renderAt(t, repWithStacks(t), i18n.ZH, 200)

	if !strings.Contains(out, "内核栈判定") {
		t.Errorf("no subsystem column header:\n%s", out)
	}
	if !strings.Contains(out, "ext4/jbd2") {
		t.Errorf("ext4 stack not classified in the table:\n%s", out)
	}
	// The point of the column: the deciding frame is shown, so the
	// classification is a reading rather than a claim.
	if !strings.Contains(out, "← jbd2_log_wait_commit") {
		t.Errorf("table does not name the deciding frame:\n%s", out)
	}
}

// A subsystem the classifier could not name must leave the cell empty rather
// than invent one. A blank is honest; a guess is not.
func TestThreadTableLeavesUnknownSubsystemBlank(t *testing.T) {
	rep := repWithStacks(t)
	rep.Threads.Processes[0].BlockedOn[0].Stack = probe.KernelStack{
		Available: true, Frames: []string{"__schedule", "schedule"},
	}
	out := renderAt(t, rep, i18n.ZH, 200)

	if !strings.Contains(out, "jbd2_log_wait_commit") {
		t.Errorf("row disappeared:\n%s", out)
	}
	// The header is there, the unknown stack contributes no name.
	if strings.Contains(out, "←") && strings.Count(out, "←") > 1 {
		t.Errorf("an unclassified stack was given a name:\n%s", out)
	}
}

// The English report must not mix a Chinese subsystem name into an otherwise
// English table.
func TestThreadTableSubsystemInEnglish(t *testing.T) {
	out := renderAt(t, repWithStacks(t), i18n.EN, 200)

	if !strings.Contains(out, "stack says") {
		t.Errorf("no English column header:\n%s", out)
	}
	if !strings.Contains(out, "ext4/jbd2 journal") {
		t.Errorf("English subsystem name missing:\n%s", out)
	}
	for _, r := range out {
		if r > 0x2E80 && r < 0xFFFD {
			t.Fatalf("CJK character %q in English report:\n%s", r, out)
		}
	}
}
