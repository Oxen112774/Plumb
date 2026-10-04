package knowledge

// Stack leaves name the kernel subsystem a blocked thread's call chain
// resolves to. They are the answer to a question wchan cannot answer:
// wchan reports the innermost function, which for nearly every block-I/O
// wait is io_schedule, and io_schedule names nothing. The stack names the
// subsystem that decided to wait, and the subsystems here have remedies
// that are not interchangeable — which is the whole reason they are in the
// tree rather than in a comment somewhere.
//
// Keys are the exact strings plumb prints after "内核栈：".
var stackLeaves = []Leaf{
	{
		Key:   "ext4/jbd2 日志",
		Kind:  KindStack,
		Alias: []string{"jbd2", "ext4", "jbd2_log_wait_commit", "日志提交"},
		What:  "文件系统在等它自己的事务日志落盘，不是磁盘硬件慢。",
		Why: []string{
			"每次 fsync 都要等一次日志提交，提交是串行的",
			"日志设备和数据在同一个盘上时，提交要和数据写抢带宽",
		},
		Fix: []string{
			"先看 fsync 频率：数据库和日志服务每次提交都触发一次，频率过高会压满队列",
			"频率正常就把日志挪到独立设备，这是最常见的结构性修复",
		},
		Unknown: "内核不报告是哪个文件触发了这次提交。plumb 只能从进程持有的文件里缩小范围。",
		Sources: []string{
			"内核源码 fs/jbd2/commit.c（jbd2_journal_commit_transaction）",
			"内核源码 fs/ext4/fsync.c（ext4_sync_file）",
		},
		Confidence: Certain,
	},
	{
		Key:   "XFS",
		Kind:  KindStack,
		Alias: []string{"xfs_log_force", "xfs_file_fsync", "xfs元数据"},
		What:  "卡在 XFS 的日志或元数据操作上，这类操作是串行的。",
		Why: []string{
			"一次大量小文件的创建或删除就能把整机的写堵住",
			"配额检查、目录遍历也会走到这里",
		},
		Fix: []string{
			"确认是不是有批量删除或目录遍历在跑",
			"xfs_info 看日志大小，日志太小会让提交更频繁",
		},
		Unknown: "XFS 的元数据串行点有很多个，这个分类只说明是 XFS，不说明是哪一个。",
		Sources: []string{
			"内核源码 fs/xfs/xfs_log.c",
			"内核源码 fs/xfs/xfs_file.c",
		},
		Confidence: Rough,
	},
	{
		Key:   "btrfs",
		Kind:  KindStack,
		Alias: []string{"btrfs_", "写时复制"},
		What:  "卡在 btrfs 的写时复制或元数据提交上。",
		Why: []string{
			"写时复制让单次写可能触发整棵树的复制，随机写会被放大",
			"自动快照会让写放大持续恶化，直到快照被清理",
		},
		Fix: []string{
			"先确认是不是有快照在自动创建",
			"btrfs filesystem usage 看实际分配，接近满时 btrfs 会非常慢",
		},
		Unknown: "写放大的倍数 plumb 读不出来，只能从现象上提示。",
		Sources: []string{
			"内核源码 fs/btrfs/ctree.c",
			"btrfs wiki: Btrfs Wiki / Gotchas",
		},
		Confidence: Rough,
	},
	{
		Key:   "FUSE",
		Kind:  KindStack,
		Alias: []string{"s3fs", "sshfs", "goofys", "用户态文件系统"},
		What:  "卡在一个用户态文件系统上：请求要走到用户态守护进程再回来，而它没有回答。",
		Why: []string{
			"守护进程慢、卡住或者已经死了",
			"这是进程问题，不是磁盘问题 —— 内核这边只能一直等",
		},
		Fix: []string{
			"先确认那个守护进程还活着，它在 ps 里通常不显得异常，但 CPU 会是 0",
			"FUSE 挂载在 D 状态下除了 umount -f 没有别的摆脱方式，重启往往是唯一出路",
		},
		Unknown: "plumb 看不出守护进程为什么没回答 —— 它在用户态，/proc 里没有它的等待原因。",
		Sources: []string{
			"内核源码 fs/fuse/dev.c（fuse_simple_request）",
		},
		Confidence: Rough,
	},
	{
		Key:   "NFS/RPC",
		Kind:  KindStack,
		Alias: []string{"nfs", "rpc", "sunrpc", "xprt", "网络文件系统"},
		What:  "在等一个网络文件系统的服务器回应 —— 这台机器本身没有问题。",
		Why: []string{
			"NFS 服务器挂了、网络断了、或者端口映射变了",
			"硬挂载（hard mount）下内核会一直等，没有超时",
		},
		Fix: []string{
			"重启本机不会有丝毫帮助：D 状态连 SIGKILL 都收不到，重启过程会卡在关机流程里",
			"去看 NFS 服务器和中间的网络，那才是故障点",
			"服务器短期回不来时，umount -f / -l 或重启是唯一出路",
		},
		Unknown: "plumb 只能确认等待发生在 NFS 子系统里，看不到远端到底怎么了。",
		Sources: []string{
			"内核源码 fs/nfs/pagelist.c（nfs_pageio_doio）",
			"内核源码 net/sunrpc/clnt.c（rpc 重传与超时）",
		},
		Confidence: Certain,
	},
	{
		Key:   "cgroup 内存回收",
		Kind:  KindStack,
		Alias: []string{"mem_cgroup", "内存回收", "容器限额", "shrink_node"},
		What:  "在 cgroup 的内存限额内做回收，回收期间所有分配都要等。",
		Why: []string{
			"容器的内存 limit 给小了，内核只能在限额内反复回收",
			"表现是整机随机卡顿，但内存使用率看起来并不高",
		},
		Fix: []string{
			"调大这个容器的内存 limit，通常设成工作集的 1.5 倍",
			"不要去查应用的内存泄漏 —— 是配额问题，不是代码问题",
		},
		Unknown: "工作集大小 plumb 读不出来，得从进程的 RSS 和它的分配模式去估。",
		Sources: []string{
			"内核源码 mm/vmscan.c（try_to_free_mem_cgroup_pages）",
			"内核源码 mm/memcontrol.c（mem_cgroup_try_charge）",
		},
		Confidence: Certain,
	},
	{
		Key:   "页回写",
		Kind:  KindStack,
		Alias: []string{"writeback", "wait_on_page", "脏页回写", "filemap_fdatawait"},
		What:  "在等已经发出去的脏页写回完成。",
		Why: []string{
			"后台刷盘开始得太晚，前台就被迫同步等",
			"写入突增时脏页会积压，即使参数合理也会堵",
		},
		Fix: []string{
			"把 vm.dirty_background_ratio 调小让后台刷盘更早开始",
			"把 vm.dirty_ratio 调小让前台等待更早触发，而不是一次性积压",
		},
		Unknown: "调参数只能让刷盘更平滑，改变不了写入总量。",
		Sources: []string{
			"内核源码 mm/page-writeback.c",
			"内核源码 mm/filemap.c（__filemap_fdatawait_range）",
		},
		Confidence: Certain,
	},
	{
		Key:   "块设备队列",
		Kind:  KindStack,
		Alias: []string{"blk_mq", "submit_bio", "块层", "设备队列"},
		What:  "请求已经到了块设备层，在等存储设备本身。",
		Why: []string{
			"存储真的被打满了：队列深度过高、磁盘慢、或者有其他租户在抢",
			"这一层说明上面几层都通过了，问题确实在设备上",
		},
		Fix: []string{
			"iostat -x 1 看 await 和 svctm：await 远大于 svctm 说明队列积压",
			"两者接近说明设备本身是快的，问题在上面几层，别去换盘",
		},
		Unknown: "plumb 看不出是设备慢还是别人在抢 —— 那需要按设备聚合，plumb 不做。",
		Sources: []string{
			"内核源码 block/blk-mq.c",
			"内核源码 block/bio.c（submit_bio）",
		},
		Confidence: Certain,
	},
	{
		Key:   "内核锁",
		Kind:  KindStack,
		Alias: []string{"rwsem", "mutex", "down_read", "锁竞争"},
		What:  "在等内核里的一把锁，不是 I/O 问题。",
		Why: []string{
			"某个子系统持有锁太久：文件系统、内存管理、或者一个驱动",
			"随并发量上升急剧恶化，而且完全不会体现在 I/O 指标上",
		},
		Fix: []string{
			"看等待是不是集中在一个子系统上，是就先看那个子系统",
			"如果分散，通常是内核版本的已知问题，查发行版 errata",
		},
		Unknown: "plumb 看不到是谁持有着那把锁 —— 那需要 lockdep 或 eBPF。",
		Sources: []string{
			"内核源码 kernel/locking/rwsem.c",
			"内核源码 kernel/locking/mutex.c",
		},
		Confidence: Rough,
	},
	{
		Key:   "futex",
		Kind:  KindStack,
		Alias: []string{"futex_wait", "用户态锁"},
		What:  "卡在应用自己的用户态锁竞争上。",
		Why: []string{
			"应用层锁设计问题：全局锁保护了热路径，或者线程池太小",
			"在 D 状态下出现说明等锁期间还捎带了 I/O 或内存分配",
		},
		Fix: []string{
			"去看应用的锁设计和线程池配置，这不是内核参数能修的",
			"加 CPU 核心通常无效 —— 瓶颈是串行段",
		},
		Unknown: "plumb 不知道是哪一把锁、被谁持有 —— 那在用户态，/proc 里没有。",
		Sources: []string{
			"内核源码 kernel/futex/waitwake.c（futex_wait）",
		},
		Confidence: Rough,
	},

	// ---------- 内存：回收路径的四个不同决定点 ----------

	{
		Key:   "直接内存回收",
		Kind:  KindStack,
		Alias: []string{"direct reclaim", "try_to_free_pages", "__alloc_pages_direct_reclaim", "分配停顿", "allocstall"},
		What:  "线程在分配内存时发现没有空闲页，只能自己动手回收 —— 这就是分配停顿。",
		Why: []string{
			"业务线程要的页型不对，free 内存再多也拿不到（碎片）",
			"kswapd 跟不上回收速度，分配方等不及就自己上",
			"回收效率低：扫了很多页但回收不了几页，多数页还被钉住或已脏",
		},
		Fix: []string{
			"先看 pgscan_/pgsteal_ 的比值：低于 0.3 说明回收基本无效，不是内存不够而是回收被挡住了",
			"看 allocstall_* 是否在涨，那是每次停顿的直接计数",
			"长期方向是加内存或降低工作集；调 vm.min_free_kbytes 只是把停顿提前，不能消除",
		},
		Unknown: "plumb 不知道这次分配要的是多大、什么类型的页 —— 内核不把 gfp 掩码写进栈里。",
		Sources: []string{
			"内核源码 mm/page_alloc.c（__alloc_pages_direct_reclaim）",
			"内核源码 mm/vmscan.c（try_to_free_pages、shrink_folio_list）",
			"内核文档 Documentation/admin-guide/mm/multigen_lru.rst",
		},
		Confidence: Certain,
	},
	{
		Key:   "分配器页等待",
		Kind:  KindStack,
		Alias: []string{"__alloc_pages", "page allocation", "页分配等待", "order"},
		What:  "卡在页分配器上，还没走到回收那一步就已经在等。",
		Why: []string{
			"请求的是高阶连续页（order > 0），内存是够的但不连续",
			"-zone 没有可用的空闲页：DMA 区往往最先耗尽",
		},
		Fix: []string{
			"看 /proc/buddyinfo：某个 order 全是 0 就是这里说的连续性不够",
			"高阶分配要靠 memory 预留或 hugepage，普通调参很难改",
		},
		Unknown: "plumb 读不到这个分配请求的 order 和 zone，两者都不在栈里。",
		Sources: []string{
			"内核源码 mm/page_alloc.c（__alloc_pages）",
			"内核源码 mm/vmstat.c（buddyinfo 输出）",
		},
		Confidence: Rough,
	},
	{
		Key:   "内存碎片整理",
		Kind:  KindStack,
		Alias: []string{"compaction", "kcompactd", "migrate_pages", "内存整理", "内存碎片"},
		What:  "内存总量够，但要的是一整块连续的，线程在等内核把碎片挪成连续的。",
		Why: []string{
			"长期运行的机器上不可迁移的页（内核页表、hugetlb、per-CPU 结构）把可用空间切成小块",
			"整理比回收贵得多，所以内核只在水位线触发时才做，触发即延迟尖峰",
		},
		Fix: []string{
			"看 compact_stall 是否在涨，那是每次因整理而阻塞的计数",
			"用 khugepaged 或 hugepage 减少高阶分配，是绕开整理的常见办法",
			"重启能清掉碎片 —— 这类问题最诚实的建议就是安排重启窗口",
		},
		Unknown: "plumb 不知道是哪个 zone 在整理，也看不到整理的进度百分比。",
		Sources: []string{
			"内核源码 mm/compaction.c（compact_compaction、migrate_pages）",
			"内核文档 Documentation/admin-guide/mm/compaction.rst",
		},
		Confidence: Certain,
	},
	{
		Key:   "khugepaged",
		Kind:  KindStack,
		Alias: []string{"collapse_huge_page", "透明大页", "THP 折叠", "khugepaged_scan_mm_slot"},
		What:  "后台线程在扫描并把普通页折叠成透明大页（THP）。",
		Why: []string{
			"常驻的透明大页在制造写放大：一次写可能触发整块 2MB 的复制",
			"内存有空洞时，khugepaged 会反复扫描同一段地址空间，扫不到就反复重来",
		},
		Fix: []string{
			"数据库场景通常应该关掉 THP，延迟抖动比内存省下来的多",
			"看 /proc/meminfo 的 AnonHugePages 占比，以及 khugepaged 的扫描量",
		},
		Unknown: "plumb 区分不了 khugepaged 是在成功折叠还是反复失败，两者的栈是一样的。",
		Sources: []string{
			"内核源码 mm/khugepaged.c（khugepaged_scan_mm_slot、collapse_huge_page）",
			"内核文档 Documentation/admin-guide/mm/transhuge.rst",
		},
		Confidence: Rough,
	},

	// ---------- 网络：两类等待的可修方向完全不同 ----------

	{
		Key:   "TCP 发送缓冲满",
		Kind:  KindStack,
		Alias: []string{"sk_stream_wait_memory", "tcp_sendmsg", "send buffer", "发送阻塞", "sndbuf"},
		What:  "发送缓冲区满了，线程在等对端把数据取走 —— 数据出不去，不是在等磁盘。",
		Why: []string{
			"对端接收慢或接收窗口被关小，这是最常见的原因",
			"SO_SNDBUF 设得比单次写入还小，每次写都得等",
			"网络栈整体内存压力下分配不到新的 skb",
		},
		Fix: []string{
			"先分清是哪一种：对端慢就查链路和接收方，别急着调本机参数",
			"确认是缓冲区小再调大 wmem_max/wmem_default，但调大会把数据堆在本机、掩盖下游问题",
			"看 snd_cwnd 和重传队列，重传在涨说明是对端或链路而不是缓冲区",
		},
		Unknown: "plumb 分不清是接收窗口小还是分不到 skb —— 两者栈里都是 sk_stream_wait_memory。",
		Sources: []string{
			"内核源码 net/core/stream.c（sk_stream_wait_memory）",
			"内核文档 Documentation/networking/ip-sysctl.rst",
		},
		Confidence: Rough,
	},
	{
		Key:   "socket 接收等待",
		Kind:  KindStack,
		Alias: []string{"tcp_recvmsg", "sock_recvmsg", "接收阻塞", "read 阻塞"},
		What:  "线程在等对端发数据过来，栈里是接收路径。",
		Why: []string{
			"对端还没发，或者发了但网络还没到",
			"接收缓冲被应用读空后继续等下一次数据",
		},
		Fix: []string{
			"这一类往往不是问题：正常阻塞式服务的常态就是这样",
			"要判断的是等得久不久 —— 用 plumb 的等待时长排序，排在第一位的才值得查",
		},
		Unknown: "plumb 不知道对端是谁，也不知道已经等了多久 —— 连接信息不在栈里。",
		Sources: []string{
			"内核源码 net/ipv4/tcp.c（tcp_recvmsg）",
			"内核源码 net/core/sock.c（sock_recvmsg）",
		},
		Confidence: NameOnly,
	},
	{
		Key:   "epoll/select 等待",
		Kind:  KindStack,
		Alias: []string{"ep_poll", "do_epoll_wait", "poll", "事件循环"},
		What:  "线程在事件循环里等新事件 —— 这是正常状态，不是阻塞。",
		Why: []string{
			"事件驱动架构里每个工作线程空闲时都停在这里",
			"负载高时会频繁在就绪和等待之间切换，这个切换频率才是信息",
		},
		Fix: []string{
			"不要去优化这个等待，它就是设计",
			"真要查的是这个线程多久才被唤醒一次，以及唤醒后是否立刻又睡下（那说明处理太慢）",
		},
		Unknown: "plumb 不知道有没有事件在等，只知道线程停在这条路径上。",
		Sources: []string{
			"内核源码 fs/eventpoll.c（ep_poll、do_epoll_wait）",
			"内核源码 fs/select.c（do_select）",
		},
		Confidence: NameOnly,
	},
	{
		Key:   "io_uring 等待",
		Kind:  KindStack,
		Alias: []string{"io_uring_enter", "io_cqring_wait", "io_uring"},
		What:  "在等 io_uring 的完成队列，异步 IO 的正常等待点。",
		Why: []string{
			"提交了请求在等完成，栈里就是等 CQ 上的新条目",
			"如果长期停在这里而系统 IO 指标全低，可能是完成中断没有到达",
		},
		Fix: []string{
			"正常等待不用管",
			"怀疑时看 /proc/interrupts 里 io_uring 相关的中断计数有没有涨",
		},
		Unknown: "plumb 看不到等待的是哪个请求 —— 请求描述符不在栈里。",
		Sources: []string{
			"内核源码 io_uring/io_uring.c（io_uring_enter、io_cqring_wait）",
		},
		Confidence: NameOnly,
	},

	// ---------- 块设备：队列 vs 超时是两个不同的问题 ----------

	{
		Key:   "块设备超时重试",
		Kind:  KindStack,
		Alias: []string{"io_wait", "blk_execute_rq", "nvme_wait_ready", "scsi_", "设备超时", "md_flush_request"},
		What:  "块设备请求没按时回来，驱动在等它或重试 —— 已经越过慢，进入可能坏了。",
		Why: []string{
			"设备固件挂死、控制器掉线、线缆或背板问题",
			"阵列卡降级重建、RAID 正在回读校验",
			"多路径切换时某条路径失效，正在等超时切换",
		},
		Fix: []string{
			"这是硬件层的信号，先看内核日志里的 I/O 错误和设备重置记录",
			"dmesg 里出现 device reset、host reset、I/O timeout 时，先怀疑硬件再怀疑配置",
			"这类卡住不会自己好起来，报修或换路径，不要靠重启掩盖",
		},
		Unknown: "plumb 读不到请求的超时设置和设备健康状态，这些在驱动私有数据里。",
		Sources: []string{
			"内核源码 block/blk-core.c（blk_execute_rq）",
			"内核源码 drivers/nvme/host/core.c",
			"内核文档 Documentation/admin-guide/blockdev.rst",
		},
		Confidence: Rough,
	},

	// ---------- 同步与调度 ----------

	{
		Key:   "RCU 等待",
		Kind:  KindStack,
		Alias: []string{"rcu_", "synchronize_rcu", "rcu_barrier", "RCU", "读侧临界区"},
		What:  "在等一个读侧临界区结束 —— 被读侧挡住的是写侧，表现为短暂延迟尖峰。",
		Why: []string{
			"读侧临界区太长：里面做了耗时的操作，或被抢占、被中断",
			"读侧临界区里不允许睡眠，所以它只能靠抢占，代价是延迟",
		},
		Fix: []string{
			"缩短读侧临界区是内核侧的事，运维能做的有限",
			"看有没有 CPU 严重不足 —— 读侧靠抢占推进，CPU 紧张时 RCU 延迟会被放大",
		},
		Unknown: "plumb 不知道在等哪一处 RCU 回调完成，内核不把对象名写进栈里。",
		Sources: []string{
			"内核源码 kernel/rcu/tree.c（rcu_、synchronize_rcu）",
			"内核文档 Documentation/RCU/whatisRCU.rst",
		},
		Confidence: Rough,
	},
	{
		Key:   "调度器",
		Kind:  KindStack,
		Alias: []string{"schedule_timeout", "sched_", "睡眠等待", "延时"},
		What:  "线程自己选择带超时睡眠 —— 栈里没有别的资源在挡它。",
		Why: []string{
			"应用显式 sleep、定时器、条件变量超时",
			"前面某个等待已经解决，现在是收尾的短暂停顿",
		},
		Fix: []string{
			"如果这是等待名单里的第一名，看它睡多久：短暂的是正常，秒级以上的要查应用逻辑",
			"和 CPU 限流区分：限流是内核不让它跑，这里是它自己不跑",
		},
		Unknown: "plumb 只有栈，看不到这个超时是应用设的还是别人设的。",
		Sources: []string{
			"内核源码 kernel/sched/core.c（schedule_timeout、__schedule）",
		},
		Confidence: NameOnly,
	},
	{
		Key:   "等待子进程",
		Kind:  KindStack,
		Alias: []string{"do_wait", "wait4", "kernel_wait", "父进程等子进程"},
		What:  "父进程在等子进程退出 —— 这通常是正常逻辑，不是故障。",
		Why: []string{
			"shell 跑命令、进程管理器等 worker、测试框架跑用例",
			"子进程自己卡住时，等待方就一直卡着 —— 真正的问题在子进程的栈里",
		},
		Fix: []string{
			"要查的是子进程，不是这个等待的进程",
			"plumb 的线程表会列出同进程的所有线程，顺着子进程 pid 去看它的栈",
		},
		Unknown: "plumb 不知道子进程是哪一个，也不去追子进程的栈 —— 那需要遍历进程树。",
		Sources: []string{
			"内核源码 kernel/exit.c（do_wait、kernel_wait）",
		},
		Confidence: NameOnly,
	},
}
