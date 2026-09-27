package knowledge

// The data-source leaves. One per file or field plumb takes a number from.
//
// These exist for the reader who wants to check a claim rather than trust it:
// every line of the report can be traced to one of these, and each leaf says
// both what the file means and — where it matters — what it cannot tell you.
// The second half is the more useful one. /proc is full of fields that look like
// they answer a question they do not answer.

var procLeaves = []Leaf{
	{
		Key:   "/proc/pressure/io",
		Kind:  KindProc,
		Alias: []string{"psi", "pressure", "PSI", "/proc/pressure"},
		What:  "PSI 的 IO 压力：有多少时间在等 IO。",
		Why: []string{
			"两行数字：some 是“至少有一个任务在等”的时间占比，full 是“所有非空闲任务都在等”的时间占比",
			"内核文档对 full 的说法很直接：这个状态下 CPU 周期是真的在浪费，长时间处于这个状态就叫 thrashing",
		},
		Fix: []string{
			"它比 util 好用，是因为它数的是等待而不是占用 —— 一块 100% 忙但没人在等的盘是健康的",
			"看 avg60 而不是 avg10：10 秒的窗口抖得厉害，容易被瞬时峰值带偏",
		},
		Unknown: "PSI 不区分你在等哪一块设备，也不说谁在等。它回答“有多堵”，不回答“谁在堵、堵在哪”。",
		Sources: []string{
			"内核文档 Documentation/accounting/psi.rst",
			"内核源码 mm/filemap.c（PSI 的 IO 计时点）",
		},
		Confidence: Certain,
	},
	{
		Key:   "/proc/pressure/cpu",
		Kind:  KindProc,
		Alias: []string{"psi cpu", "cpu压力"},
		What:  "PSI 的 CPU 压力：有多少时间有任务在等 CPU。",
		Why: []string{
			"只有 some 一行有意义：full 在整机层面没有定义，内核从 5.13 起才把它补成 0，只为了向前兼容",
			"这是它能取代 load average 的原因：它数的是排队，不是占用",
		},
		Fix: []string{
			"some 持续走高说明 CPU 真的不够用了，这时的加核是有据可依的",
			"CPU 满但 some 很低，说明 CPU 在被有效利用，不需要动手",
		},
		Sources: []string{
			"内核文档 Documentation/accounting/psi.rst（CPU full 的历史）",
		},
		Confidence: Certain,
	},
	{
		Key:   "/proc/pressure/memory",
		Kind:  KindProc,
		Alias: []string{"psi memory", "内存压力"},
		What:  "PSI 的内存压力：有多少时间在等内存回收。",
		Why: []string{
			"some 是“有任务在等内存”，full 是“所有任务都在等内存”",
			"内核文档说 full 高意味着接近 OOM：内核在拼命换页回收，而所有人在等它",
		},
		Fix: []string{
			"some 抬头的时候调优还来得及；full 开始爬就已经在悬崖边上了",
			"它比 MemFree 好用，因为它把页缓存的影响排除掉了",
		},
		Sources: []string{
			"内核文档 Documentation/accounting/psi.rst",
			"内核源码 mm/vmscan.c",
		},
		Confidence: Certain,
	},
	{
		Key:   "/proc/stat",
		Kind:  KindProc,
		Alias: []string{"stat", "iowait", "steal", "procs_blocked"},
		What:  "整机与逐核的 CPU 时间账本。",
		Why: []string{
			"每一行是一段时间的分配：user、system、idle、iowait、steal 等等",
			"iowait 是把“等 IO 的空闲”记成的一项。它不可靠：内核无法准确区分“因为 IO 而空闲”和“碰巧空闲时有 IO 在跑”，所以 PSI 才是正解",
			"procs_blocked 是在等 IO 完成的进程数 —— 这一项很实在，它和 load average 用的是同一类计数",
		},
		Fix: []string{
			"老内核没有 PSI 时，plumb 只能用 iowait 代替，并且会明说精度降低了",
			"steal 高说明 CPU 被宿主机拿走了，这不是客户机里的问题",
		},
		Unknown: "iowait 的定义本身就有含糊之处，所以任何基于它的结论都比基于 PSI 的结论弱一档。plumb 会把这个差别说出来。",
		Sources: []string{
			"内核文档 Documentation/filesystems/proc.rst（/proc/stat）",
			"内核文档 Documentation/accounting/psi.rst（iowait 为什么不够）",
			"man 5 proc_stat（procs_blocked、procs_running）",
		},
		Confidence: Certain,
	},
	{
		Key:   "/proc/meminfo",
		Kind:  KindProc,
		Alias: []string{"meminfo", "MemAvailable", "MemFree", "Dirty", "Writeback"},
		What:  "内存的明细账本。",
		Why: []string{
			"MemFree 是完全没有被用的内存：在 Linux 上它偏低是正常的，因为内核会把空闲内存拿去做页缓存",
			"MemAvailable 是内核给的估算，它已经在算上可回收的缓存之后给你一个“还能拿来跑新程序”的数",
			"内核文档自己写明：这个估算考虑的因素影响程度因系统而异 —— 它是估算，不是精确值",
		},
		Fix: []string{
			"判断内存够不够看 MemAvailable，别用 free 或者 used",
			"Dirty 和 Writeback 是脏页与正在写的页，配合 vm.dirty_* 一起看",
		},
		Unknown: "MemAvailable 是启发式估算（内核源码 si_mem_available），不同系统上偏差不一样。它够用来判断“紧不紧”，不够用来精确规划容量。",
		Sources: []string{
			"内核文档 Documentation/filesystems/proc.rst（MemAvailable 等字段）",
			"内核源码 mm/show_mem.c（si_mem_available）",
			"man 5 proc_meminfo",
		},
		Confidence: Certain,
	},
	{
		Key:   "/proc/vmstat",
		Kind:  KindProc,
		Alias: []string{"vmstat", "pgscan", "pgsteal", "allocstall", "compact_stall", "pswpin", "pswpout"},
		What:  "虚拟内存子系统的累计计数器。",
		Why: []string{
			"pgscan / pgsteal：回收器扫了多少页、真正收回了多少。扫得多收得少，说明内存里全是收不掉的匿名页",
			"allocstall_*：分配内存时被迫在分配路径上直接回收的次数。它在涨，就是“分配被内存拖住”的硬证据",
			"compact_stall / compact_fail：内存碎片整理被触发的次数、以及失败的次数",
			"pswpin / pswpout：换入换出的页数，这才是判断“是否真的在换页”的依据",
		},
		Fix: []string{
			"它是累计值，只有两次采样的差有意义 —— plumb 就是这么用的",
			"allocstall 在涨就该加内存或收紧 cgroup 限额，不用再等其它信号",
		},
		Sources: []string{
			"内核文档 Documentation/filesystems/proc.rst（/proc/vmstat）",
			"内核源码 mm/vmscan.c（pgscan、pgsteal、allocstall 的计数点）",
		},
		Confidence: Certain,
	},
	{
		Key:   "/proc/loadavg",
		Kind:  KindProc,
		Alias: []string{"loadavg", "load average", "负载", "平均负载"},
		What:  "1、5、15 分钟的平均负载，以及正在运行的线程数。",
		Why: []string{
			"Linux 的 load average 不只数在跑的任务，还把不可中断睡眠（D 状态）算了进去",
			"这就是为什么会出现 load 很高而 CPU 很闲：D 状态的线程在等盘，它们推高负载却不吃 CPU",
			"这个口径从很早就定下来了，和其它 Unix 不一样",
		},
		Fix: []string{
			"看到 load 高 CPU 低，先去看 D 状态线程 —— 问题在存储或内存，不在算力",
			"不要直接用 load / 核数 判断是否过载：分母要对，口径也要对",
			"想用更靠谱的负载指标就用 PSI，它数的是排队时间而不是任务数",
		},
		Unknown: "load average 把“想吃 CPU 的”和“在等盘的”混在一个数字里，这是它最大的问题，而你无法从一个数字里把它们拆开。",
		Sources: []string{
			"内核文档 Documentation/filesystems/proc.rst（/proc/loadavg）",
			"Brendan Gregg：Linux Load Averages: Solving the Mystery（说明 Linux 把 TASK_UNINTERRUPTIBLE 计入负载的历史）",
			"Tanel Poder：High System Load with Low CPU Utilization on Linux?",
		},
		Confidence: Certain,
	},
	{
		Key:   "/proc/<pid>/io",
		Kind:  KindProc,
		Alias: []string{"io counter", "read_bytes", "write_bytes", "rchar", "wchar"},
		What:  "某个进程累计的读写字节数。",
		Why: []string{
			"rchar / wchar 是经过系统调用的字符数，含读终端的 IO，也不管数据是不是从页缓存里拿的",
			"read_bytes / write_bytes 才是真正到存储层的量：内核文档说它在 submit_bio 那一层统计，对块设备后端是准的",
		},
		Fix: []string{
			"它是累计值，要看两次采样的差",
			"读它是为了找元凶：谁在产生 IO。但别把它当成“谁在卡”的唯一依据",
		},
		Unknown: "这是最重要的一条：卡在 fsync 上的线程传输量极小，所以按 read_bytes/write_bytes 排出来的榜单会把机器上最卡的进程报成空闲。要看堵塞必须用 D 状态加 wchan，不能靠吞吐量。",
		Sources: []string{
			"内核文档 Documentation/filesystems/proc.rst（/proc/<pid>/io 各字段及其统计位置）",
			"man 5 proc_pid_io",
		},
		Confidence: Certain,
	},
	{
		Key:   "/proc/<pid>/stat",
		Kind:  KindProc,
		Alias: []string{"delayacct_blkio_ticks", "任务状态", "stat字段", "R状态", "D状态字段"},
		What:  "进程的状态与统计，包括它被 IO 阻塞了多久。",
		Why: []string{
			"第三个字段是状态：R 在跑、S 可中断睡眠、D 不可中断睡眠、Z 僵尸",
			"第 42 个字段是 delayacct_blkio_ticks：这个线程累计被 IO 阻塞的时钟数",
			"plumb 用它来看“谁被 IO 拖了最久” —— 它是累计的，所以看的是历史而不是当下，这和 D 状态的瞬时快照正好互补",
		},
		Fix: []string{
			"要读它得先确认内核开了 CONFIG_TASK_DELAY_ACCT，否则这一列根本不存在",
			"这一列缺失的时候 plumb 记成未知而不是 0 —— 把“读不到”当成 0 会让排名完全错位",
		},
		Unknown: "这个字段需要内核编译时开启延迟统计。没开的时候它不存在，而不是等于零 —— 这两件事必须分开。",
		Sources: []string{
			"内核文档 Documentation/filesystems/proc.rst（/proc/<pid>/stat 各字段）",
			"内核文档 Documentation/accounting/delay-accounting.rst",
			"内核源码 kernel/delayacct.c",
		},
		Confidence: Certain,
	},
	{
		Key:   "/proc/<pid>/task",
		Kind:  KindProc,
		Alias: []string{"threads", "线程", "task目录"},
		What:  "进程下每个线程的目录。",
		Why: []string{
			"Linux 的线程就是共享地址空间的任务，每个都有自己的一份 stat、wchan、fd",
			"问题是进程级的：一个进程里只要有一个线程卡住，这个进程对外就已经不响应了",
		},
		Fix: []string{
			"要定位堵塞必须逐线程看，进程级的总量会把卡住的那一个平均掉",
			"plumb 只对 D 状态的线程读 wchan，因为那是全流程里最贵的一步",
		},
		Sources: []string{
			"内核文档 Documentation/filesystems/proc.rst（/proc/<pid>/task）",
		},
		Confidence: Certain,
	},
	{
		Key:   "/proc/<pid>/wchan",
		Kind:  KindProc,
		Alias: []string{"wchan", "等待点", "内核函数"},
		What:  "线程此刻停在内核的哪个函数上。",
		Why: []string{
			"它给出的是一个内核函数名 —— 这是 plumb 手里最锋利的线索，因为它没有歧义",
			"搜网页的人只能输入“服务器卡”，拿到一堆泛泛的文章；这个函数名指向内核里确切的一个地方",
			"同一个函数名下等待的原因很具体：等盘、等锁、等内存回收、或者只是正常的休眠",
		},
		Fix: []string{
			"拿去查：plumb -why <符号> 会给出它是什么、为什么会出现、怎么处置、以及出处",
			"没收录的符号也会原样打印 —— 它仍然是一条确凿的线索，只是要自己去查",
		},
		Unknown: "wchan 只说“停在哪”，不说“等了多久”。要和累计的阻塞时间一起看才知道严重程度。",
		Sources: []string{
			"内核文档 Documentation/filesystems/proc.rst（/proc/<pid>/wchan）",
			"man 5 proc_pid_wchan",
		},
		Confidence: Certain,
	},
	{
		Key:   "/proc/<pid>/fd",
		Kind:  KindProc,
		Alias: []string{"fd", "打开文件", "文件描述符"},
		What:  "进程打开的每个文件描述符指向什么。",
		Why: []string{
			"想知道一个被堵住的进程在跟什么较劲，这是唯一的入口：它把内核的等待点翻译成了具体文件",
			"plumb 会滤掉 socket、pipe、anon_inode 和 /proc、/sys、/dev 这些系统路径，只留真正的普通文件",
		},
		Fix: []string{
			"文件名带 (deleted) 后缀说明这个文件已经被删了但还被进程打开着 —— 这是磁盘满了却查不出谁占用的经典原因",
			"这个后缀必须保住，不能为了让输出好看而切掉",
		},
		Sources: []string{
			"内核文档 Documentation/filesystems/proc.rst（/proc/<pid>/fd）",
			"man 5 proc_pid_fd",
		},
		Confidence: Certain,
	},
	{
		Key:   "/proc/mdstat",
		Kind:  KindProc,
		Alias: []string{"mdstat", "raid状态", "重建进度"},
		What:  "软件 RAID 阵列的状态与重建进度。",
		Why: []string{
			"能看到阵列级别、成员盘的状态标记、以及 resync/recovery 的进度和速度",
			"重建期间性能下降是正常现象，但它会结束；一直没有进度才是问题",
		},
		Fix: []string{
			"重建拖慢业务时用 /proc/sys/dev/raid/speed_limit_max 限速，别硬扛",
			"看到 (F) 失效盘就先去处理硬件",
		},
		Sources: []string{
			"内核文档 Documentation/admin-guide/md.rst（/proc/mdstat 与 sync_action）",
			"内核源码 drivers/md/md.c",
		},
		Confidence: Certain,
	},
	{
		Key:   "/proc/slabinfo",
		Kind:  KindProc,
		Alias: []string{"slabinfo", "slab", "内核对象缓存"},
		What:  "内核对象缓存的占用明细。",
		Why: []string{
			"内核自己的数据结构也占内存，而且不显示在进程里",
			"某一项只增不减通常意味着内核里的内存泄漏，或者某个驱动在缓存不该缓存的东西",
		},
		Fix: []string{
			"用 slabtop -o 按占用排序看，比读这个文件方便",
			"确认是哪个子系统之后，能做的通常只有升级内核或者关掉那个功能",
		},
		Sources: []string{
			"内核源码 mm/slab_common.c",
			"man 1 slabtop",
		},
		Confidence: Rough,
	},
}
