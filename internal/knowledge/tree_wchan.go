package knowledge

// The kernel-wait leaves. These are keyed by the function name /proc/<pid>/wchan
// reports, which is the sharpest key plumb has: a symbol names exactly one place
// in the kernel, so there is nothing to guess and nothing to rank.
//
// Ordering inside this file is by layer — block and filesystems, then memory
// reclaim, then locks, then network, then the waits that are not problems — so
// that a reader adding a leaf can see where it belongs.
//
// A note on the sources. They are given as paths into the kernel tree rather
// than URLs, because a URL rots and a source file generally does not: fs/jbd2/
// commit.c has held the journal commit path for years, while documentation URLs
// get reorganised every few releases. Anyone can check a claim by grepping the
// function name in the kernel they are running.

var wchanLeaves = []Leaf{
	// --- the block layer -------------------------------------------------

	{
		Key:  "io_schedule",
		Kind: KindWChan,
		Alias: []string{
			"submit_bio", "submit_bio_wait", "blkdev", "blk_mq_submit_bio",
			"__submit_bio",
		},
		What: "线程已经进了块设备层，在等一次 IO 返回。",
		Why: []string{
			"盘本身慢，或者队列已经排满（机械盘的寻道、云盘被邻居挤占、网络存储的一趟往返）",
			"上层压下来的并发太多：每个请求都不算慢，但排队让等待变成常态",
			"远端存储：iSCSI、NFS、Ceph 之类，延迟不在本机，本机只是在等对面",
		},
		Fix: []string{
			"先量再改：iostat -x 1 看这块盘的 await 与队列深度。await 高而队列浅说明盘真的慢；队列深说明是你压上去的",
			"是排队就降并发：线程池、连接池、队列深度这些旋钮拧小，比换盘便宜得多",
			"随机读写改成顺序（合并小写、预读、批量提交），机械盘上这一条比什么都值钱",
		},
		Unknown: "io_schedule 只知道在等 IO，不知道在等哪块盘、也不知道是谁压上去的。要看进程持有的文件才能回答这两个问题。",
		Sources: []string{
			"内核源码 kernel/sched/core.c（io_schedule、io_schedule_timeout）",
			"内核源码 block/blk-core.c（submit_bio）",
		},
		Confidence: Certain,
	},
	{
		Key:   "blk_mq_get_tag",
		Kind:  KindWChan,
		Alias: []string{"blk_mq", "blk_mq_get_driver_tag", "sbitmap_queue_wait"},
		What:  "在等块设备队列里的一个名额（tag）。",
		Why: []string{
			"队列深度被用满：盘已经吃不下了，后来的请求连进队都要等",
			"上层并发远超盘能同时处理的量",
		},
		Fix: []string{
			"这是“盘过载”最直接的证据，比 util 百分比硬 —— util 到 100% 只是忙，等不到 tag 是真的处理不过来",
			"把压上去的并发降下来，或者把盘换成能扛住这个队列深度的",
		},
		Sources: []string{
			"内核源码 block/blk-mq-tag.c（blk_mq_get_tag）",
			"内核源码 include/linux/sbitmap.h",
		},
		Confidence: Certain,
	},
	{
		Key:   "blk_mq_freeze_queue_wait",
		Kind:  KindWChan,
		Alias: []string{"blk_mq_freeze_queue", "blk_mq_unfreeze_queue"},
		What:  "块设备队列被冻住了，在等它放开。",
		Why: []string{
			"有人正在改这个设备的配置：LVM 的 suspend/resume、热插拔、驱动重置、队列参数变更",
			"一次 dm 操作或者驱动复位卡住了，冻结就一直没解除",
		},
		Fix: []string{
			"这类冻结正常应该在毫秒级。停在这里很久，说明某次设备操作没回来",
			"查是谁在做设备操作（dmsetup、multipath、驱动重置），以及 dmesg 里的超时与重置记录",
		},
		Unknown: "这不是磁盘慢，是设备被配置操作冻住了 —— 用 iostat 看这块盘多半一切正常，从那里查不出问题。",
		Sources: []string{
			"内核源码 block/blk-mq.c（blk_mq_freeze_queue_wait）",
			"内核源码 block/blk-mq.c（blk_mq_freeze_queue / blk_mq_unfreeze_queue）",
		},
		Confidence: Rough,
	},
	{
		Key:   "blkdev_issue_flush",
		Kind:  KindWChan,
		Alias: []string{"blkdev_issue_flush_nowait"},
		What:  "在让设备把写缓存刷到盘上。",
		Why: []string{
			"每次 fsync 的最后一公里：数据写进设备缓存不算数，得让它真的落到介质上",
			"磁盘或 RAID 卡有写缓存时，一次 flush 是一趟真实的往返",
		},
		Fix: []string{
			"这是持久性必须付的代价，别想着关掉它 —— 关掉就是掉电丢数据",
			"能省的只有“别这么频繁”：减少 fsync 次数、合并提交",
			"带掉电保护的写缓存（掉电保护电容、企业盘）能让一次 flush 快很多，这是硬件层面的正解",
		},
		Sources: []string{
			"内核源码 block/blk-flush.c",
			"man 2 fsync",
		},
		Confidence: Certain,
	},
	{
		Key:   "do_blockdev_direct_IO",
		Kind:  KindWChan,
		Alias: []string{"__blockdev_direct_IO", "iomap_dio_rw", "blkdev_direct_IO"},
		What:  "在做直接 IO，绕过了页缓存。",
		Why: []string{
			"应用在用 O_DIRECT：数据库自己的缓冲池、虚拟机的磁盘镜像、部分存储软件",
			"块设备被直接读写（dd、备份工具）",
		},
		Fix: []string{
			"直接 IO 的每次读都要等介质，没有页缓存可以缓冲 —— 延迟就等于盘的延迟，没有折扣",
			"要降延迟只有两条路：换更快的盘，或者让应用别用 O_DIRECT 去换页缓存",
			"确认这是有意的：有些应用用 O_DIRECT 是为了自己管缓存，那是对的；如果只是照抄的配置项，可以试试去掉",
		},
		Sources: []string{
			"内核源码 fs/direct-io.c（老内核路径）",
			"内核源码 fs/iomap/direct-io.c（新内核走 iomap）",
			"man 2 open（O_DIRECT 一节）",
		},
		Confidence: Certain,
	},

	// --- storage hardware and the layers above it -------------------------

	{
		Key:   "nvme_queue_rq",
		Kind:  KindWChan,
		Alias: []string{"nvme_", "nvme", "nvme_process_cq", "nvme_poll", "nvme_irq"},
		What:  "在等 NVMe 设备返回。",
		Why: []string{
			"盘忙，或者队列铺得太深",
			"盘在降速：温度过高、写放大、寿命将尽的盘会明显变慢",
			"超时重试：线缆、背板、盘本身有问题时，一次请求会重试很久",
		},
		Fix: []string{
			"看 dmesg 里的 nvme 记录：timeout、reset、I/O error 都是硬件层面的信号，不是负载问题",
			"看盘的 SMART（nvme smart-log）：可用备用块、介质错误、温度",
			"有重试或重置就别再往调优上花时间了 —— 先解决硬件",
		},
		Unknown: "NVMe 的超时、重置、SMART 都在内核日志和设备自己的寄存器里，不在 /proc 里，plumb 看不到。",
		Sources: []string{
			"内核源码 drivers/nvme/host/pci.c",
			"内核源码 drivers/nvme/host/core.c",
		},
		Confidence: Certain,
	},
	{
		Key:   "scsi_queue_rq",
		Kind:  KindWChan,
		Alias: []string{"scsi_", "scsi", "scsi_dispatch_cmd", "scsi_run_queue"},
		What:  "在等 SCSI 层完成一次命令。",
		Why: []string{
			"机械盘、SAS、USB 存储、以及大量虚拟化平台的虚拟盘都走这一层",
			"重试风暴：介质错误或链路抖动会让一次请求拖成好几次，延迟成倍放大",
		},
		Fix: []string{
			"dmesg 里的 SCSI 报错和 sense key 是唯一能区分“慢”和“要坏”的东西",
			"iostat 的 await 是平均值，会把重试盖住 —— 请求数很低但延迟很高，就要怀疑重试",
		},
		Sources: []string{
			"内核源码 drivers/scsi/scsi_lib.c",
			"内核源码 drivers/scsi/scsi_error.c（重试与错误处理）",
		},
		Confidence: Certain,
	},
	{
		Key:   "md_write_start",
		Kind:  KindWChan,
		Alias: []string{"md_", "md", "raid", "md_write_end", "md_super_wait"},
		What:  "在等软件 RAID 允许这次写开始。",
		Why: []string{
			"RAID5/6 的写惩罚：一次逻辑写会变成读-改-写，随机写尤其吃亏",
			"阵列正在重建或校验（resync/recover/check），后台任务在和业务抢 IO",
			"某块成员盘慢了，整个阵列必须等最慢的那块",
		},
		Fix: []string{
			"看 /proc/mdstat：进度、速度、还有哪块盘是 (F) 失效或 (S) 备用",
			"重建期间性能下降是正常的，但它会结束；如果一直没有进度，去查成员盘的 SMART",
			"随机写为主不要用 RAID5/6，换 RAID10",
			"重建限速：/proc/sys/dev/raid/speed_limit_max 和 sync_speed_max",
		},
		Sources: []string{
			"内核源码 drivers/md/md.c",
			"内核文档 Documentation/admin-guide/md.rst（/proc/mdstat 与 sync_action 各字段）",
		},
		Confidence: Certain,
	},
	{
		Key:   "dm_suspend",
		Kind:  KindWChan,
		Alias: []string{"dm_", "dm", "device-mapper", "dm_resume", "dm_io"},
		What:  "在等 device-mapper 的目标设备把 IO 做完。",
		Why: []string{
			"LVM 的映射层、dm-crypt 加密、dm-thin 精简置备、dm-multipath 都在这一层",
			"dm-crypt 的加解密吞吐不够（没走硬件加速时尤其明显）",
			"thin pool 数据或元数据空间用尽，得等元数据腾出位置",
		},
		Fix: []string{
			"加密层：用 AES-NI 能加速的算法与模式（aes-xts-plain64），别在加密之上再叠压缩",
			"thin pool：dmsetup status 看 data/metadata 的使用百分比，接近满的时候会明显变慢",
			"multipath：看是不是一直在某条路径上重试",
		},
		Sources: []string{
			"内核源码 drivers/md/dm.c",
			"内核源码 drivers/md/dm-crypt.c",
			"内核源码 drivers/md/dm-thin.c",
		},
		Confidence: Certain,
	},
	{
		Key:   "rpc_wait_bit_killable",
		Kind:  KindWChan,
		Alias: []string{"nfs_", "__rpc_execute", "nfs_wait_on_request", "rpc_wait_for_completion_task"},
		What:  "在等一次 NFS 往返。",
		Why: []string{
			"NFS 服务器忙、慢，或者网络在丢包",
			"服务器重启或失联，客户端在按挂载参数重传",
			"默认挂载参数下，NFS 的等待是不可中断的 —— 一次卡住能拖很久",
		},
		Fix: []string{
			"这种挂载上的进程连 kill -9 都杀不掉，只能等超时。所以先看 mount 的 timeo/retrans 与 soft/hard 设置",
			"网络：看丢包与重传（netstat -s 或 ss -s 的 retrans 段）",
			"跨机的问题要用两边的证据：本机只告诉你“在等”，对面的 nfsd 才知道为什么",
		},
		Unknown: "原因多半在另一台机器上。这台机器的 /proc 只能告诉你它在等，说不出对面怎么了。",
		Sources: []string{
			"内核源码 net/sunrpc/sched.c（rpc_wait_bit_killable）",
			"内核源码 fs/nfs/",
			"man 5 nfs（timeo、retrans、soft、hard）",
		},
		Confidence: Certain,
	},
	{
		Key:   "fuse_file_read_iter",
		Kind:  KindWChan,
		Alias: []string{"fuse_", "fuse", "fuse_dev_do_read", "fuse_request_send"},
		What:  "在等一个用户态的 FUSE 服务回话。",
		Why: []string{
			"sshfs、s3fs、glusterfs 这类文件系统：真正的 IO 在另一个用户态进程里做",
			"那个进程慢、卡住、或者已经死了",
		},
		Fix: []string{
			"瓶颈在用户态进程里，不在内核。先找到它（ps 里带 fuse 的那个），看它在干什么",
			"关键路径上不要用 FUSE：它的每一次 IO 都要走两趟进程边界",
		},
		Unknown: "内核在这条链路上只是个转发者，真正的耗时在用户态进程里，/proc 看不到。",
		Sources: []string{
			"内核源码 fs/fuse/file.c",
			"内核源码 fs/fuse/dev.c",
		},
		Confidence: Certain,
	},

	// --- filesystem journals, sync and writeback --------------------------

	{
		Key:   "jbd2_log_wait_commit",
		Kind:  KindWChan,
		Alias: []string{"jbd2", "log_wait_commit", "jbd2_log_wait_commit_nested"},
		What:  "在等一次 ext4 日志提交完成。",
		Why: []string{
			"ext4 改元数据前先把“要改什么”写进日志，日志落盘之后改动才算数 —— 这个函数就在等那一次落盘",
			"应用在密集地做同步写：fsync、O_SYNC、大量的小文件创建与重命名",
			"日志所在的盘写延迟高（日志和数据在同一块盘上时最容易撞）",
			"一次提交里塞进太多操作，提交本身变慢",
		},
		Fix: []string{
			"先确认它真慢：iostat -x 1 看日志所在盘的 await 与 util",
			"减少 fsync 次数是唯一真正的解法：数据库的组提交、消息队列的批量刷盘、把多次 fsync 合并成一次",
			"把日志挪到另一块盘上（ext4 默认把日志放在同一个文件系统里）",
			"换更快的盘能缩短单次提交，但改变不了“提交是同步的”这件事",
		},
		Sources: []string{
			"内核源码 fs/jbd2/commit.c",
			"内核文档 Documentation/filesystems/ext4/journal.rst",
		},
		Confidence: Certain,
	},
	{
		Key:   "jbd2_journal_commit_transaction",
		Kind:  KindWChan,
		Alias: []string{"kjournald2"},
		What:  "jbd2 内核线程正在提交 ext4 日志。",
		Why: []string{
			"这是提交的实际执行者：等它的线程很多时，卡住的是它的盘",
			"大事务（一次性删很多文件、truncate 大文件）会让一次提交变长",
		},
		Fix: []string{
			"看它自己在等什么：它的 wchan 通常就是 io_schedule，问题在那块盘上",
			"避免一次性的大批量元数据操作（比如百万小文件的 rm -rf），分批做",
		},
		Sources: []string{
			"内核源码 fs/jbd2/commit.c",
			"内核文档 Documentation/filesystems/ext4/journal.rst",
		},
		Confidence: Certain,
	},
	{
		Key:   "xfs_log_force",
		Kind:  KindWChan,
		Alias: []string{"xfs_log_force_lsn", "xlog_state_get_iclog_space", "xfs_buf_lock"},
		What:  "在等 XFS 把日志强制刷到盘上。",
		Why: []string{
			"XFS 的日志是环形缓冲：写满了必须强制刷出去，写的人就得等",
			"大事务、大批量的元数据改动会把日志撑满",
			"日志所在的盘慢（和数据共盘时）",
		},
		Fix: []string{
			"XFS 的日志大小是建文件系统时定的，太小会频繁触发强制刷盘（xfs_info 可以看）",
			"给日志单独一块快的设备（XFS 支持外部日志设备）",
			"减少 fsync 与大批量元数据操作，和 ext4 一样",
		},
		Sources: []string{
			"内核源码 fs/xfs/xfs_log.c",
			"内核文档 Documentation/filesystems/xfs/",
		},
		Confidence: Certain,
	},
	{
		Key:   "ext4_sync_file",
		Kind:  KindWChan,
		Alias: []string{"do_fsync", "vfs_fsync", "vfs_fsync_range", "generic_write_sync"},
		What:  "在等一次 fsync 落盘。",
		Why: []string{
			"应用明确要求数据落盘：数据库的 redo log、消息队列的持久化、配置文件的原子替换",
			"fsync 的语义就是“等我确定写下去了再返回”，所以它天然要等一次真实的往返",
		},
		Fix: []string{
			"先分清是这个应用的正常业务量，还是它在过度 fsync。同样是落盘，一次提交一条和一次提交一千条差三个数量级",
			"能改应用的改成组提交；改不了应用的，把盘换成写延迟低的（这一步见效最直接）",
			"别用 data=journal 之外的错误方式去“优化”：把 fsync 改成不落盘就是拿数据换数字",
		},
		Sources: []string{
			"内核源码 fs/ext4/fsync.c",
			"内核源码 fs/sync.c（vfs_fsync_range）",
			"man 2 fsync",
		},
		Confidence: Certain,
	},
	{
		Key:   "balance_dirty_pages",
		Kind:  KindWChan,
		Alias: []string{"balance_dirty_pages_ratelimited", "dirty_pause"},
		What:  "写入者被内核按住，等脏页降到限以下。",
		Why: []string{
			"这个进程写盘的速度超过了内核后台刷盘的速度，脏页堆到 vm.dirty_ratio 附近，内核只能把写入者按停",
			"这是限流，不是盘坏了 —— 内核在拿延迟换内存不被写爆",
		},
		Fix: []string{
			"把 vm.dirty_background_ratio 调小，让后台刷盘更早开始：这是关键的一步",
			"不要靠调大 vm.dirty_ratio 来解决 —— 那只会让积压更深，停顿时更长",
			"用 cgroup v2 的 io.max 给这个进程限速，让它自己慢下来而不是被内核按住",
			"降低写入速率本身，或者把盘换成能跟上写入量的",
		},
		Unknown: "这是内核主动限流，不是磁盘忙碌：iostat 上这块盘可能并不满，光看盘的指标找不到问题在这里。",
		Sources: []string{
			"内核源码 mm/page-writeback.c",
			"内核文档 Documentation/admin-guide/sysctl/vm.rst（dirty_background_ratio、dirty_bytes、dirty_ratio）",
		},
		Confidence: Certain,
	},
	{
		Key:   "wait_on_page_writeback",
		Kind:  KindWChan,
		Alias: []string{"folio_wait_writeback", "wait_on_page_writeback_killable"},
		What:  "在等某一页写完。",
		Why: []string{
			"内核正在把这一页刷出去，有人需要它落到盘上才能继续",
			"读一个正在被写的文件、或者对一个刚写完的区域做一致性操作时会碰到",
		},
		Fix: []string{
			"单看这个符号只能说“有一页在写”。它自己不是结论，要看整机上还有多少线程在等同一块盘",
			"要定位到具体是谁，看线程持有的文件与它打开的 fd",
		},
		Sources: []string{
			"内核源码 mm/page-writeback.c",
			"内核源码 mm/filemap.c",
		},
		Confidence: Rough,
	},
	{
		Key:   "wait_on_page_bit",
		Kind:  KindWChan,
		Alias: []string{"folio_wait_bit", "folio_wait_bit_common", "__folio_lock", "wait_on_page_locked"},
		What:  "在等某一页的就绪状态（锁或回写）。",
		Why: []string{
			"大多数情况是在等这一页的写回完成",
			"也可能是等另一个线程放掉这一页的锁 —— 那就变成页级锁竞争，不是 IO 问题",
		},
		Fix: []string{
			"用它自己的 wchan 区分：如果展开后的等待链指向回写，就是 IO；指向锁，就是竞争",
			"页级锁竞争通常是同一个文件被很多线程同时读写，改数据布局比调内核有效",
		},
		Unknown: "同一个符号同时覆盖“等 IO”和“等锁”两件事，plumb 从 wchan 一个字段分不出来。",
		Sources: []string{
			"内核源码 mm/filemap.c（folio_wait_bit_common）",
		},
		Confidence: Rough,
	},
	{
		Key:   "filemap_fault",
		Kind:  KindWChan,
		Alias: []string{"do_fault", "handle_mm_fault"},
		What:  "缺页，要把数据从盘上读回来。",
		Why: []string{
			"内存里的文件页已经被回收掉了，进程再访问就要重新读回来",
			"这是内存不够的直接证据：文件缓存被挤出去，读延迟从微秒变成毫秒",
		},
		Fix: []string{
			"加内存，或者把别的进程的工作集压下去（cgroup v2 的 memory.max）",
			"对延迟敏感的少量文件可以用 MAP_POPULATE 或者提前读，把缺页挪到启动时",
			"注意它的表现是随机尖刺而不是持续变慢 —— 平均值看不出来，看尾部延迟",
		},
		Sources: []string{
			"内核源码 mm/filemap.c（filemap_fault）",
			"内核源码 mm/memory.c",
		},
		Confidence: Certain,
	},
	{
		Key:   "btrfs_commit_transaction",
		Kind:  KindWChan,
		Alias: []string{"btrfs_", "btrfs", "btrfs_wait_ordered_extents", "btrfs_tree_read_lock"},
		What:  "在等一次 btrfs 事务提交或 extent 落盘。",
		Why: []string{
			"btrfs 的写入都进事务，提交时要落元数据",
			"大量写之后的一次提交会很重；snapshot 越多、extent 越碎越明显",
		},
		Fix: []string{
			"先用 btrfs filesystem df 看空间是不是快满了（btrfs 的“还有空间”和实际可用不是一回事）",
			"碎片严重时做一次 balance：btrfs balance start -dusage=50 /",
			"别在 btrfs 上开 autodefrag 跑数据库负载",
		},
		Sources: []string{
			"内核源码 fs/btrfs/transaction.c",
			"内核源码 fs/btrfs/extent_io.c",
		},
		Confidence: Certain,
	},
	{
		Key:   "ksys_sync",
		Kind:  KindWChan,
		Alias: []string{"sync_filesystems", "iterate_supers", "sync_inodes_sb", "do_sync"},
		What:  "在等一次整机 sync 把所有脏数据写完。",
		Why: []string{
			"有人（或者某个备份、快照、卸载脚本）调了 sync(2)",
			"内核的定期回写任务也在走这条路",
		},
		Fix: []string{
			"sync 要等所有文件系统都写完，慢是它的定义，不是故障",
			"但如果有脚本频繁调 sync，那就是在人为制造全局停顿：改成针对具体文件 fsync",
		},
		Sources: []string{
			"内核源码 fs/sync.c（ksys_sync、sync_filesystems、iterate_supers）",
			"man 2 sync",
		},
		Confidence: Certain,
	},

	// --- memory reclaim ---------------------------------------------------

	{
		Key:   "try_to_free_pages",
		Kind:  KindWChan,
		Alias: []string{"do_try_to_free_pages", "direct_reclaim", "balance_pgdat", "shrink_zones"},
		What:  "在分配内存时被迫自己动手回收。",
		Why: []string{
			"要内存的时候发现不够，等不及后台回收，只能在分配路径上现场回收",
			"这是内存压力的“直接”路径：走这条路的停顿是毫秒级的，会直接变成应用延迟",
			"cgroup 的内存上限被打满也会走这里",
		},
		Fix: []string{
			"加内存是根治；在加之前，用 cgroup v2 的 memory.max 把不重要的负载圈起来",
			"看 /proc/pressure/memory 的 full 值：它高说明整机都在等内存，不是某个进程的事",
			"把交换分区打开一点能缓解，但换页本身也会拖慢 —— 它把内存问题换成了 IO 问题",
		},
		Sources: []string{
			"内核源码 mm/vmscan.c（try_to_free_pages、do_try_to_free_pages）",
			"内核源码 mm/page_alloc.c",
			"内核文档 Documentation/accounting/psi.rst",
		},
		Confidence: Certain,
	},
	{
		Key:   "shrink_inactive_list",
		Kind:  KindWChan,
		Alias: []string{"shrink_active_list", "shrink_lruvec", "shrink_page_list"},
		What:  "在从 LRU 上回收一批页。",
		Why: []string{
			"回收的主力函数：扫 LRU、挑牺牲品、把它们写出去或丢掉",
			"有大量脏页要回收时，这一趟会很久 —— 回收必须先把脏页写出去才能收",
		},
		Fix: []string{
			"回收慢经常只是症状，真正的原因是脏页太多或者设备慢（看 /proc/vmstat 的 pgscan 与 allocstall）",
			"pgscan 远大于 pgsteal 说明扫了很多却收不下来：内存里全是不可回收的匿名页，这时加内存是唯一解",
		},
		Sources: []string{
			"内核源码 mm/vmscan.c",
			"内核文档 Documentation/filesystems/proc.rst（/proc/vmstat 字段）",
		},
		Confidence: Certain,
	},
	{
		Key:   "shrink_node",
		Kind:  KindWChan,
		Alias: []string{"kswapd", "kswapd0", "shrink_node_memcgs"},
		What:  "kswapd 后台回收线程在回收内存。",
		Why: []string{
			"空闲内存跌破水位线，内核开始后台回收 —— 这是正常的、健康的机制",
			"但它一直在跑，说明内存长期紧张，只是还没到卡住分配的地步",
		},
		Fix: []string{
			"kswapd 在跑本身不是问题，是它一直跑不完才是问题",
			"对比 /proc/pressure/memory：some 高而 full 低，说明有人在等但没到整机停摆 —— 这时候调优还来得及",
		},
		Sources: []string{
			"内核源码 mm/vmscan.c（kswapd、shrink_node）",
			"内核文档 Documentation/accounting/psi.rst",
		},
		Confidence: Certain,
	},
	{
		Key:   "shrink_slab",
		Kind:  KindWChan,
		Alias: []string{"do_shrink_slab", "shrink_slab_memcg", "drop_slab", "prune_icache_sb"},
		What:  "在回收内核对象缓存（slab）。",
		Why: []string{
			"页缓存不够了，内核开始收 dentry、inode 这类对象缓存",
			"某些内核模块的 slab 只增不减，或者回收器很慢（历史上就有过这类问题）",
		},
		Fix: []string{
			"先看是谁占着：slabtop -o 或者 /proc/slabinfo 里找最大的那几项",
			"如果某一项疯狂增长，那是内核里的内存泄漏，加内存只能推迟问题",
			"dentry/inode 缓存大通常是正常的，它本来就是拿来用的",
		},
		Sources: []string{
			"内核源码 mm/vmscan.c（6.7 起拆到 mm/shrinkers.c）",
			"内核源码 mm/slab_common.c",
		},
		Confidence: Rough,
	},
	{
		Key:   "__alloc_pages_slowpath",
		Kind:  KindWChan,
		Alias: []string{"__alloc_pages", "prepare_alloc_pages", "get_page_from_freelist"},
		What:  "内存分配走了慢路径。",
		Why: []string{
			"快路径（直接从空闲链表拿页）失败了，才进到这里：要么回收、要么整理碎片、要么等",
			"碎片化也会走到这里 —— 空闲内存总量看着还有，但凑不出连续的页",
		},
		Fix: []string{
			"看 /proc/vmstat 的 allocstall_* 计数在不在涨，涨就说明确实在分配路径上等",
			"碎片导致的大页分配失败，看 compaction 相关计数",
		},
		Sources: []string{
			"内核源码 mm/page_alloc.c（__alloc_pages_slowpath）",
			"内核文档 Documentation/filesystems/proc.rst（/proc/vmstat）",
		},
		Confidence: Certain,
	},
	{
		Key:   "try_to_free_mem_cgroup_pages",
		Kind:  KindWChan,
		Alias: []string{"mem_cgroup_reclaim", "shrink_memcg", "mem_cgroup_oom", "charge_memcg"},
		What:  "在给 cgroup 的账本回收内存。",
		Why: []string{
			"这个 cgroup 的 memory.max 打满了，它只能在记账的范围内回收",
			"容器化的环境里这是最常见的内存压力来源：整机还有很多空闲，但这个容器已经到顶了",
		},
		Fix: []string{
			"看这个 cgroup 的 memory.current / memory.max，以及 memory.events 里的 high、max、oom 计数",
			"memory.pressure 是这个 cgroup 自己的 PSI —— 比整机指标准得多",
			"调大限额，或者把里面的进程工作集压小",
		},
		Sources: []string{
			"内核源码 mm/memcontrol.c",
			"内核文档 Documentation/admin-guide/cgroup-v2.rst（memory 控制器）",
		},
		Confidence: Certain,
	},
	{
		Key:   "compact_zone",
		Kind:  KindWChan,
		Alias: []string{"compaction", "__compact_pgdat", "kcompactd", "migrate_pages", "isolate_migratepages"},
		What:  "在整理内存碎片，把页挪到一起。",
		Why: []string{
			"要一块连续的物理内存（大页、驱动的 DMA 缓冲）但拿不到，只能现场搬页",
			"长时间运行、反复分配释放的系统碎片会越来越重",
			"透明大页（THP）会主动触发整理",
		},
		Fix: []string{
			"看 /proc/vmstat 的 compact_stall 和 compact_fail：fail 涨说明整理也没用，碎片已经很严重",
			"重启是最直接的办法；长期看要减少长时间运行的进程的反复分配",
			"对延迟敏感的场景可以关掉 THP 的 always：/sys/kernel/mm/transparent_hugepage/enabled 改成 madvise",
		},
		Sources: []string{
			"内核源码 mm/compaction.c",
			"内核文档 Documentation/admin-guide/mm/transhuge.rst",
		},
		Confidence: Certain,
	},
	{
		Key:   "collapse_huge_page",
		Kind:  KindWChan,
		Alias: []string{"khugepaged", "__collapse_huge_page", "__collapse_huge_page_isolate"},
		What:  "khugepaged 在把普通页折叠成大页。",
		Why: []string{
			"透明大页的后台折叠线程：它需要先锁住一段地址区间再搬页，期间进程会短暂停住",
			"这是少数“内核线程让别人停下”的典型场景，停顿能到几十毫秒",
		},
		Fix: []string{
			"对延迟敏感的服务，把 THP 从 always 改成 madvise，或者关掉 khugepaged",
			"关掉要接受代价：失去大页带来的 TLB 收益，某些负载吞吐会降",
			"别只看平均值 —— 这类停顿只在尾部延迟里看得见",
		},
		Sources: []string{
			"内核源码 mm/khugepaged.c",
			"内核文档 Documentation/admin-guide/mm/transhuge.rst",
		},
		Confidence: Certain,
	},

	// --- locks ------------------------------------------------------------

	{
		Key:   "rwsem_down_write_slowpath",
		Kind:  KindWChan,
		Alias: []string{"rwsem_down_write_failed", "rwsem_optimistic_spin"},
		What:  "在等一把内核读写锁的写权限。",
		Why: []string{
			"有别的线程持着读锁或写锁不放，写者必须等所有人放开",
			"写者优先的语义下，等着的写者还会阻塞后来的读者，容易放大成一片区域一起停住",
		},
		Fix: []string{
			"这是内核里的竞争，从 /proc 看不出具体的锁 —— 要知道是哪把锁得用 ftrace 的 lock 事件",
			"实际能做的通常是减少争用：把多个进程读同一份东西改成各自一份，或者降低并发度",
		},
		Unknown: "具体是哪一把锁、谁持着它，/proc 里没有 —— 需要 ftrace 或者内核探针才能看到。",
		Sources: []string{
			"内核源码 kernel/locking/rwsem.c",
		},
		Confidence: Rough,
	},
	{
		Key:   "rwsem_down_read_slowpath",
		Kind:  KindWChan,
		Alias: []string{"rwsem_down_read_failed"},
		What:  "在等一把内核读写锁的读权限。",
		Why: []string{
			"写者在排队时会拦住读者（写者优先），所以读者也会停在这里",
			"一个长时间的写操作能拖住一大群读者",
		},
		Fix: []string{
			"找那个长时间的写操作，而不是找读者",
			"同样的符号在整机上出现很多时，值得用 ftrace 看一眼是谁在持有",
		},
		Unknown: "持锁者是谁，/proc 不告诉你。",
		Sources: []string{
			"内核源码 kernel/locking/rwsem.c",
		},
		Confidence: Rough,
	},
	{
		Key:   "__mutex_lock",
		Kind:  KindWChan,
		Alias: []string{"mutex_lock", "mutex_lock_interruptible", "__mutex_lock_slowpath", "mutex_lock_killable"},
		What:  "在等一把内核互斥锁。",
		Why: []string{
			"内核里有别的线程持着这把锁，而且它做的事不是很快",
			"典型来源：驱动、文件系统、网络栈里的串行化点",
		},
		Fix: []string{
			"内核锁竞争不好从外部调：能做的通常是降低并发，或者换掉触发它的驱动/子系统",
			"要看具体是哪把锁，用 ftrace 的 lock 事件或者 /proc/lock_stat（需要打开 lockdep 的统计）",
		},
		Unknown: "plumb 只看到“在等锁”，看不到锁的名字和持有者。",
		Sources: []string{
			"内核源码 kernel/locking/mutex.c",
			"内核源码 Documentation/locking/lockstat.rst",
		},
		Confidence: Rough,
	},
	{
		Key:   "down_write",
		Kind:  KindWChan,
		Alias: []string{"down_read", "down", "down_killable", "__down"},
		What:  "在等一把内核信号量。",
		Why: []string{
			"信号量适合“允许 N 个人同时进”的场景，等它通常意味着某个内核路径在长时间占用",
			"模块卸载、设备热插拔、挂载卸载这些操作会持有信号量很久",
		},
		Fix: []string{
			"如果这个等待和某次设备操作同时出现，先怀疑那次操作",
			"频繁出现说明有个内核路径的临界区太长，那要改的是驱动或子系统，不是参数",
		},
		Sources: []string{
			"内核源码 kernel/locking/semaphore.c",
		},
		Confidence: Rough,
	},
	{
		Key:   "_raw_spin_lock",
		Kind:  KindWChan,
		Alias: []string{"queued_spin_lock_slowpath", "native_queued_spin_lock_slowpath", "spin_lock"},
		What:  "在自旋等一把内核自旋锁。",
		Why: []string{
			"自旋锁的临界区本该极短，等在这里说明有人的临界区过长",
			"在虚拟化环境里，持有者被宿主机抢走了 CPU，自旋会白等很久",
		},
		Fix: []string{
			"单看一个符号说明不了太多，要看它是不是成片出现",
			"成片出现且 CPU 不饱和，怀疑虚拟化的 CPU 调度（看 steal 那一项）",
		},
		Unknown: "自旋等待可能只是“拿锁的人被调度走了”，和锁本身无关 —— 这一点 /proc 分不出来。",
		Sources: []string{
			"内核源码 kernel/locking/qspinlock.c",
			"内核源码 kernel/locking/spinlock.c",
		},
		Confidence: Rough,
	},
	{
		Key:   "futex_wait",
		Kind:  KindWChan,
		Alias: []string{"do_futex", "futex_wait_queue_me", "__futex_wait", "futex_lock_pi"},
		What:  "在等应用自己的锁。",
		Why: []string{
			"futex 是用户态的锁：pthread mutex、Java 的对象锁、Go 的 channel、数据库内部的闩锁都落在它上面",
			"内核只是那个帮忙排队的，锁本身在应用里",
		},
		Fix: []string{
			"这是应用自己的问题，不要去调内核参数：修的是代码或配置（连接池大小、锁粒度、线程数）",
			"先找出是谁持着锁：应用自己的 profile、pprof、或者 per-thread 的栈",
			"注意：G 状态之外，等 futex 是 S 不是 D。如果 plumb 把它报成被堵住，那是采样瞬间的信号量语义，看上下文",
		},
		Unknown: "锁在哪、谁持着、持了多久，全在应用里，/proc 只能告诉你它在等。",
		Sources: []string{
			"内核源码 kernel/futex/waitwake.c（旧内核为 kernel/futex.c）",
			"man 2 futex",
		},
		Confidence: Certain,
	},
	{
		Key:   "synchronize_rcu",
		Kind:  KindWChan,
		Alias: []string{"rcu_barrier", "wait_rcu_gp", "synchronize_rcu_expedited", "rcu_gp_kthread"},
		What:  "在等一个 RCU 宽限期结束。",
		Why: []string{
			"内核要在“所有 CPU 都不再引用老数据”之后才能释放它",
			"如果有 CPU 长时间不经过静止点（被实时任务占着、或者一直关中断），宽限期就会拖长",
		},
		Fix: []string{
			"查是不是有实时优先级的任务把 CPU 占住了（chrt、或者内核里的 RT 线程）",
			"虚拟化环境里 CPU 被偷走也会拖长宽限期",
			"这个符号出现在内核线程上通常是正常的，出现在业务进程上才值得追",
		},
		Sources: []string{
			"内核源码 kernel/rcu/tree.c",
			"内核源码 Documentation/RCU/",
		},
		Confidence: Rough,
	},

	// --- network ----------------------------------------------------------

	{
		Key:   "sk_stream_wait_memory",
		Kind:  KindWChan,
		Alias: []string{"sk_stream_wait_connect", "sk_wait_pending"},
		What:  "TCP 发送缓冲满了，在等有空位。",
		Why: []string{
			"发送窗口用完：对端读得慢，或者中间链路拥塞",
			"本机的发送缓冲区太小也会这样",
		},
		Fix: []string{
			"先分清是谁的问题：如果对端读得慢，本机调多大都没用 —— 这类等待可能根本不该记在这台机器头上",
			"连接层面的症状：ss -ti 看发送队列和重传超时",
			"确认真的是本机缓冲不够，再看 net.ipv4.tcp_wmem",
		},
		Unknown: "原因可能完全在另一台机器或者链路上。这台机器的 /proc 只能说它在等，说不出对面怎么了。",
		Sources: []string{
			"内核源码 net/core/stream.c（sk_stream_wait_memory）",
			"man 7 tcp",
		},
		Confidence: Certain,
	},
	{
		Key:   "tcp_sendmsg",
		Kind:  KindWChan,
		Alias: []string{"tcp_sendmsg_locked", "tcp_write_xmit", "tcp_push"},
		What:  "在 TCP 发送路径上等。",
		Why: []string{
			"发送路径拥塞：要么是对端，要么是本机队列，要么是链路",
			"也常和 sk_stream_wait_memory 一起出现（它就是被它等的那一步）",
		},
		Fix: []string{
			"用 ss -ti 看这个连接的重传、cwnd、rtt —— 这三个数能区分本机缓冲和链路问题",
			"是链路问题就用 tc / 网络设备侧的计数器确认丢包，别在本机调参数",
		},
		Sources: []string{
			"内核源码 net/ipv4/tcp.c",
			"man 8 ss",
		},
		Confidence: Certain,
	},
	{
		Key:   "tcp_recvmsg",
		Kind:  KindWChan,
		Alias: []string{"tcp_recvmsg_locked", "sk_wait_data"},
		What:  "在等 TCP 数据到达。",
		Why: []string{
			"这是阻塞读的常态：没有数据就等，是正常的",
			"只有当它和“服务变慢”同时出现时才是问题 —— 那说明上游没发数据",
		},
		Fix: []string{
			"多数情况下不用管它。要查的是上游为什么不发数据",
			"如果是本机在读超时后才收到数据，看对端的发送节奏和网络的延迟抖动",
		},
		Sources: []string{
			"内核源码 net/ipv4/tcp.c",
		},
		Confidence: Certain,
	},
	{
		Key:   "__skb_wait_for_more_packets",
		Kind:  KindWChan,
		Alias: []string{"skb_wait_for_more_packets", "__sk_wait_data", "unix_stream_recvmsg"},
		What:  "套接字读缓冲空了，在等更多包。",
		Why: []string{
			"同样是阻塞读的常态，属于正常等待",
			"从 /proc 的角度看，它和“服务卡住”长得一样，要靠上下文区分",
		},
		Fix: []string{
			"看这个进程是不是本来就该在等（比如一个空闲的连接处理线程）",
			"如果它同时是压力来源之一，去看上游的发送端",
		},
		Sources: []string{
			"内核源码 net/core/datagram.c（__skb_wait_for_more_packets）",
		},
		Confidence: Rough,
	},
	{
		Key:   "inet_csk_accept",
		Kind:  KindWChan,
		Alias: []string{"inet_csk_wait_for_connect", "inet_wait_for_connect", "__sys_accept4"},
		What:  "服务端在等一个新连接进来。",
		Why: []string{
			"accept 的常态：没有新连接就等，完全正常",
			"如果它和“请求变慢”同时出现，问题在更前面的地方（队列、防火墙、上游）",
		},
		Fix: []string{
			"不用管它。要看的是：请求有没有到（看 accept 队列和 SYN 队列有没有溢出，ss -lnt 的 Send-Q）",
			"队列溢出的计数器在 /proc/net/netstat 里（ListenOverflows、ListenDrops）",
		},
		Sources: []string{
			"内核源码 net/ipv4/inet_connection_sock.c",
			"内核文档 Documentation/networking/（/proc/net/netstat 的 ListenOverflows）",
		},
		Confidence: Certain,
	},
	{
		Key:   "inet_wait_for_clear_once",
		Kind:  KindWChan,
		Alias: []string{"inet_csk_wait_for_close", "tcp_close", "__inet_wait_for_clear"},
		What:  "在等一条 TCP 连接彻底消失。",
		Why: []string{
			"TIME_WAIT 的等待：主动关闭方要等 2MSL 才能复用这个四元组",
			"短连接很多的服务上，这一层会被大量占用",
		},
		Fix: []string{
			"确认是不是短连接风暴：看 ss -s 的 TIME_WAIT 数量",
			"能从应用层改成连接复用（keepalive、连接池）最好；调 tcp_tw_reuse 是退而求其次，且只在特定条件下允许",
			"不要用 tcp_tw_recycle —— 它在新内核里已经被移除了（对 NAT 后的客户端有害）",
		},
		Sources: []string{
			"内核源码 net/ipv4/inet_timewait_sock.c",
			"man 7 tcp（TIME_WAIT 与 tcp_tw_reuse）",
		},
		Confidence: Certain,
	},
	{
		Key:   "unix_stream_read_generic",
		Kind:  KindWChan,
		Alias: []string{"unix_stream_read_actor", "unix_dgram_recvmsg"},
		What:  "在等一个 Unix socket 的数据。",
		Why: []string{
			"本机的进程间通信：数据库的连接、容器运行时、日志转发都在用",
			"对端没写、或者写得太慢",
		},
		Fix: []string{
			"多数是常态。要看的是对端进程是不是也在等（两个进程互相等就是死锁或背压设计问题）",
			"本地 socket 也会堵：看 ss -x 的 Recv-Q / Send-Q，缓冲区满了就是消费端跟不上",
		},
		Sources: []string{
			"内核源码 net/unix/af_unix.c",
		},
		Confidence: Certain,
	},

	// --- waits that are not problems --------------------------------------
	//
	// These are in the tree because a short report cannot explain why they are
	// not mentioned, and the first question a reader has about any monitor is
	// "why is this not a problem". Answering that once, properly, is cheaper
	// than answering it every time it comes up.

	{
		Key:   "ep_poll",
		Kind:  KindWChan,
		Alias: []string{"do_epoll_wait", "epoll_wait", "__x64_sys_epoll_wait"},
		What:  "事件循环在等下一个事件。",
		Why: []string{
			"这是健康状态：几乎每个网络服务的主循环都停在这里",
			"它占着线程时间不等于服务慢 —— 它在等，不是在忙",
		},
		Fix: []string{
			"不用处置。看到它出现在进程列表里是正常的",
			"要关注的是同一批进程里有没有别的线程在等盘、等锁或等内存",
		},
		Sources: []string{
			"内核源码 fs/eventpoll.c",
			"man 2 epoll_wait",
		},
		Confidence: Certain,
	},
	{
		Key:   "poll_schedule_timeout",
		Kind:  KindWChan,
		Alias: []string{"do_sys_poll", "__poll", "select"},
		What:  "在等文件描述符就绪（poll/select）。",
		Why: []string{
			"和 epoll 一样，是等待而不是问题",
			"老代码里用 select 的还不少，它在大并发下有额外的开销，但那不是卡顿",
		},
		Fix: []string{
			"不用处置",
		},
		Sources: []string{
			"内核源码 fs/select.c",
			"man 2 poll",
		},
		Confidence: Certain,
	},
	{
		Key:   "nanosleep",
		Kind:  KindWChan,
		Alias: []string{"hrtimer_nanosleep", "schedule_hrtimeout", "do_nanosleep", "clock_nanosleep"},
		What:  "在按计划休眠。",
		Why: []string{
			"进程主动 sleep：轮询间隔、退避重试、定时任务",
			"这是它自己想停，不是被别人挡住",
		},
		Fix: []string{
			"不用处置。如果堆积太多，看的是轮询频率设计得合不合理",
		},
		Sources: []string{
			"内核源码 kernel/time/hrtimer.c",
			"man 2 nanosleep",
		},
		Confidence: Certain,
	},
	{
		Key:   "do_wait",
		Kind:  KindWChan,
		Alias: []string{"wait4", "kernel_wait4", "__wait_for_common"},
		What:  "父进程在等子进程退出。",
		Why: []string{
			"任何 shell 脚本、构建、命令行工具调用都会出现",
			"它是父子关系，不是资源竞争",
		},
		Fix: []string{
			"不用处置",
		},
		Sources: []string{
			"内核源码 kernel/exit.c",
			"man 2 wait4",
		},
		Confidence: Certain,
	},
	{
		Key:   "pipe_read",
		Kind:  KindWChan,
		Alias: []string{"pipe_read_iter", "pipe_wait_readable"},
		What:  "在等管道另一端写入。",
		Why: []string{
			"管道、进程替换、shell 里的 | 都会出现",
			"常态；但两个进程互相等对方写就是死锁",
		},
		Fix: []string{
			"看它的对端是不是也在等（pipe_write）：两边都在等就是设计问题",
		},
		Sources: []string{
			"内核源码 fs/pipe.c",
			"man 7 pipe",
		},
		Confidence: Certain,
	},
	{
		Key:   "pipe_write",
		Kind:  KindWChan,
		Alias: []string{"pipe_write_iter", "pipe_wait_writable"},
		What:  "管道写满了，在等对端读走。",
		Why: []string{
			"管道容量默认只有 64 KiB，写端快、读端慢就会堵在这里",
			"这是典型的背压：写的人被按住，等读的人跟上",
		},
		Fix: []string{
			"这不是故障，是背压生效。要提吞吐得让读端更快",
			"也可以调大管道容量，但那只是把缓冲挪个位置，不解决读端慢",
		},
		Sources: []string{
			"内核源码 fs/pipe.c",
			"man 7 pipe（F_SETPIPE_SZ）",
		},
		Confidence: Certain,
	},
	{
		Key:   "msleep",
		Kind:  KindWChan,
		Alias: []string{"msleep_interruptible", "schedule_timeout", "schedule_timeout_uninterruptible"},
		What:  "内核代码在按计划休眠。",
		Why: []string{
			"驱动轮询硬件状态、子系统等一个固定时间",
			"它出现在内核线程上，不在你的业务进程里",
		},
		Fix: []string{
			"通常不用处置，是内核的正常节奏",
			"只有当你怀疑某个驱动时才有意义，那时要看的是 dmesg 和驱动版本",
		},
		Sources: []string{
			"内核源码 kernel/time/timer.c（msleep）",
			"内核源码 kernel/sched/core.c（schedule_timeout）",
		},
		Confidence: Certain,
	},
	{
		Key:   "wait_for_completion",
		Kind:  KindWChan,
		Alias: []string{"__wait_for_common", "wait_for_completion_io", "wait_for_completion_killable"},
		What:  "在等另一段代码宣布“做完了”。",
		Why: []string{
			"内核里的完成量：一个线程干活，另一个等它",
			"等谁干完了，才是真正的问题 —— 这个符号只说明有等待关系",
		},
		Fix: []string{
			"从 /proc 给不出处置：要知道等的是谁，得看它属于哪个子系统",
			"如果这个进程对应的是某个驱动或存储设备，怀疑最后的那个硬件",
		},
		Unknown: "plumb 看到的是“有人在等”，看不到等的是谁、要等多久。这一片只能帮你排除掉“它在等磁盘直连 IO”这种误判。",
		Sources: []string{
			"内核源码 kernel/sched/completion.c",
		},
		Confidence: NameOnly,
	},
}
