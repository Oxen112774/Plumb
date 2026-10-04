package diagnose

import (
	"strings"

	"github.com/Oxen112774/plumb/internal/i18n"
	"github.com/Oxen112774/plumb/internal/probe"
)

// Subsystem names a kernel subsystem a blocked thread is waiting on, with
// the remedy that goes with it.
//
// This table exists because the same-looking symptom has incompatible
// remedies. "Threads stuck in D state" could be a journal commit, an absent
// NFS server, a tight cgroup limit, or saturated storage. Tuning fsync
// frequency does nothing for an NFS server that is down, and raising a
// memory limit does nothing for a full disk queue. Sending an operator at
// the wrong one of these costs hours, and the failure mode is silent: they
// try the plausible fix, it does not work, and the blame moves to plumb.
//
// The subsystem comes from the kernel stack (probe.KernelStack.Subsystem),
// which is the only read-only interface that separates these cases. wchan
// cannot: it names the innermost frame, which is usually io_schedule, and
// io_schedule is what all four of them bottom out in.
type Subsystem struct {
	// Short is one line for the report, naming what is actually waiting.
	Short string

	// Advice is what to do about it. Written to be acted on at 3am, which
	// means it says what NOT to do as well — the wrong remedy is the
	// expensive mistake here, not the missing one.
	Advice string

	// Local is false when the wait is on something this machine does not
	// own. Used to say plainly "this machine is fine", which is the single
	// most useful thing plumb can tell someone about to restart a server.
	Local bool

	// ShortEN / AdviceEN are the English renderings, kept next to the
	// Chinese so an edit to one without the other shows up in the diff.
	ShortEN  string
	AdviceEN string

	// NameEN is the subsystem's name in English. The map key is the Chinese
	// name, and it is what probe puts in KernelStack.Subsystem — so without
	// this the English report names a subsystem in a language the rest of
	// the line is not in.
	NameEN string
}

// In returns the subsystem rendered in lang, falling back to Chinese where an
// English rendering is missing. A blank remedy is worse than a foreign one.
func (s Subsystem) In(lang i18n.Lang) Subsystem {
	if lang == i18n.EN {
		if s.ShortEN != "" {
			s.Short = s.ShortEN
		}
		if s.AdviceEN != "" {
			s.Advice = s.AdviceEN
		}
	}
	return s
}

// subsystemTable keyed by probe.KernelStack.Subsystem.
var subsystemTable = map[string]Subsystem{
	"ext4/jbd2 日志": {
		NameEN: "ext4/jbd2 journal",
		Short:  "卡在 ext4 的日志提交（jbd2）：文件系统在等它自己的事务落盘",
		Advice: "这是文件系统自己的账本在等，不是磁盘硬件慢。先看 fsync 频率：" +
			"数据库和日志服务每次提交都会触发一次日志提交，频率过高会把队列压满。" +
			"如果提交频率正常，检查日志设备是不是和数据在同一个盘上 —— 独立盘放日志是最常见的结构性修复。" +
			"不要去换更快的盘：日志提交的瓶颈是事务串行，不是带宽。",
		Local: true,
		ShortEN: "stuck in the ext4 journal commit (jbd2): the file system is waiting on " +
			"its own transaction",
		AdviceEN: "This is the file system's own ledger waiting, not slow hardware. Look at " +
			"fsync frequency first: every commit by a database or a log service triggers a " +
			"journal commit, and a high enough rate saturates the queue. If the rate is " +
			"normal, check whether the journal shares a device with the data — putting the " +
			"journal on its own device is the common structural fix. Do not buy a faster " +
			"disk: the bottleneck here is that commits are serial, not bandwidth.",
	},
	"XFS": {
		NameEN: "XFS",
		Short:  "卡在 XFS 的内部等待：通常是日志或元数据操作",
		Advice: "XFS 的元数据操作是串行的，一次大量小文件创建/删除就能把整机的写堵住。" +
			"看是不是有批量删除、目录遍历或配额检查在跑。" +
			"xfs_info 看日志大小，日志太小会让提交更频繁；" +
			"如果同时有大量随机写，考虑把日志挪到独立设备。",
		Local:   true,
		ShortEN: "stuck inside XFS: usually the journal or a metadata operation",
		AdviceEN: "XFS metadata operations are serial, so one bulk create or delete of small " +
			"files can stall every write on the machine. Check for a bulk delete, a " +
			"directory walk, or a quota check in progress. xfs_info shows the journal size; " +
			"a small journal means more frequent commits. With heavy random writes at the " +
			"same time, consider moving the journal to its own device.",
	},
	"btrfs": {
		NameEN: "btrfs",
		Short:  "卡在 btrfs 的内部等待：写时复制或元数据提交",
		Advice: "btrfs 的写时复制让单次写可能触发整棵树的复制，" +
			"大量小随机写会放大成远多于预期的 IO。先确认是不是有快照在自动创建：" +
			"快照会让写放大持续恶化直到快照被清理。" +
			"btrfs filesystem usage 看实际分配，接近满时 btrfs 会变得非常慢。",
		Local:   true,
		ShortEN: "stuck inside btrfs: copy-on-write or a metadata commit",
		AdviceEN: "Copy-on-write means one small write can trigger a copy of a whole tree, so " +
			"many small random writes amplify into far more I/O than expected. Check first " +
			"whether snapshots are being created automatically: write amplification keeps " +
			"worsening until the snapshots are cleaned up. btrfs filesystem usage shows real " +
			"allocation — btrfs gets very slow as it approaches full.",
	},
	"FUSE": {
		NameEN: "FUSE",
		Short:  "卡在 FUSE：一个用户态文件系统（s3fs / sshfs / goofys 之类）没有响应",
		Advice: "这不是磁盘问题，是用户态进程没有回答。FUSE 的请求要走到用户态守护进程再回来，" +
			"那个进程慢、卡住或者已经死了，内核这边就只能一直等。" +
			"先确认那个守护进程还活着 —— 它通常不在 ps 的输出里显得异常，但它的 CPU 会是 0。" +
			"注意：FUSE 挂载在 D 状态下无法 umount -f 之外的任何方式摆脱，" +
			"重启这台机器往往是唯一出路，但请先看守护进程。",
		Local:   false,
		ShortEN: "stuck in FUSE: a userspace file system (s3fs, sshfs, goofys, …) is not answering",
		AdviceEN: "This is not a disk problem, it is a userspace process not answering. A FUSE " +
			"request has to travel out to the daemon and back; if that daemon is slow, stuck, " +
			"or dead, the kernel simply waits. Confirm the daemon is alive first — it rarely " +
			"looks wrong in ps, but its CPU will be 0. Note that a FUSE mount in D state " +
			"cannot be escaped by anything short of umount -f, and rebooting this machine is " +
			"often the only way out — but look at the daemon first.",
	},
	"NFS/RPC": {
		NameEN: "NFS/RPC",
		Short:  "卡在 NFS：在等一个网络文件系统的服务器回应",
		Advice: "这台机器没有问题 —— 它在等远端。去看 NFS 服务器和中间的网络：" +
			"服务器是不是挂了、网络是不是断了、端口映射是不是变了。" +
			"本机上能做的只有 umount -f -l 把挂载点摘掉；" +
			"但先确认这确实是 NFS 而不是本地磁盘 —— 这个判断错了就是白折腾一次。" +
			"重启本机通常也会卡在关机流程里，等到 IO 返回才继续。",
		Local:   false,
		ShortEN: "stuck in NFS: waiting on a network file system's server",
		AdviceEN: "There is nothing wrong with this machine — it is waiting on a remote one. " +
			"Go and look at the NFS server and the network between: is the server down, is " +
			"the path broken, did the port mapping change. Locally the only thing available " +
			"is umount -f -l to detach the mount; but confirm this really is NFS and not a " +
			"local disk first — getting that wrong wastes the whole trip. Rebooting this " +
			"machine usually stalls in shutdown too, waiting for the I/O to come back.",
	},
	"cgroup 内存回收": {
		NameEN: "cgroup memory reclaim",
		Short:  "卡在 cgroup 的内存回收：容器限额太小，内核在限额内反复回收",
		Advice: "进程不是内存泄漏，是限额给小了。内核在这个 cgroup 的限额内做直接回收，" +
			"回收期间所有分配都要等，表现为整机随机卡顿。" +
			"调大这个容器的内存 limit 是最直接的修复；" +
			"如果能确认工作集大小，把 limit 设成工作集的 1.5 倍左右。" +
			"不要去查应用的内存泄漏 —— 是配额问题，不是代码问题。",
		Local: true,
		ShortEN: "stuck in cgroup memory reclaim: the container limit is too small and the " +
			"kernel keeps reclaiming inside it",
		AdviceEN: "The process is not leaking; the limit is too small. The kernel reclaims " +
			"directly inside this cgroup's limit, and every allocation waits while it does — " +
			"which looks like random stuttering across the whole machine. Raising the " +
			"container's memory limit is the direct fix; if you can measure the working set, " +
			"set the limit to roughly 1.5× it. Do not go hunting for a leak in the " +
			"application — this is a quota problem, not a code problem.",
	},
	"页回写": {
		NameEN: "page writeback",
		Short:  "卡在页回写：在等已经发出去的脏页写回完成",
		Advice: "脏页已经交给块层了，但回写没有完成。看 vm.dirty_ratio 和" +
			"vm.dirty_background_ratio：后台刷盘开始得太晚，前台就会被迫同步等。" +
			"把 dirty_background_ratio 调小让后台更早开始，把 dirty_ratio 调小" +
			"让前台等待更早触发而不是一次性积压。" +
			"同时看是不是有写入突增 —— 参数只是让刷盘更平滑，改变不了总量。",
		Local:   true,
		ShortEN: "stuck in page writeback: waiting for dirty pages already submitted",
		AdviceEN: "The dirty pages have reached the block layer but have not landed. Look at " +
			"vm.dirty_ratio and vm.dirty_background_ratio: if background flushing starts too " +
			"late, the foreground is forced to wait synchronously. Lower " +
			"dirty_background_ratio so background work starts sooner, and lower dirty_ratio " +
			"so the foreground wait triggers earlier instead of piling up. Also check for a " +
			"write spike — these knobs make flushing smoother, they do not change the volume.",
	},
	"块设备队列": {
		NameEN: "block device queue",
		Short:  "卡在块设备队列：请求已经发出，在等存储设备",
		Advice: "这一层说明请求真的到了设备，之前几层都通过了。" +
			"看队列深度和设备的响应时间：iostat -x 1 的 await 和 svctm，" +
			"await 远大于 svctm 说明队列积压。这时候才是真的要去看存储：" +
			"磁盘健康、RAID 卡缓存、有没有其他租户在抢。" +
			"如果 await 和 svctm 接近，设备本身是快的，问题在上面几层。",
		Local:   true,
		ShortEN: "stuck in the block device queue: the request is out and waiting on storage",
		AdviceEN: "This layer means the request really reached the device — everything above " +
			"it is clear. Look at queue depth and device response time: await and svctm in " +
			"iostat -x 1; await far above svctm means the queue is backing up. This is the " +
			"point at which storage is genuinely the next thing to inspect: disk health, " +
			"RAID controller cache, other tenants competing for it. If await and svctm are " +
			"close, the device itself is fast and the problem is in a layer above.",
	},
	"内核锁": {
		NameEN: "kernel lock",
		Short:  "卡在内核锁：在等一个内核里的信号量或互斥量",
		Advice: "不是 I/O 问题，是锁竞争。通常是某个子系统持有锁太久" +
			"（文件系统、内存管理、或者一个驱动）。" +
			"这类等待会随并发量上升而急剧恶化，而且不会体现在任何 I/O 指标上。" +
			"看是哪个子系统持有：如果集中在一个文件系统上，先看那个文件系统；" +
			"如果分散，通常是内核版本已知的问题，查一下发行版的 errata。",
		Local:   true,
		ShortEN: "stuck on a kernel lock: waiting on a semaphore or mutex inside the kernel",
		AdviceEN: "This is not I/O, it is lock contention — usually a subsystem holding a " +
			"lock too long (a file system, memory management, or a driver). Waits like this " +
			"degrade sharply as concurrency rises and show up in no I/O metric at all. Find " +
			"out which subsystem holds it: if it concentrates on one file system, look there " +
			"first; if it is spread out, it is usually a known issue with this kernel — check " +
			"your distribution's errata.",
	},
	"futex": {
		NameEN: "futex",
		Short:  "卡在 futex：应用自己的用户态锁竞争",
		Advice: "这是应用层的锁，不是内核的问题，也不该从内核参数上去修。" +
			"D 状态下出现 futex 说明等锁期间还捎带了 I/O 或者内存分配。" +
			"去看应用的锁设计：是不是有一个全局锁保护了热路径，" +
			"是不是线程池太小导致请求排队在锁上。" +
			"加 CPU 核心对这种问题通常无效 —— 瓶颈是串行段。",
		Local:   true,
		ShortEN: "stuck on a futex: contention on the application's own userspace lock",
		AdviceEN: "This is an application-level lock, not a kernel problem, and it should not " +
			"be fixed by tuning kernel parameters. A futex in D state means the wait also " +
			"picked up an I/O or an allocation. Go and look at the application's locking " +
			"design: is one global lock protecting a hot path, is the thread pool too small " +
			"so requests queue on the lock. Adding CPU cores usually does nothing here — the " +
			"bottleneck is the serial section.",
	},

	// ---------- 内存：回收路径的四个不同决定点 ----------
	// 分开的理由和知识树一样：cgroup 限额、整机回收、整理、页分配器，
	// 四者的处置方向完全不同，指错方向比不知道更贵。

	"直接内存回收": {
		NameEN: "direct memory reclaim",
		Short:  "卡在直接内存回收：业务线程要内存时发现没有，只能自己动手回收",
		Advice: "业务线程要内存而空闲页不够，它只能自己去回收 —— 这就是分配停顿。" +
			"先看 pgscan_ 和 pgsteal_ 的比值：低于 0.3 说明扫了很多页却回收不了几页，" +
			"问题不是内存不够，而是回收被挡住了（页被钉住、太脏、或者页型不对）。" +
			"allocstall_* 每次停顿都会加一，那个数字在涨就是在持续停顿。" +
			"调 vm.min_free_kbytes 只是把停顿提前，换不来内存；" +
			"真正的方向是加内存或者把工作集压小。",
		Local:   true,
		ShortEN: "stuck in direct reclaim: the thread had to reclaim memory itself",
		AdviceEN: "A business thread needed memory, found none free, and had to reclaim " +
			"itself — that is an allocation stall. Start with the pgscan_/pgsteal_ ratio: " +
			"below 0.3 means it scanned plenty and reclaimed almost nothing, so the problem " +
			"is not too little memory but reclaim being blocked (pinned pages, too-dirty " +
			"pages, or the wrong page type). allocstall_* increments once per stall — if it " +
			"is climbing, you are stalling continuously. Tuning vm.min_free_kbytes only " +
			"moves the stall earlier; the real fix is more memory or a smaller working set.",
	},
	"分配器页等待": {
		NameEN: "page allocation wait",
		Short:  "卡在页分配器：内存总量够，但拿不到需要的那一页",
		Advice: "还没走到回收那一步就已经在等了 —— 分配器手上没有可用的页。" +
			"最常见的原因是请求高阶连续页（order 大于 0），" +
			"内存总量够但空间不连续。看 /proc/buddyinfo：某个 order 全是 0 " +
			"就说明是这个区域的连续性不够。" +
			"高阶分配靠调内核参数很难改，通常要靠预留区或者 hugepage。",
		Local:   true,
		ShortEN: "stuck in the page allocator: enough memory in total, wrong page available",
		AdviceEN: "The wait happens before reclaim even starts — the allocator has no " +
			"suitable page. The usual cause is a high-order contiguous request (order > 0): " +
			"enough memory in total, not enough of it contiguous. Read " +
			"/proc/buddyinfo — an order that is all zeros is exactly this. High-order " +
			"allocations are hard to fix by tuning; they usually need a reserved region or " +
			"hugepages.",
	},
	"内存碎片整理": {
		NameEN: "memory compaction",
		Short:  "卡在内存碎片整理：内存够，但要一整块连续的",
		Advice: "内存总量够，问题是空间不连续，内核在把碎片挪成整块。" +
			"整理很贵，所以内核只在水位线触发时才做 —— 触发就是一次延迟尖峰。" +
			"看 compact_stall 是不是在涨，那是每次因整理而阻塞的计数。" +
			"用 khugepaged 或者 hugepage 减少高阶分配，是绕开整理的常见办法。" +
			"重启能清掉碎片，所以这类问题最诚实的建议是安排一个重启窗口，" +
			"而不是继续调参数。",
		Local:   true,
		ShortEN: "stuck in memory compaction: enough memory, but not contiguous",
		AdviceEN: "There is enough memory in total; it is not contiguous, and the kernel is " +
			"migrating pages to make it so. Compaction is expensive, so the kernel only " +
			"triggers it at a watermark — and triggering it is a latency spike. Watch " +
			"compact_stall: it counts every stall caused by compaction. Using khugepaged or " +
			"hugepages to cut down on high-order allocations is the usual way around it. A " +
			"reboot clears fragmentation, so for this one the honest advice is to schedule " +
			"a reboot window rather than keep tuning.",
	},
	"khugepaged": {
		NameEN: "khugepaged",
		Short:  "卡在 khugepaged：后台在把普通页折叠成透明大页",
		Advice: "这是后台线程在做透明大页（THP）的折叠，不是业务线程被卡住。" +
			"但它有两个真实代价：常驻 THP 会放大写（一次写可能触发整块 2MB 的复制），" +
			"内存有空洞时 khugepaged 会反复扫同一段地址空间、扫不到就反复重来。" +
			"数据库场景通常应该关掉 THP —— 延迟抖动比省下来的内存更值钱。" +
			"看 /proc/meminfo 的 AnonHugePages 占比。",
		Local:   true,
		ShortEN: "stuck in khugepaged: the background thread folding pages into huge pages",
		AdviceEN: "This is the background thread doing transparent huge page collapsing, " +
			"not a business thread being blocked. It still has two real costs: a resident " +
			"THP amplifies writes (one write can trigger a copy of a whole 2MB block), and " +
			"with holes in memory khugepaged rescans the same address space over and over. " +
			"For databases THP is usually better turned off — the latency jitter costs more " +
			"than the saved memory. Check the AnonHugePages share in /proc/meminfo.",
	},

	// ---------- 网络：两类等待的可修方向完全不同 ----------

	"TCP 发送缓冲满": {
		NameEN: "TCP send buffer full",
		Short:  "卡在 TCP 发送缓冲满：数据出不去，不是在等磁盘",
		Advice: "发送缓冲区满了，线程在等对端把数据取走。" +
			"最常见的原因是对端接收慢或者接收窗口被关小 —— 那不是本机的问题。" +
			"先分清是哪一种：对端慢就查链路和接收方，别急着调本机参数。" +
			"确认是缓冲区本身太小再调大 wmem_max 和 wmem_default，" +
			"但调大会把数据堆在本机、掩盖下游的问题。" +
			"看 snd_cwnd 和重传队列：重传在涨说明是对端或链路，不是缓冲区。",
		Local:   false,
		ShortEN: "stuck on a full TCP send buffer: data cannot leave, this is not disk",
		AdviceEN: "The send buffer is full and the thread is waiting for the peer to take " +
			"the data. The usual cause is a slow receiver or a closed window — which is not " +
			"this machine's problem. Work out which it is before touching local tuning: if " +
			"the peer is slow, go look at the link and the receiver. Only raise " +
			"wmem_max/wmem_default once you have confirmed the buffer really is too small, " +
			"and know that a bigger buffer just piles data on this host and hides the " +
			"downstream problem. Watch snd_cwnd and the retransmit queue: rising " +
			"retransmits point at the peer or the link, not at the buffer.",
	},
	"socket 接收等待": {
		NameEN: "socket receive wait",
		Short:  "卡在 socket 接收：在等对端发数据",
		Advice: "阻塞式服务在空闲时就是这个状态，多数时候它不是问题。" +
			"真正要判断的是等得久不久 —— 用 plumb 的等待时长排序，" +
			"排在第一位的那个才值得查。" +
			"如果这一类排在前面，说明服务大部分时间在等对端而不是在干活，" +
			"这时候要查的是对端的响应速度，不是本机的内核参数。",
		Local:   false,
		ShortEN: "stuck on a socket receive: waiting for the peer to send",
		AdviceEN: "For a blocking service this is what idle looks like, and most of the " +
			"time it is not a problem. The real question is how long it has waited — sort " +
			"by wait duration in plumb and only the top one is worth investigating. If " +
			"this class is at the top, the service is spending most of its time waiting " +
			"on the peer rather than working, and what deserves attention is the peer's " +
			"responsiveness, not this host's kernel parameters.",
	},
	"epoll/select 等待": {
		NameEN: "epoll/select wait",
		Short:  "卡在 epoll/select：事件循环在等新事件，这是正常状态",
		Advice: "事件驱动架构里每个空闲的工作线程都停在这里，这是设计，不是故障。" +
			"不要去优化这个等待。" +
			"真要查的是这个线程多久才被唤醒一次，以及唤醒后是不是立刻又睡下 —— " +
			"那才说明处理太慢。这时候该查的是事件处理路径，不是内核参数。",
		Local:   true,
		ShortEN: "stuck in epoll/select: the event loop is waiting, which is normal",
		AdviceEN: "In an event-driven architecture every idle worker sits here. This is " +
			"the design, not a fault, and there is nothing to optimise. What is worth " +
			"looking at is how long the thread stays asleep and whether it goes straight " +
			"back to sleep after waking — that is what tells you event handling is too " +
			"slow. When it does, look at the handling path, not at kernel parameters.",
	},
	"io_uring 等待": {
		NameEN: "io_uring wait",
		Short:  "卡在 io_uring：在等完成队列，正常等待",
		Advice: "异步 IO 在等完成队列里的新条目，这是正常等待。" +
			"怀疑的时候看 /proc/interrupts 里和 io_uring 相关的中断计数有没有在涨 —— " +
			"不涨但线程长期停在这里，才说明完成事件没到。",
		Local:   true,
		ShortEN: "stuck in io_uring: waiting on the completion queue, which is normal",
		AdviceEN: "Async I/O waiting for a new entry on the completion queue — a normal " +
			"wait. When you want to check it, look at the io_uring lines in " +
			"/proc/interrupts: if they are not climbing while threads sit here for a long " +
			"time, completions are not arriving.",
	},

	// ---------- 块设备：队列和超时的处置完全不同 ----------

	"块设备超时重试": {
		NameEN: "block device timeout",
		Short:  "卡在块设备超时：请求没按时回来，设备或控制器可能已经出问题",
		Advice: "这一类已经越过慢，进入可能坏了。" +
			"先看内核日志里的 I/O 错误和设备重置记录：" +
			"出现 device reset、host reset、I/O timeout 时，先怀疑硬件再怀疑配置。" +
			"可能是设备固件挂死、控制器掉线、线缆或背板问题，" +
			"也可能是阵列卡降级重建、或者多路径切换时某条路径失效。" +
			"这类卡住不会自己好起来，不要靠重启掩盖 —— 该报修就报修，该换路径就换路径。",
		Local:   true,
		ShortEN: "stuck on a block device timeout: the request is not coming back in time",
		AdviceEN: "This class is past \"slow\" and into \"possibly broken\". Go to the kernel " +
			"log first: device reset, host reset, and I/O timeout all point at hardware " +
			"before configuration. It may be dead firmware, a dropped controller, cabling " +
			"or backplane trouble, an array degrading and rebuilding, or one path of a " +
			"multipath setup failing over. These waits do not clear on their own — do not " +
			"use a reboot to paper over it. Call for the part, or fail the path over.",
	},

	// ---------- 同步与调度 ----------

	"RCU 等待": {
		NameEN: "RCU wait",
		Short:  "卡在 RCU：读侧临界区太长，挡住了写侧",
		Advice: "被读侧挡住的是写侧，表现通常是短暂的延迟尖峰而不是吞吐下降。" +
			"读侧临界区里不允许睡眠，所以它只能靠抢占来推进 —— " +
			"CPU 紧张的时候 RCU 延迟会被明显放大。" +
			"缩短读侧临界区是内核侧的事，运维能做的有限；" +
			"但要先确认 CPU 是不是真的不够，因为那是你唯一能改的地方。",
		Local:   true,
		ShortEN: "stuck in RCU: a long read-side critical section is blocking the writer",
		AdviceEN: "A long read-side critical section is holding up the writer, which shows " +
			"up as short latency spikes rather than lost throughput. Read-side sections " +
			"cannot sleep, so they can only advance by preemption — which means RCU " +
			"latency is amplified when CPUs are short. Shortening the read-side is a " +
			"kernel-side fix and not yours to make, but check whether CPUs are actually " +
			"scarcity first, because that is the part you can change.",
	},
	"调度器": {
		NameEN: "scheduler wait",
		Short:  "卡在调度器：线程自己选择带超时睡眠，栈里没有别的资源挡着",
		Advice: "这一类说明线程是自愿睡着的，不是被资源挡住 —— 和 CPU 限流不同，" +
			"限流是内核不让它跑，这里是它自己不跑。" +
			"如果它是等待名单里的第一名，看它睡了多久：短暂的是正常，" +
			"秒级以上的要回去看应用逻辑是不是哪里写死了 sleep。",
		Local:   true,
		ShortEN: "stuck on a timed sleep: the thread chose to sleep, nothing is blocking it",
		AdviceEN: "This class means the thread went to sleep voluntarily rather than being " +
			"blocked by a resource — which is different from CPU throttling, where the " +
			"kernel is refusing to run it. If it tops the waiting list, look at how long it " +
			"slept: brief sleeps are normal, and multi-second ones mean you should go back " +
			"and look for a hardcoded sleep in the application.",
	},
	"等待子进程": {
		NameEN: "waiting on a child process",
		Short:  "卡在等待子进程：父进程在等子进程退出",
		Advice: "这通常是正常逻辑 —— shell 跑命令、进程管理器等 worker、测试框架跑用例。" +
			"要查的不是这个等待的进程，而是它的子进程：" +
			"子进程自己卡住的时候，等待方就一直卡着。" +
			"plumb 的线程表会列出同进程的所有线程，顺着子进程的 pid 去看它的内核栈，" +
			"问题在那里。",
		Local:   true,
		ShortEN: "waiting on a child process: the parent is waiting for the child to exit",
		AdviceEN: "This is usually normal logic — a shell running a command, a supervisor " +
			"waiting on a worker, a test runner running a case. The process to investigate " +
			"is not the waiter but its child: when the child wedges, the waiter waits " +
			"forever. plumb lists every thread of the process; follow the child's pid and " +
			"read its kernel stack, that is where the problem is.",
	},
}

// TranslateSubsystem returns the meaning and remedy for a stack subsystem,
// or a zero Subsystem when the name is not in the table. Unknown is
// reported honestly rather than mapped to a plausible neighbour.
func TranslateSubsystem(name string, lang i18n.Lang) Subsystem {
	return subsystemTable[name].In(lang)
}

// subsystemName returns the name to print for a subsystem key: the English
// one when one exists and the report is in English, otherwise the key.
//
// The key is what probe classified the stack into, so it is also the fallback:
// an untranslated subsystem still gets named, just not in the reader's
// language — which is the honest order of failures.
func (a *analysis) subsystemName(key string) string {
	if a.lang == i18n.EN {
		if n := subsystemTable[key].NameEN; n != "" {
			return n
		}
	}
	return key
}
func (a *analysis) blockedSubsystems() []string {
	var threads []probe.Thread
	for _, p := range a.cur.Threads.Processes {
		threads = append(threads, p.BlockedOn...)
	}
	return probe.StackSubsystems(threads)
}

// BlockedSubsystemLine formats the subsystem findings for an evidence row,
// or "" when there is nothing to say. It names the deciding frame for each
// subsystem so the classification can be checked rather than trusted.
func (a *analysis) BlockedSubsystemLine() string {
	subs := a.blockedSubsystems()
	if len(subs) == 0 {
		return ""
	}
	var parts []string
	for _, s := range subs {
		sub := TranslateSubsystem(s, a.lang)
		part := a.subsystemName(s)
		if sub.Short != "" {
			part = sub.Short
		}
		if !sub.Local {
			// The single most valuable thing plumb can say to someone about
			// to restart a server: the wait is not on this machine.
			part += a.msg("（不在本机，重启本机无效）", " (not on this machine; rebooting here will not help)")
		}
		parts = append(parts, part)
	}
	return strings.Join(parts, a.msg("；", "; "))
}
