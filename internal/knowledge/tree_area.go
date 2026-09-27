package knowledge

// The area leaves. One per check plumb runs, keyed by the exact string the
// report prints.
//
// These carry the reasoning that the report has no room for: why the criterion
// is the right one, what it deliberately does not look at, and what to do when
// it fires. A recurring theme is worth stating once here rather than in every
// leaf — almost every area measures waiting rather than utilization, because
// utilization answers "how busy is the road" and waiting answers "how many
// people are stuck". A disk at 100% busy with nobody waiting is a disk doing its
// job.

var areaLeaves = []Leaf{
	{
		Key:  "磁盘 IO",
		Kind: KindArea,
		Alias: []string{
			"磁盘IO", "磁盘", "IO", "disk io", "io",
		},
		What: "这条判定看的是有多少时间在等磁盘，不是磁盘有多忙。",
		Why: []string{
			"plumb 读的是 /proc/pressure/io：它统计的是“有任务因为等 IO 而停住”的时间占比",
			"没有 PSI 的老内核退化成看 iowait。iowait 是个不精确的替代品，它把“等”和“闲”混在一起，所以 plumb 会明说精度降低了",
		},
		Fix: []string{
			"先看是谁在等：报告里会给出元凶进程和它持有的文件，那比任何图表都好用",
			"再决定是降并发还是换盘：await 高而队列浅说明盘真的慢，队列深说明是你压上去的",
			"脏页那一项一起看：如果脏页也在告警，问题可能根本不在盘上，而在写入节奏",
		},
		Unknown: "PSI 只统计“有人在等”，不区分等的是本地盘、远端存储还是网络块设备。要知道等的是哪个设备，得看进程持有的文件。",
		Sources: []string{
			"内核文档 Documentation/accounting/psi.rst",
			"内核文档 Documentation/filesystems/proc.rst（/proc/stat 的 iowait）",
			"内核源码 mm/filemap.c、block/blk-core.c",
		},
		Confidence: Certain,
	},
	{
		Key:   "内存",
		Kind:  KindArea,
		Alias: []string{"memory", "mem", "内存压力"},
		What:  "这条判定看的是内存压力，不是内存用了多少。",
		Why: []string{
			"plumb 读 /proc/pressure/memory：它统计的是任务因为等内存（回收、换页）而停住的时间",
			"没启 PSI 时退化成看使用率，而使用率是个会骗人的指标：Linux 会把空闲内存拿去做页缓存，所以“用了 95%”通常完全正常",
			"真正要区分的是这两种状态：占用高但没人等（健康）和真的卡住（不健康）",
		},
		Fix: []string{
			"看 MemAvailable 而不是 MemFree 或 used：内核文档自己说这个字段是估算，但它比 free 接近事实得多",
			"用 cgroup v2 的 memory.max 把不重要的负载圈起来，比整机加内存更早见效",
			"换页（swap）活跃的时候，问题会从内存变成磁盘 —— 两个判定要一起看",
		},
		Unknown: "内存的“估算”是真的估算。内核文档明说 MemAvailable 的影响因素因系统而异，它不是一个能精确到 MB 的数。",
		Sources: []string{
			"内核文档 Documentation/accounting/psi.rst（some 与 full 的定义）",
			"内核文档 Documentation/filesystems/proc.rst（MemAvailable）",
			"内核源码 mm/show_mem.c（si_mem_available）",
		},
		Confidence: Certain,
	},
	{
		Key:   "CPU",
		Kind:  KindArea,
		Alias: []string{"处理器", "cpu 压力"},
		What:  "这条判定看的是有任务在等 CPU 跑，不是 CPU 跑得有多满。",
		Why: []string{
			"/proc/pressure/cpu 的 some 值回答的是“有多少时间至少有一个任务在排队等 CPU”",
			"CPU 跑到 100% 本身不是问题 —— 花钱买的 CPU 就该被用满。问题是排队等它",
			"另一个常被误读的指标是 load average：统计口径见 /proc/loadavg 那一片",
		},
		Fix: []string{
			"先在报告里看进程：是不是单个进程吃满了，还是一个进程起了太多线程在抢",
			"看整机 busy 和逐核忙闲的差：如果只有一两个核满，那更像是单线程瓶颈，加核没用，要改代码",
			"确实需要加算力的时候，先确认不是被自己的锁或者 IO 拖住造成的假象",
		},
		Sources: []string{
			"内核文档 Documentation/accounting/psi.rst",
			"内核源码 kernel/sched/",
		},
		Confidence: Certain,
	},
	{
		Key:   "CPU 被抢占",
		Kind:  KindArea,
		Alias: []string{"steal", "被抢占", "CPU被抢占", "cpu steal"},
		What:  "这台虚机的 CPU 时间被宿主机拿走了。",
		Why: []string{
			"steal 是 /proc/stat 里的一项：虚拟机想跑但热点被宿主机分给别人了",
			"这不是负载高，是“你的钱买的时间被借走了”",
		},
		Fix: []string{
			"这台机器上你能做的只有一件事：确认它不是被自己的资源请求引起的（比如开了突发实例、或者超卖了配额）",
			"剩下的都在宿主那边：找云厂商或者宿主管理员，这不是能在客户机里调参数解决的问题",
			"换机型、换可用区、或者换服务商，是实际可选的路径",
		},
		Unknown: "plumb 只能告诉你被抢走了多少，看不到是谁抢的、宿主机上还有谁在跑。",
		Sources: []string{
			"内核文档 Documentation/filesystems/proc.rst（/proc/stat 的 steal 字段）",
		},
		Confidence: NameOnly,
	},
	{
		Key:   "交换分区",
		Kind:  KindArea,
		Alias: []string{"swap", "交换", "swap分区"},
		What:  "在拿磁盘当内存用。",
		Why: []string{
			"内存不够时内核把不常用的匿名页写到交换分区，再读回来",
			"交换分区的延迟是内存的成千上万倍，所以一旦频繁换页，延迟就失控了",
		},
		Fix: []string{
			"先分清是“用过”还是“在用”：SwapUsed 高不一定在换页，看 Si/So 或者 /proc/vmstat 的 pswpin/pswpout 在不在持续增长",
			"持续换页就是内存不够。加内存是根治",
			"调低 vm.swappiness 能让内核更不情愿换出，但它不能凭空变出内存，只把换页推迟",
		},
		Sources: []string{
			"内核文档 Documentation/admin-guide/sysctl/vm.rst（swappiness）",
			"内核文档 Documentation/filesystems/proc.rst（/proc/meminfo 的 SwapCached、/proc/vmstat 的 pswpin/pswpout）",
		},
		Confidence: Certain,
	},
	{
		Key:   "磁盘空间",
		Kind:  KindArea,
		Alias: []string{"磁盘容量", "空间", "disk space", "df"},
		What:  "挂载点的剩余空间不够了。",
		Why: []string{
			"用 statfs(2) 读的是文件系统的空闲块",
			"plumb 用的是 f_bavail（普通用户可用），不是 f_bfree —— 差的正是 root 保留块，用 f_bfree 会看到一个普通进程根本用不到的数字",
		},
		Fix: []string{
			"先确认是真的没有，还是被删除但仍被进程打开的文件占着：lsof +L1 是唯一能看出这一点的办法",
			"inode 用尽和空间用尽在 df 里长得不一样，要看 df -i",
			"ext4 的 root 保留块（默认 5%）可以用 tune2fs -m 调小，但要知道那是在给普通用户让出位置",
		},
		Sources: []string{
			"man 2 statfs（f_bavail 与 f_bfree 的区别）",
			"man 1 df",
		},
		Confidence: Certain,
	},
	{
		Key:   "脏页",
		Kind:  KindArea,
		Alias: []string{"dirty", "脏页堆积", "writeback"},
		What:  "已经改过但还没写回磁盘的数据积压了。",
		Why: []string{
			"数据先写在内存里，过一会儿再刷到盘上。积压说明写的速度超过了刷的速度",
			"积压到 vm.dirty_ratio 附近，内核就会把写入者按停来限流 —— 表现是应用突然卡一下，而不是变慢",
			"脏页本身不是故障，是缓冲；缓冲打满才是",
		},
		Fix: []string{
			"调小 vm.dirty_background_ratio，让后台刷盘更早开始 —— 这是最有效的一步",
			"不要调大 vm.dirty_ratio 来解决：它只是把爆发推迟并放大",
			"写入速率压不下来的话，用 cgroup v2 的 io.max 给那个进程限速",
		},
		Sources: []string{
			"内核文档 Documentation/admin-guide/sysctl/vm.rst（dirty_background_ratio、dirty_ratio、dirty_bytes）",
			"内核文档 Documentation/filesystems/proc.rst（Dirty、Writeback）",
		},
		Confidence: Certain,
	},
	{
		Key:   "被堵住的线程",
		Kind:  KindArea,
		Alias: []string{"被堵住", "D状态", "blocked", "阻塞"},
		What:  "有线程进了不可中断睡眠，在等存储或内存。",
		Why: []string{
			"D 状态的线程连 kill -9 都杀不掉，只能等它自己回来 —— 这是最硬的堵塞",
			"plumb 只对 D 状态线程读 wchan，因为那才是“在等什么”；S 状态的线程绝大多数是正常的等待",
			"判断依据不只是 D 状态：plumb 还要求它等的是存储或内存（见 wchan 翻译表里的 Blocking），因为驱动拿不可中断睡眠做常规轮询也是有的",
		},
		Fix: []string{
			"看它等在内核的哪个函数上 —— 那一个函数名就是全部线索，用 plumb -why 可以查",
			"看它持有的文件：卡在 fsync 上的线程传输量极小，所以任何按吞吐量排的榜单都会把它报成空闲",
			"先别重启：现场是唯一能告诉你原因的东西",
		},
		Unknown: "这是某一瞬间的采样。一两个 D 线程通常是正常的同步写；只有持续存在、或者数量在涨，才说明问题。",
		Sources: []string{
			"内核文档 Documentation/filesystems/proc.rst（/proc/<pid>/stat 的状态字段、/proc/<pid>/wchan）",
			"内核源码 kernel/sched/（TASK_UNINTERRUPTIBLE 的语义）",
		},
		Confidence: Certain,
	},
	{
		Key:   "单核跑满",
		Kind:  KindArea,
		Alias: []string{"单核", "单核心", "single core"},
		What:  "一个核被单个线程占满，而其余核闲着。",
		Why: []string{
			"整机 busy 不高，所以平均数看起来完全正常，只有逐核看才发现",
			"典型来源：单线程的服务（老式应用、很多脚本语言的主循环）、或者被 GIL / 单锁串行化的程序",
			"这也是“加核没用”的经典场景：核加了，瓶颈那个线程还是只能用一个核",
		},
		Fix: []string{
			"plumb 会在整机 busy 已经很高时主动不报这一项 —— 那时候这已经不是主要矛盾了",
			"要解决得从应用下手：多进程、多线程、分片，或者把互斥的粒度改小",
			"确认不是内核侧的串行化（比如某个单队列的设备中断都落在一个核上）",
		},
		Sources: []string{
			"内核文档 Documentation/filesystems/proc.rst（/proc/stat 的逐 CPU 行）",
			"内核源码 kernel/sched/",
		},
		Confidence: Certain,
	},
	{
		Key:   "整体",
		Kind:  KindArea,
		Alias: []string{"总结", "概览", "summary"},
		What:  "这是没发现具体问题时用来收尾的合成项。",
		Why: []string{
			"当所有判定都是正常时，报告总得有个对象，这一项就是那个对象",
			"它不代表“我检查了所有东西”，只代表“我检查的那几项都没问题”",
		},
		Fix: []string{
			"没问题就是没问题，不用处置",
			"如果机器确实慢但 plumb 说没事，那说明问题在 plumb 看不到的地方：应用内部、日志、或者别的机器。这时候该看的不是这里",
		},
		Unknown: "plumb 只读 /proc。它看不到你的应用日志、看不到网络另一头、看不到数据库内部 —— 那些地方的问题它永远不会报。",
		Sources: []string{
			"本项目 README 的“已收录判定项”一节",
		},
		Confidence: Certain,
	},
}
