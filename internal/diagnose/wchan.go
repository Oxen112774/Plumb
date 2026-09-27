package diagnose

import "strings"

// WaitKind groups the reasons a thread can be stuck.
type WaitKind int

const (
	// WaitUnknown is a kernel symbol this build does not recognise. It is
	// reported as unknown rather than guessed at: a wrong explanation is worse
	// than an absent one when someone is about to act on it.
	WaitUnknown WaitKind = iota
	WaitDiskIO
	WaitFlush
	WaitWriteback
	WaitReclaim
	WaitLock
	WaitNetwork
	WaitNormal
)

// Wait is a translated kernel wait, with the raw symbol kept for verification.
type Wait struct {
	Kind   WaitKind
	Short  string // one line, for the report
	Advice string // what to do about it, when it is a problem
	Symbol string // the raw wchan value
}

// waitEntry pairs a kernel function prefix with its meaning.
type waitEntry struct {
	prefix string
	wait   Wait
}

// waitTable maps a kernel function prefix to its meaning.
//
// The order matters: the first prefix that matches wins, so entries that
// describe a specific caller are listed before the generic routines they call
// into. io_schedule in particular sits at the bottom of almost every block I/O
// stack, so anything that explains *why* we got there has to be tested first.
var waitTable = []waitEntry{
	// --- dirty-page throttling: the most actionable wait there is ---
	{"balance_dirty_pages", Wait{
		Kind:   WaitWriteback,
		Short:  "被内核按住，等脏页刷完",
		Advice: "这个进程写盘的速度超过了内核刷盘的速度，内核只能把它按停。这不是磁盘坏了，是刷盘参数和写入速率不匹配：把 vm.dirty_background_ratio 调小，让后台刷盘更早开始，或者降低写入速率。反过来把 vm.dirty_ratio 调大会让积压更严重，别动它。"}},

	// --- file system journal commit ---
	{"jbd2_log_wait_commit", Wait{
		Kind:   WaitFlush,
		Short:  "等 ext4 日志提交",
		Advice: "ext4 的日志必须先落盘，之后的写才算数。日志提交慢通常是小文件同步写太多：把 fsync 改成批量提交，或把日志放到更快的盘上。"}},
	{"jbd2_journal_commit_transaction", Wait{
		Kind:   WaitFlush,
		Short:  "正在提交 ext4 日志",
		Advice: "日志提交是同步瓶颈。降低 fsync 频率是唯一有效的办法。"}},
	{"xfs_log_force", Wait{
		Kind:   WaitFlush,
		Short:  "等 XFS 日志落盘",
		Advice: "XFS 被要求强制把日志落盘。查是不是有大事务或过于频繁的 fsync。"}},
	{"ext4_sync_file", Wait{
		Kind:   WaitFlush,
		Short:  "在等 fsync 落盘",
		Advice: "应用在 fsync。数据库的 redo log、消息队列的持久化都堵在这里。如果量是正常业务量，问题在盘的写延迟；如果不是，去看应用为什么这么频繁地 fsync。"}},
	{"do_fsync", Wait{
		Kind:   WaitFlush,
		Short:  "在等 fsync 落盘",
		Advice: "同 ext4_sync_file。"}},
	{"vfs_fsync", Wait{
		Kind:   WaitFlush,
		Short:  "在等 fsync 落盘",
		Advice: "同 ext4_sync_file。"}},

	// --- writeback of specific pages ---
	{"wait_on_page_writeback", Wait{
		Kind:   WaitWriteback,
		Short:  "等某一页写完",
		Advice: "内核正在把这一页刷出去，线程在等它完成。"}},
	{"folio_wait_writeback", Wait{
		Kind:   WaitWriteback,
		Short:  "等某一页写完",
		Advice: "同 wait_on_page_writeback。"}},
	{"wait_on_page_bit", Wait{
		Kind:   WaitWriteback,
		Short:  "等某一页的就绪状态",
		Advice: "通常是等脏页刷完。"}},
	{"folio_wait_bit", Wait{
		Kind:   WaitWriteback,
		Short:  "等某一页的就绪状态",
		Advice: "通常是等脏页刷完。"}},

	// --- direct I/O and the block layer ---
	{"do_blockdev_direct_IO", Wait{
		Kind:   WaitDiskIO,
		Short:  "在做直接 IO（绕过缓存）",
		Advice: "直接 IO 写路径短，延迟完全取决于盘本身，没有页缓存可以缓冲。"}},
	{"blk_mq", Wait{
		Kind:   WaitDiskIO,
		Short:  "等磁盘队列空位",
		Advice: "磁盘队列满了 —— 这是磁盘过载最直接的证据。降低并发，或换更快的盘。"}},
	{"blkdev", Wait{
		Kind:   WaitDiskIO,
		Short:  "等块设备完成",
		Advice: "IO 已经交给块设备层，在等硬件回话。看盘的 util 和 await。"}},
	{"submit_bio", Wait{
		Kind:   WaitDiskIO,
		Short:  "在提交 IO 请求",
		Advice: "正在往块设备层压 IO。"}},
	{"nvme_", Wait{
		Kind:   WaitDiskIO,
		Short:  "等 NVMe 完成",
		Advice: "NVMe 盘在忙。"}},
	{"scsi_", Wait{
		Kind:   WaitDiskIO,
		Short:  "等 SCSI 层完成",
		Advice: "存储栈在忙。"}},

	// --- memory reclaim ---
	{"try_to_free_pages", Wait{
		Kind:   WaitReclaim,
		Short:  "直接内存回收",
		Advice: "分配内存时发现不够，被迫自己动手回收。这条路径上的停顿是毫秒级的，会直接变成应用延迟。加内存，或限制进程的工作集。"}},
	{"shrink_", Wait{
		Kind:   WaitReclaim,
		Short:  "在回收内存",
		Advice: "内存紧张，内核在回收页。"}},
	{"__alloc_pages", Wait{
		Kind:   WaitReclaim,
		Short:  "等内存分配",
		Advice: "分配失败或被迫进入回收。"}},
	{"direct_reclaim", Wait{
		Kind:   WaitReclaim,
		Short:  "直接内存回收",
		Advice: "同 try_to_free_pages。"}},

	// --- page faults pulling data back from storage ---
	{"filemap_fault", Wait{
		Kind:   WaitDiskIO,
		Short:  "缺页，要从磁盘读回来",
		Advice: "内存里的文件页已经被换出去，现在要读回来。这是内存不够的直接证据 —— 表现为随机的读延迟尖刺。"}},

	// io_schedule sits at the bottom of nearly every block-I/O stack, so it
	// has to be tested last among the disk waits: every entry above this line
	// calls into it, and matching it first would collapse all of them into
	// "waiting for the disk" and lose the reason.
	{"io_schedule", Wait{
		Kind:  WaitDiskIO,
		Short: "等磁盘 IO 完成",
		Advice: "线程已经进了块设备层，在等 IO 返回。这是磁盘过载的直接证据：" +
			"看盘的 util 和 await（iostat -x 1），再决定是降低并发、把 IO 摊开，还是换盘。"}},

	// --- kernel locks ---
	{"rwsem_down_write_slowpath", Wait{
		Kind:   WaitLock,
		Short:  "等内核写锁",
		Advice: "有别的线程持着这把锁不放。查是谁。"}},
	{"rwsem_down_read_slowpath", Wait{
		Kind:   WaitLock,
		Short:  "等内核读锁",
		Advice: "写锁被持有的时间过长。"}},
	{"__mutex_lock", Wait{
		Kind:   WaitLock,
		Short:  "等内核互斥锁",
		Advice: "内核里的锁竞争。"}},
	{"mutex_lock", Wait{
		Kind:   WaitLock,
		Short:  "等内核互斥锁",
		Advice: "内核里的锁竞争。"}},
	{"down_write", Wait{
		Kind:   WaitLock,
		Short:  "等内核写锁",
		Advice: "锁竞争。"}},
	{"down_read", Wait{
		Kind:   WaitLock,
		Short:  "等内核读锁",
		Advice: "锁竞争。"}},
	{"_raw_spin_lock", Wait{
		Kind:   WaitLock,
		Short:  "自旋等锁",
		Advice: "锁竞争。"}},

	// --- userspace locks: the application locking itself ---
	{"futex_wait", Wait{
		Kind:   WaitLock,
		Short:  "等应用自己的锁",
		Advice: "futex 是用户态锁。这是应用内部的线程竞争（连接池、缓存锁、数据库内部闩锁），不是内核的问题 —— 该去改应用。"}},
	{"do_futex", Wait{
		Kind:   WaitLock,
		Short:  "等应用自己的锁",
		Advice: "同 futex_wait。"}},

	// --- network ---
	{"sk_stream_wait_memory", Wait{
		Kind:   WaitNetwork,
		Short:  "网络发送缓冲满，等对端消费",
		Advice: "TCP 发送窗口满了 —— 对端读得太慢，或者中间链路拥塞。这可能根本不在你这一端。"}},
	{"tcp_sendmsg", Wait{
		Kind:   WaitNetwork,
		Short:  "等网络发送",
		Advice: "发送路径拥塞。"}},
	{"tcp_recvmsg", Wait{
		Kind:   WaitNetwork,
		Short:  "等网络接收",
		Advice: "在等数据到达。"}},
	{"__skb_wait_for_more_packets", Wait{
		Kind:   WaitNetwork,
		Short:  "等更多网络包",
		Advice: "套接字读缓冲空了。"}},
	{"unix_stream_read_generic", Wait{
		Kind:   WaitNormal,
		Short:  "等 Unix socket 数据",
		Advice: "进程间通信在等对端，通常是正常的。"}},

	// --- normal, non-problematic waiting ---
	{"ep_poll", Wait{
		Kind: WaitNormal, Short: "等事件（epoll）", Advice: "事件循环在等下一个事件，这是健康状态。"}},
	{"do_epoll_wait", Wait{
		Kind: WaitNormal, Short: "等事件（epoll）", Advice: "事件循环在等下一个事件。"}},
	{"poll_schedule_timeout", Wait{
		Kind: WaitNormal, Short: "等事件（poll）", Advice: "在等文件描述符就绪。"}},
	{"nanosleep", Wait{
		Kind: WaitNormal, Short: "在按计划休眠", Advice: "主动 sleep，正常。"}},
	{"hrtimer_nanosleep", Wait{
		Kind: WaitNormal, Short: "在按计划休眠", Advice: "主动 sleep，正常。"}},
	{"schedule_hrtimeout", Wait{
		Kind: WaitNormal, Short: "在按计划休眠", Advice: "主动 sleep，正常。"}},
	{"do_wait", Wait{
		Kind: WaitNormal, Short: "等子进程退出", Advice: "父进程在等子进程，正常。"}},
	{"wait4", Wait{
		Kind: WaitNormal, Short: "等子进程退出", Advice: "父进程在等子进程，正常。"}},
	{"pipe_read", Wait{
		Kind: WaitNormal, Short: "等管道数据", Advice: "正常。"}},
	{"pipe_write", Wait{
		Kind: WaitNormal, Short: "管道写满，等对端读", Advice: "管道对端没有及时把数据读走。"}},
	{"sk_wait_data", Wait{
		Kind: WaitNormal, Short: "等套接字数据", Advice: "正常。"}},
	{"inet_csk_accept", Wait{
		Kind: WaitNormal, Short: "等新连接", Advice: "服务端在 accept，正常。"}},
	{"msleep", Wait{
		Kind: WaitNormal, Short: "内核按计划休眠", Advice: "通常是驱动在轮询，正常。"}},
}

// TranslateWChan turns a kernel symbol from wchan into something an operator
// can act on, keeping the raw symbol so the translation can be checked.
func TranslateWChan(symbol string) Wait {
	s := strings.TrimSpace(symbol)
	if s == "" {
		return Wait{Kind: WaitUnknown, Short: "正在运行，或无法读取等待点"}
	}
	for _, e := range waitTable {
		if strings.HasPrefix(s, e.prefix) {
			w := e.wait
			w.Symbol = s
			return w
		}
	}
	return Wait{
		Kind:  WaitUnknown,
		Short: "停在 " + s + "（未收录）",
		Advice: "这个等待点不在 plumb 的翻译表里。它仍然是一条确凿的线索：" +
			"把 " + s + " 这个函数名拿去查，就能知道内核在这一刻等的是什么。",
		Symbol: s,
	}
}

// Blocking reports whether this kind of wait means the thread is genuinely
// stuck, as opposed to parked in a healthy event loop.
//
// The distinction matters because D state alone is not proof of a problem: a
// driver may use uninterruptible sleep for a routine poll. What makes a thread
// worth reporting is that it cannot be interrupted *and* is waiting on
// storage or memory.
func (w Wait) Blocking() bool {
	switch w.Kind {
	case WaitDiskIO, WaitFlush, WaitWriteback, WaitReclaim:
		return true
	default:
		return false
	}
}

// Label is the short human name for a wait kind, used as a grouping key.
func (w Wait) Label() string {
	switch w.Kind {
	case WaitDiskIO:
		return "等磁盘"
	case WaitFlush:
		return "等刷盘"
	case WaitWriteback:
		return "等写回"
	case WaitReclaim:
		return "等内存回收"
	case WaitLock:
		return "等锁"
	case WaitNetwork:
		return "等网络"
	case WaitNormal:
		return "正常等待"
	default:
		return "原因未知"
	}
}
