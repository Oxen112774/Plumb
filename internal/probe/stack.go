package probe

import (
	"strings"
)

// KernelStack is the kernel stack of a thread, read from
// /proc/<pid>/task/<tid>/stack.
//
// wchan names only the innermost kernel function a thread sleeps in, which
// is frequently too shallow to act on: "io_schedule" says a thread waits
// for I/O without saying whose I/O. The stack names the entire call chain,
// and somewhere in that chain is the subsystem actually responsible — a
// journal commit waiting on itself, an NFS round trip to a server that is
// not answering, a block-layer queue that is full, a memory reclaim under a
// cgroup limit.
//
// Those four have different remedies, and confusing them costs hours:
// lowering fsync frequency, going to fix the NFS server, raising a memory
// limit, and buying faster storage are not interchangeable. The stack is
// the only read-only interface that separates them.
type KernelStack struct {
	// Available is false when the file could not be read. This is normal:
	// /proc/<pid>/task/<tid>/stack exists only when the kernel was built
	// with CONFIG_STACKTRACE, and reading it needs the same
	// PTRACE_MODE_READ that wchan does. An absent stack must degrade to
	// the wchan answer, never to a guess.
	Available bool `json:"available"`

	// Frames, innermost first, with addresses and offsets stripped. Ten to
	// twenty frames is typical; the list is kept whole because the
	// interesting frame is not always near the top.
	Frames []string `json:"frames,omitempty"`

	// Subsystem is the recognised subsystem the wait belongs to, or "" when
	// none of the frames matched a known subsystem. Empty is a honest
	// answer, not a failure.
	Subsystem string `json:"subsystem,omitempty"`

	// DecidedBy is the frame that produced Subsystem. It exists so a reader
	// can check the classification instead of trusting it — the same rule
	// every other number in plumb follows.
	DecidedBy string `json:"decided_by,omitempty"`
}

// ReadKernelStack reads one thread's kernel stack.
//
// The read is attempted only for threads already known to be in D state.
// That is the same gate wchan uses, for the same two reasons: the file
// needs PTRACE_MODE_READ, so unprivileged runs see it only for their own
// processes, and sweeping every thread on a large machine buys nothing
// because a running thread has no interesting stack to show.
func ReadKernelStack(r Root, rel string) KernelStack {
	data, err := r.ReadFile(rel)
	if err != nil {
		return KernelStack{}
	}
	frames := parseKernelStack(data)
	if len(frames) == 0 {
		return KernelStack{}
	}
	st := KernelStack{Available: true, Frames: frames}
	st.Subsystem, st.DecidedBy = classifyStack(frames)
	return st
}

// parseKernelStack pulls function names out of a kernel stack dump.
//
// The two shapes in the wild are
//
//	[<0>] io_schedule+0x42/0x70
//	[<ffffffffa1234567>] io_schedule+0x42/0x70
//
// depending on whether KASLR hid the addresses. Addresses and the
// "+offset/size" suffix are dropped: neither is stable across builds, and
// neither is what a reader can look up. The function name is.
//
// Lines that do not match are skipped rather than failing the parse. A
// stack dump can carry a trailing "Call Trace:" header, an interrupt
// boundary, or a ret_from_fork tail frame, none of which is a function a
// thread is waiting in.
func parseKernelStack(data []byte) []string {
	var out []string
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		i := strings.Index(line, "] ")
		if i < 0 {
			continue
		}
		rest := strings.TrimSpace(line[i+2:])
		if j := strings.IndexByte(rest, '+'); j >= 0 {
			rest = rest[:j]
		}
		rest = strings.TrimSpace(rest)
		if rest == "" || rest == "0" {
			continue
		}
		out = append(out, rest)
	}
	return out
}

// subsystemRule maps kernel function-name prefixes to the subsystem that
// owns them.
//
// The kernel names its functions by subsystem — jbd2_*, xfs_*, nfs_*,
// mem_cgroup_* — so a prefix match survives version churn in a way that an
// address, an offset, or a fixed frame index never could. That stability is
// the whole reason this classifier is a prefix table and not a symbol
// lookup.
type subsystemRule struct {
	Name     string
	Prefixes []string
}

// subsystemRules is ordered most specific first. A stack that contains both
// a block-layer frame and an ext4 frame is an ext4 problem: the file system
// is what decided to submit the I/O, and the block layer only carried it
// out. Blaming the queue in that case would send the operator to look at
// storage that is fine.
//
// The same reasoning orders the memory rules: a memcg reclaim frame wins
// over a generic reclaim frame because the cgroup limit is the narrower
// fact, and "this container hit its limit" is more actionable than "the
// machine was short of memory".
var subsystemRules = []subsystemRule{
	// --- 文件系统：串行点在这里，不在块设备 ---
	{"ext4/jbd2 日志", []string{"jbd2_", "__jbd2_", "ext4_"}},
	{"XFS", []string{"xfs_"}},
	{"btrfs", []string{"btrfs_"}},
	{"FUSE", []string{"fuse_"}},
	{"NFS/RPC", []string{"nfs_", "rpc_", "sunrpc", "xprt_"}},

	// --- 内存：按"谁做的决定"分，cgroup 窄于整机 ---
	{"cgroup 内存回收", []string{"mem_cgroup_", "try_to_free_mem", "mem_cgroup_wait"}},
	{"内存碎片整理", []string{"compact_compaction", "migrate_pages", "compact_isolated",
		"__compact_pgdat", "kcompactd", "compact_"}},
	{"khugepaged", []string{"khugepaged", "collapse_huge_page", "khugepaged_scan_mm_slot"}},
	{"直接内存回收", []string{"try_to_free_pages", "__alloc_pages_direct_reclaim",
		"do_try_to_free_pages", "shrink_all_memory", "lru_gen", "shrink_node",
		"shrink_folio_list", "balance_pgdat"}},
	{"分配器页等待", []string{"__alloc_pages", "alloc_pages_vma", "__page_cache_alloc",
		"pagealloc", "wait_on_page_bit_common"}},

	// --- 网络：发送缓冲满是最容易被误判成"卡住"的一类 ---
	{"TCP 发送缓冲满", []string{"sk_stream_wait_memory", "tcp_sendmsg", "wait_sock",
		"inet_csk_accept", "tcp_connect", "__inet_stream_connect"}},
	{"socket 接收等待", []string{"tcp_recvmsg", "skb_copy_datagram_msg", "wait_for_more_data",
		"unix_stream_read", "sock_recvmsg"}},

	// --- 事件循环：不是问题，是正常的空转等待 ---
	{"epoll/select 等待", []string{"ep_poll", "do_epoll_wait", "do_select", "do_poll",
		"poll_schedule_timeout", "sys_poll"}},
	{"io_uring 等待", []string{"io_uring_enter", "io_cqring_wait", "io_uring_get_sqe",
		"io_submit_sqes"}},

	// --- 块设备与回写 ---
	{"页回写", []string{"wait_on_page", "filemap_fdatawait", "writeback_", "wb_wait",
		"balance_dirty_pages", "wait_for_completion_io"}},
	{"块设备队列", []string{"blk_mq_", "submit_bio", "blk_", "__blockdev",
		"wait_on_page_bit"}},
	{"块设备超时重试", []string{"io_wait", "blk_execute_rq", "scsi_", "nvme_",
		"md_flush_request", "submit_bio_wait"}},

	// --- 锁与同步 ---
	{"内核锁", []string{"rwsem_down", "down_read", "down_write", "__mutex", "mutex_lock",
		"ww_sem", "rt_spin_lock", "spin_lock"}},
	{"futex", []string{"futex_wait", "do_futex"}},

	// --- RCU 与调度：延迟尖峰的常见来源 ---
	// 注意：__schedule 和 set_current_state 不在这里。两者出现在**每一个**
	// 阻塞栈里 —— 任何线程进入睡眠都要过它们，所以命中它们等于什么都没说。
	// 判"调度器"要看到 schedule_timeout 这类**主动带超时睡眠**的帧，
	// 那才说明线程是自己选择睡的，而不是被某个资源挡住。
	{"RCU 等待", []string{"rcu_", "synchronize_rcu", "rcu_barrier", "krcu_"}},
	{"调度器", []string{"schedule_timeout", "schedule_hrtimeout", "sched_",
		"io_schedule_timeout", "prepare_to_wait"}},

	// --- 进程自身 ---
	{"等待子进程", []string{"do_wait", "kernel_wait", "wait_task_zombie", "__wait4"}},
}

// SubsystemNames returns every subsystem the classifier can produce.
//
// It exists so the two tables that have to agree with this one — the remedy
// table in diagnose and the knowledge-tree leaves — can be checked against it
// in a test. Without it a rule added here silently leaves a subsystem that
// plumb can name but cannot explain: it prints "卡在 X" with no remedy, and
// no knowledge leaf to look it up in.
func SubsystemNames() []string {
	var out []string
	for _, r := range subsystemRules {
		out = append(out, r.Name)
	}
	return out
}

// StackFromFrames builds a classified KernelStack from frame names, the same
// way ReadKernelStack does after parsing a dump. It is exported because
// "a thread waiting on a recognised subsystem" is a fact other packages need
// to construct — notably the report layer's own tests, which must not need a
// real /proc to prove that the classification reaches the screen.
func StackFromFrames(frames []string) KernelStack {
	if len(frames) == 0 {
		return KernelStack{}
	}
	st := KernelStack{Available: true, Frames: frames}
	st.Subsystem, st.DecidedBy = classifyStack(frames)
	return st
}

// classifyStack returns the subsystem a stack belongs to, and the frame that
// decided it. Both are empty when nothing matched.
func classifyStack(frames []string) (subsystem, decidedBy string) {
	for _, rule := range subsystemRules {
		for _, f := range frames {
			for _, p := range rule.Prefixes {
				if strings.HasPrefix(f, p) {
					return rule.Name, f
				}
			}
		}
	}
	return "", ""
}

// StackSubsystems returns the distinct subsystems a set of blocked threads
// is waiting on, most common first. It is how a single finding can say
// "half your stuck threads are on NFS" instead of listing twenty stacks.
func StackSubsystems(threads []Thread) []string {
	var out []string
	count := map[string]int{}
	for _, th := range threads {
		if th.Stack.Subsystem != "" {
			count[th.Stack.Subsystem]++
		}
	}
	for name := range count {
		out = append(out, name)
	}
	// Stable ordering: by count descending, then by name so equal counts do
	// not shuffle between runs.
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && (count[out[j]] > count[out[j-1]] ||
			(count[out[j]] == count[out[j-1]] && out[j] < out[j-1])); j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out
}
