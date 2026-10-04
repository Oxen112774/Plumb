package probe

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseKernelStackStripsAddressesAndOffsets(t *testing.T) {
	// Both shapes occur: KASLR hides addresses as <0>, older kernels print
	// them in full. Neither the address nor the +offset/size suffix is
	// stable across builds, so only the function name survives.
	data := `[<0>] __schedule+0x2d5/0x890
[<0>] schedule+0x33/0xa0
[<ffffffffa1234567>] io_schedule+0x42/0x70
[<0>] ext4_sync_file
Call Trace:
`
	got := parseKernelStack([]byte(data))
	want := []string{"__schedule", "schedule", "io_schedule", "ext4_sync_file"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("frame[%d] = %q, want %q", i, got[i], want[i])
		}
	}
	// The "Call Trace:" header is not a function a thread waits in.
	for _, f := range got {
		if strings.Contains(f, "Call") || strings.Contains(f, "Trace") {
			t.Errorf("header leaked into frames: %v", got)
		}
	}
}

func TestParseKernelStackOnEmptyAndJunk(t *testing.T) {
	if got := parseKernelStack(nil); len(got) != 0 {
		t.Errorf("nil: got %v", got)
	}
	if got := parseKernelStack([]byte("\n\n  \n")); len(got) != 0 {
		t.Errorf("blank: got %v", got)
	}
	if got := parseKernelStack([]byte("[] \n")); len(got) != 0 {
		t.Errorf("empty frame: got %v", got)
	}
}

func TestClassifyStackNamesTheSubsystem(t *testing.T) {
	cases := []struct {
		name  string
		stack []string
		want  string
	}{
		{
			"journal commit",
			[]string{"__schedule", "io_schedule", "wait_on_page_bit", "jbd2_log_wait_commit", "ext4_sync_file"},
			"ext4/jbd2 日志",
		},
		{
			"nfs",
			[]string{"__schedule", "io_schedule", "nfs_wait_bit_killable", "nfs_do_readpage"},
			"NFS/RPC",
		},
		{
			"cgroup reclaim",
			[]string{"__schedule", "io_schedule", "wait_on_page_bit", "shrink_node_memcg", "mem_cgroup_try_charge"},
			"cgroup 内存回收",
		},
		{
			"block queue",
			[]string{"__schedule", "io_schedule", "blk_mq_get_request", "submit_bio"},
			"块设备队列",
		},
		{
			"fuse",
			[]string{"__schedule", "fuse_file_write_iter", "fuse_simple_request"},
			"FUSE",
		},
		{
			"xfs",
			[]string{"__schedule", "xfs_log_force", "xfs_file_fsync"},
			"XFS",
		},
		{
			"nothing recognised is answered honestly",
			[]string{"__schedule", "some_unknown_thing", "another_unknown"},
			"",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, decided := classifyStack(c.stack)
			if got != c.want {
				t.Errorf("subsystem = %q, want %q (decided by %q)", got, c.want, decided)
			}
			if c.want != "" && decided == "" {
				t.Errorf("subsystem %q has no deciding frame", got)
			}
		})
	}
}

// A stack containing both a file-system frame and a block-layer frame is a
// file-system problem: the file system decided to submit the I/O, and the
// block layer only carried it out. Blaming the queue would send an operator
// to look at storage that is fine, which is the expensive mistake.
func TestClassifyStackPrefersTheSpecificSubsystem(t *testing.T) {
	stack := []string{"__schedule", "io_schedule", "blk_mq_get_request", "submit_bio", "ext4_writepages"}
	got, decided := classifyStack(stack)
	if got != "ext4/jbd2 日志" {
		t.Errorf("subsystem = %q, want ext4/jbd2；decided by %q", got, decided)
	}
}

func TestReadKernelStackDegradesWhenAbsent(t *testing.T) {
	// CONFIG_STACKTRACE is not always on, and the read needs PTRACE_MODE_READ.
	// Both are normal, and neither is an error: the wchan answer stands alone.
	dir := t.TempDir()
	st := ReadKernelStack(NewRoot(dir), "proc/9999/task/9999/stack")
	if st.Available {
		t.Errorf("Available on a missing file: %+v", st)
	}
	if st.Subsystem != "" || st.DecidedBy != "" {
		t.Errorf("classified a missing stack: %+v", st)
	}
}

func TestReadKernelStackFromFile(t *testing.T) {
	dir := t.TempDir()
	rel := "proc/1234/task/1240/stack"
	full := filepath.Join(dir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(full), 0755); err != nil {
		t.Fatal(err)
	}
	body := "[<0>] __schedule+0x2d5/0x890\n[<0>] io_schedule+0x42/0x70\n[<0>] nfs_wait_bit_killable+0x1a/0x60\n"
	if err := os.WriteFile(full, []byte(body), 0644); err != nil {
		t.Fatal(err)
	}

	st := ReadKernelStack(NewRoot(dir), rel)
	if !st.Available {
		t.Fatalf("not available: %+v", st)
	}
	if len(st.Frames) != 3 {
		t.Errorf("frames = %v", st.Frames)
	}
	if st.Subsystem != "NFS/RPC" {
		t.Errorf("subsystem = %q, want NFS/RPC", st.Subsystem)
	}
	if st.DecidedBy != "nfs_wait_bit_killable" {
		t.Errorf("decided by %q", st.DecidedBy)
	}
}

func TestStackSubsystemsOrderedByCount(t *testing.T) {
	threads := []Thread{
		{Stack: KernelStack{Subsystem: "NFS/RPC"}},
		{Stack: KernelStack{Subsystem: "块设备队列"}},
		{Stack: KernelStack{Subsystem: "NFS/RPC"}},
		{Stack: KernelStack{}},
	}
	got := StackSubsystems(threads)
	want := []string{"NFS/RPC", "块设备队列"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestStackSubsystemsIsStableForEqualCounts(t *testing.T) {
	// Two subsystems with the same count must come out in the same order
	// every run, or the report shifts between samples for no reason.
	var got []string
	for i := 0; i < 5; i++ {
		got = StackSubsystems([]Thread{
			{Stack: KernelStack{Subsystem: "XFS"}},
			{Stack: KernelStack{Subsystem: "btrfs"}},
		})
	}
	if len(got) != 2 || got[0] != "XFS" || got[1] != "btrfs" {
		t.Errorf("unstable order: %v", got)
	}
}
