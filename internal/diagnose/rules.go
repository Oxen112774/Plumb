package diagnose

import (
	"fmt"
	"strings"
)

// PSI thresholds.
//
// PSI reports the share of wall-clock time tasks spent stalled, so a value
// reads directly as "the machine was unable to make progress for N% of the
// last 10 seconds". `full` is the harsher of the two lines — every runnable
// task blocked, no forward progress anywhere — which is why its thresholds
// sit far below `some`'s: 2% of the machine being entirely frozen is already
// worth a warning.
//
// Where each number comes from, so it can be argued with:
//   - io full 2%/10%: a machine fully frozen 1.2s of every minute is degraded
//     in a way users feel; 10% means a tenth of the window produced nothing
//     at all. The kernel's own psi.rst describes sustained full stalls as
//     thrashing, and 10% sustained is unambiguous thrashing.
//   - io some 15%/30%: some includes ordinary single-task waits, so its bar is
//     higher; 30% of a window with at least one waiter is a disk struggling,
//     not a busy one.
//   - memory full 2%/10%: memory full means everyone waited on reclaim, which
//     a healthy machine does only in bursts; sustained 2% is already swap
//     weather.
//   - cpu some 25%/50%: CPU some counts runnable-but-unscheduled time; a
//     saturated CPU shows 100% some, so 25% is a queue forming and 50% is a
//     queue that doubles latency for a quarter of the window.
//
// These are the *defaults*: the values a run actually used come from
// analysis.th, which the caller can move through config. The constants stay
// because DefaultThresholds is defined in terms of them, and because the
// reasoning above has to live next to the numbers it defends.
const (
	psiIOFullWarn     = 2.0
	psiIOFullCritical = 10.0
	psiIOSomeWatch    = 15.0
	psiIOSomeWarn     = 30.0

	psiMemFullWarn     = 2.0
	psiMemFullCritical = 10.0
	psiMemSomeWatch    = 25.0

	psiCPUSomeWatch = 25.0
	psiCPUSomeWarn  = 50.0
)

// Fallback thresholds, consulted only when PSI is unavailable.
//
// Utilisation is a strictly worse signal — it cannot distinguish a disk
// serving a deep queue from one blocking a single fsync — but reporting
// nothing at all would be worse still.
//
// Unlike the PSI lines these are not configurable: they exist only to say
// "something is wrong, but this kernel cannot say how badly", and a knob on
// a degraded signal would just move the moment it starts lying.
const (
	cpuBusyWarn = 90.0
	memUsedWarn = 90.0
	iowaitWarn  = 30.0
)

const (
	stealWatchPct = 3.0
	stealWarnPct  = 10.0

	swapWatchPct = 0.1
	swapWarnPct  = 10.0

	diskWatchPct    = 75.0
	diskWarnPct     = 85.0
	diskCriticalPct = 92.0

	dirtyWatchKB = 256 * 1024
	dirtyWarnKB  = 1024 * 1024

	// retransWatchPct / retransWarnPct: 1% sustained retransmission on a LAN
	// is a problem (packets should not be lost on a healthy switch); across a
	// WAN 2–3% can be tolerable, so the warning line sits at 5% and the
	// evidence carries the raw numbers for the reader who knows which kind of
	// link this is.
	retransWatchPct = 2.0
	retransWarnPct  = 5.0

	// cgroupMemWatchPct / cgroupMemWarnPct: memory.max is a hard wall, not a
	// soft slope — past 100% the OOM killer fires, so the gap between "watch"
	// and "dead" is a few percent and a reclaim burst. 90% warns while there
	// is still room to act.
	cgroupMemWatchPct = 75.0
	cgroupMemWarnPct  = 90.0

	// cpuThrottleWatchPct / WarnPct / CriticalPct cut the share of CFS
	// enforcement periods that ended with the cgroup stopped.
	//
	// A throttled period is one in which the workload was frozen for whatever
	// remained of it, so unlike the PSI lines this is a share of
	// opportunities lost rather than a share of wall-clock time. With the
	// default 100 ms period, 2% is a stall every few seconds — already worth
	// knowing on a latency-sensitive service. 10% is a workload being stopped
	// one period in ten, which users describe as "intermittently slow". 30%
	// means a third of its scheduling opportunities were denied, which no
	// amount of code optimisation inside the container can fix.
	cpuThrottleWatchPct    = 2.0
	cpuThrottleWarnPct     = 10.0
	cpuThrottleCriticalPct = 30.0
)

// ruleIOPressure is the highest-value rule in the set: storage stalls are the
// most common cause of "the server is slow" and the one a utilisation graph
// hides most effectively.
func (a *analysis) ruleIOPressure() *Finding {
	io := a.pressure().IO
	if !io.HasFull {
		return a.ruleIOWaitFallback()
	}

	var sev Severity
	switch {
	case io.Full.Avg10 >= a.th.IOFullCritical:
		sev = SeverityCritical
	case io.Full.Avg10 >= a.th.IOFullWarn:
		sev = SeverityWarning
	case io.Some.Avg10 >= a.th.IOSomeWarn:
		sev = SeverityWarning
	case io.Some.Avg10 >= a.th.IOSomeWatch:
		sev = SeverityWatch
	default:
		return nil
	}

	f := &Finding{
		Area:     a.msg("磁盘 IO", "disk I/O"),
		Severity: sev,
		Headline: a.msg(
			"磁盘让整机停摆 —— 过去 10 秒有 %.1f%% 的时间所有任务都在等它",
			"storage is stalling the whole machine — every task waited on it for %.1f%% of the last 10 seconds",
			io.Full.Avg10),
		Evidence: []Evidence{
			{Source: a.pressureSource("io"), Detail: fmt.Sprintf(
				"full avg10=%.2f avg60=%.2f avg300=%.2f",
				io.Full.Avg10, io.Full.Avg60, io.Full.Avg300)},
			{Source: a.pressureSource("io"), Detail: a.msg(
				"some avg10=%.2f（至少一个任务在等磁盘）",
				"some avg10=%.2f (at least one task waiting on storage)",
				io.Some.Avg10)},
		},
		Culprits: a.TopIO(a.top),
		Advice: a.msg(
			"先确认是谁在写：pidstat -d 1 或 iotop -o。数据库重点看全表扫描、fsync 过频、刷盘策略；"+
				"日志服务重点看级别和同步落盘。多数情况下降低队列深度（合并小写、延长 write-back）比换盘更有效。",
			"Find who is writing first: pidstat -d 1 or iotop -o. On a database look at full table "+
				"scans, fsync frequency and flush policy; on a log service look at log level and "+
				"synchronous flush. Most of the time lowering queue depth — batching small writes, "+
				"extending write-back — beats buying a faster disk."),
	}
	f.Evidence = append(f.Evidence, a.systemPSIForContrast("io")...)
	f.Evidence = append(f.Evidence, a.pressureScopeNote("io")...)
	f.Evidence = append(f.Evidence, a.thresholdNote(
		"io_full_warn", "io_full_critical", "io_some_watch", "io_some_warn")...)
	if !a.hasPrev {
		f.Culprits = nil
		f.Evidence = append(f.Evidence, Evidence{
			Source: "plumb",
			Detail: a.msg(
				"单次采样无法定位元凶进程；加 -d 1s 再跑一次即可列出",
				"a single sample cannot attribute culprits; run again with -d 1s to have them listed"),
		})
	}
	return f
}

// systemPSIForContrast adds the system-wide reading next to a cgroup one, so a
// container operator can see both stories: "we are stalled" and "the host we
// share is stalled too". Empty when the judgment already used the system
// numbers — then the line would repeat the one above it.
func (a *analysis) systemPSIForContrast(name string) []Evidence {
	c := a.cur.Cgroup
	if !c.V2 || !c.Pressure.Supported || !a.cur.Pressure.Supported {
		return nil
	}
	sys := a.cur.Pressure.IO
	if name == "memory" {
		sys = a.cur.Pressure.Memory
	}
	var detail string
	if sys.HasFull {
		detail = a.msg(
			"full avg10=%.2f some avg10=%.2f（系统级，含其他租户）",
			"full avg10=%.2f some avg10=%.2f (system-wide, other tenants included)",
			sys.Full.Avg10, sys.Some.Avg10)
	} else {
		detail = a.msg(
			"some avg10=%.2f（系统级，含其他租户）",
			"some avg10=%.2f (system-wide, other tenants included)",
			sys.Some.Avg10)
	}
	return []Evidence{{Source: "/proc/pressure/" + name, Detail: detail}}
}

// pressureScopeNote states, when the verdict had to fall back to the
// system-wide PSI, whose waiting that number actually counts.
//
// /proc/pressure is not namespaced: there is one set of counters per machine,
// and inside a container they describe the whole node — every other pod on it
// included. A container whose own cgroup has no PSI files therefore gets a
// verdict that borrows its neighbours' waiting, and without this line it is
// presented as the workload's own. That is the exact kind of number that
// sends someone off to profile a service that was innocent.
//
// It is silent when the cgroup had its own PSI, because then the number is
// already this workload's and there is nothing to qualify.
func (a *analysis) pressureScopeNote(name string) []Evidence {
	c := a.cur.Cgroup
	if !c.V2 || c.Pressure.Supported || !a.cur.Pressure.Supported {
		return nil
	}
	detail := a.msg(
		"这是节点级的数字：/proc/pressure 统计整台机器上所有任务的等待，"+
			"属于其他容器的那一部分无法剥离。这个 cgroup 没有自己的 PSI 文件，"+
			"所以这条结论借用了节点的等待 —— 有可能是别的租户在等，不是你",
		"this is a node-wide number: /proc/pressure counts every task on the machine and the "+
			"part belonging to other containers cannot be separated out. This cgroup has no PSI "+
			"file of its own, so the verdict borrows the node's waiting — it may be another "+
			"tenant that is waiting, not you")
	if a.cur.K8s.InPod {
		detail = a.msg(
			"这是节点级的数字：/proc/pressure 统计整个节点上所有任务的等待，"+
				"同一节点上其他 Pod 的等待也计在内。这个 Pod 的 cgroup 没有自己的 PSI 文件，"+
				"所以这条结论借用了节点的数据 —— 有可能是邻居 Pod 在等，不是这个 Pod",
			"this is a node-wide number: /proc/pressure counts every task on the node, including "+
				"the waiting of every other pod scheduled here. This pod's cgroup has no PSI file "+
				"of its own, so the verdict borrows the node's numbers — it may be a neighbouring "+
				"pod that is waiting, not this one")
	}
	return []Evidence{{Source: "/proc/pressure/" + name, Detail: detail}}
}

// ruleIOWaitFallback approximates IO pressure from the CPU iowait share,
// used only when PSI is missing.
func (a *analysis) ruleIOWaitFallback() *Finding {
	if !a.cpuMeasured || a.cpu.IOWait < iowaitWarn {
		return nil
	}
	return &Finding{
		Area:     a.msg("磁盘 IO", "disk I/O"),
		Severity: SeverityWarning,
		Headline: a.msg("CPU 有 %.1f%% 的时间花在等磁盘上",
			"the CPU spent %.1f%% of this window waiting on storage", a.cpu.IOWait),
		Evidence: []Evidence{
			{Source: "/proc/stat", Detail: a.msg("iowait 占本窗口 %.2f%%",
				"iowait is %.2f%% of this window", a.cpu.IOWait)},
			{Source: "plumb", Detail: a.msg(
				"该内核未启用 PSI，判据退化为 iowait 占比，精度低于 /proc/pressure/io",
				"PSI is not enabled on this kernel: the rule falls back to the iowait share, "+
					"which is weaker evidence than /proc/pressure/io")},
		},
		Culprits: a.TopIO(a.top),
		Advice: a.msg(
			"同上：先定位写入方，再决定是降级日志、调整刷盘策略还是换盘。",
			"As above: find the writer first, then decide between cutting logging, "+
				"changing flush policy, or replacing the disk."),
	}
}

func (a *analysis) ruleMemoryPressure() *Finding {
	mem := a.pressure().Memory
	if !mem.HasFull {
		return a.ruleMemUsedFallback()
	}

	var sev Severity
	switch {
	case mem.Full.Avg10 >= a.th.MemFullCritical:
		sev = SeverityCritical
	case mem.Full.Avg10 >= a.th.MemFullWarn:
		sev = SeverityWarning
	case mem.Some.Avg10 >= a.th.MemSomeWatch:
		sev = SeverityWatch
	default:
		return nil
	}

	f := &Finding{
		Area:     a.msg("内存", "memory"),
		Severity: sev,
		Headline: a.msg(
			"内存不足 —— 过去 10 秒有 %.1f%% 的时间系统在等内存回收",
			"memory is short — the system spent %.1f%% of the last 10 seconds waiting on reclaim",
			mem.Full.Avg10),
		Evidence: []Evidence{
			{Source: a.pressureSource("memory"), Detail: fmt.Sprintf(
				"full avg10=%.2f avg60=%.2f avg300=%.2f",
				mem.Full.Avg10, mem.Full.Avg60, mem.Full.Avg300)},
			{Source: "/proc/meminfo", Detail: fmt.Sprintf(
				"MemTotal=%s MemAvailable=%s SwapUsed=%s",
				HumanBytes(a.cur.Memory.TotalKB*1024),
				HumanBytes(a.cur.Memory.AvailableKB*1024),
				HumanBytes(a.cur.Memory.SwapUsedKB()*1024))},
		},
		Culprits: a.TopRSS(a.top),
		Advice: a.msg(
			"内存压力表现为回收停顿，不是单纯的百分比高。看下面谁的工作集最大；"+
				"如果能限制它的常驻内存就先限制，否则只能加内存。注意 swap 一旦被大量使用，响应时间会上升一个数量级。",
			"Memory pressure shows up as reclaim stalls, not as a high percentage. Look at who has "+
				"the largest working set below; cap its resident size if you can, otherwise add "+
				"memory. Note that once swap is in real use, latency rises by an order of magnitude."),
	}
	f.Evidence = append(f.Evidence, a.systemPSIForContrast("memory")...)
	f.Evidence = append(f.Evidence, a.pressureScopeNote("memory")...)
	f.Evidence = append(f.Evidence, a.thresholdNote(
		"mem_full_warn", "mem_full_critical", "mem_some_watch")...)
	return f
}

func (a *analysis) ruleMemUsedFallback() *Finding {
	p := a.cur.Memory.UsedPercent()
	if p < memUsedWarn {
		return nil
	}
	return &Finding{
		Area:     a.msg("内存", "memory"),
		Severity: SeverityWarning,
		Headline: a.msg("内存使用率 %.1f%%", "memory usage %.1f%%", p),
		Evidence: []Evidence{
			{Source: "/proc/meminfo", Detail: fmt.Sprintf("MemAvailable %s / MemTotal %s",
				HumanBytes(a.cur.Memory.AvailableKB*1024), HumanBytes(a.cur.Memory.TotalKB*1024))},
			{Source: "plumb", Detail: a.msg(
				"该内核未启用 PSI，判据退化为使用率，无法区分“占用高但没人在等”和真正卡住",
				"PSI is not enabled on this kernel: the rule falls back to usage, which cannot tell "+
					"\"high but nobody is waiting\" from genuinely stuck")},
		},
		Culprits: a.TopRSS(a.top),
		Advice: a.msg(
			"使用率高本身不一定是问题（page cache 占满是正常的）。先确认是否真有回收停顿。",
			"High usage is not by itself a problem — a full page cache is normal. "+
				"Confirm there is real reclaim stalling first."),
	}
}

func (a *analysis) ruleCPUPressure() *Finding {
	cpu := a.pressure().CPU
	if !cpu.HasSome {
		return a.ruleCPUBusyFallback()
	}

	var sev Severity
	switch {
	case cpu.Some.Avg10 >= a.th.CPUSomeWarn:
		sev = SeverityWarning
	case cpu.Some.Avg10 >= a.th.CPUSomeWatch:
		sev = SeverityWatch
	default:
		return nil
	}

	advice := a.msg(
		"任务在排队等 CPU。看下面谁在持续占用；如果占用者是你的服务，考虑限流或加核。",
		"Tasks are queueing for CPU. Look at who is holding it below; "+
			"if it is your service, consider rate limiting or more cores.")
	if a.cpu.SoftIRQ >= 20 {
		advice = a.msg(
			"注意 softirq 占比很高：这通常是网络包处理吃掉了 CPU，而不是你的程序。"+
				"检查是否有异常流量或连接风暴，再看网卡队列和 RPS/RFS 配置。",
			"Note the high softirq share: this is usually packet processing eating CPU, not your "+
				"program. Check for unusual traffic or a connection storm, then look at NIC queues "+
				"and RPS/RFS settings.")
	}

	f := &Finding{
		Area:     a.msg("CPU", "CPU"),
		Severity: sev,
		Headline: a.msg(
			"任务在等 CPU —— 过去 10 秒有 %.1f%% 的时间至少一个任务处于可运行但没被调度",
			"tasks are waiting for CPU — for %.1f%% of the last 10 seconds at least one task "+
				"was runnable but unscheduled", cpu.Some.Avg10),
		Evidence: []Evidence{
			{Source: a.pressureSource("cpu"), Detail: fmt.Sprintf(
				"some avg10=%.2f avg60=%.2f avg300=%.2f",
				cpu.Some.Avg10, cpu.Some.Avg60, cpu.Some.Avg300)},
			{Source: "/proc/loadavg", Detail: a.msg(
				"load1=%.2f，%d 核，每核 %.2f",
				"load1=%.2f, %d cores, %.2f per core",
				a.cur.System.Load[0], a.cur.System.Cores, a.cur.System.LoadPerCore())},
		},
		Culprits: a.TopCPU(a.top),
		Advice:   advice,
	}
	if a.cpuMeasured {
		f.Evidence = append(f.Evidence, Evidence{Source: "/proc/stat", Detail: fmt.Sprintf(
			"user=%.1f%% system=%.1f%% softirq=%.1f%%", a.cpu.User, a.cpu.System, a.cpu.SoftIRQ)})
	}
	f.Evidence = append(f.Evidence, a.pressureScopeNote("cpu")...)
	f.Evidence = append(f.Evidence, a.thresholdNote("cpu_some_watch", "cpu_some_warn")...)
	return f
}

func (a *analysis) ruleCPUBusyFallback() *Finding {
	if !a.cpuMeasured || a.cpu.Busy < cpuBusyWarn {
		return nil
	}
	return &Finding{
		Area:     a.msg("CPU", "CPU"),
		Severity: SeverityWarning,
		Headline: a.msg("CPU 使用率 %.1f%%", "CPU usage %.1f%%", a.cpu.Busy),
		Evidence: []Evidence{
			{Source: "/proc/stat", Detail: fmt.Sprintf("user=%.1f%% system=%.1f%% iowait=%.1f%%",
				a.cpu.User, a.cpu.System, a.cpu.IOWait)},
			{Source: "plumb", Detail: a.msg("该内核未启用 PSI，判据退化为使用率",
				"PSI is not enabled on this kernel: the rule falls back to usage")},
		},
		Culprits: a.TopCPU(a.top),
		Advice: a.msg("使用率高不等于有人在等。先看 load per core 是否超过 1。",
			"High usage does not mean anyone is waiting. Check load per core against 1 first."),
	}
}

// ruleSteal surfaces CPU time a hypervisor handed to another tenant.
//
// This is the finding that separates a real diagnosis from a dashboard: no
// amount of tuning inside the guest recovers stolen time, and nothing in
// top/htop/proc looks wrong. Without this number an operator on a noisy
// neighbour host will spend days optimising code that was never the problem.
func (a *analysis) ruleSteal() *Finding {
	if !a.cpuMeasured {
		return nil
	}
	var sev Severity
	switch {
	case a.cpu.Steal >= stealWarnPct:
		sev = SeverityWarning
	case a.cpu.Steal >= stealWatchPct:
		sev = SeverityWatch
	default:
		return nil
	}
	return &Finding{
		Area:     a.msg("CPU 被抢占", "CPU stolen"),
		Severity: sev,
		Headline: a.msg("宿主机抢走了 %.1f%% 的 CPU",
			"the hypervisor took %.1f%% of the CPU", a.cpu.Steal),
		Evidence: []Evidence{
			{Source: "/proc/stat", Detail: a.msg("steal 占本窗口 CPU 时间的 %.2f%%",
				"steal is %.2f%% of this window's CPU time", a.cpu.Steal)},
			{Source: "/proc/stat", Detail: a.msg("同期 user=%.1f%% system=%.1f%% idle=%.1f%%",
				"same window: user=%.1f%% system=%.1f%% idle=%.1f%%",
				a.cpu.User, a.cpu.System, a.cpu.Idle)},
		},
		Advice: a.msg(
			"steal 是虚拟机监控器把物理 CPU 分配给了同一宿主机上的其他租户。"+
				"这部分损耗不在你的程序里 —— 优化代码没有意义，应当换机型或联系服务商，并要求查看宿主机超售比例。",
			"steal is the hypervisor handing physical CPU to another tenant on the same host. "+
				"That loss is not in your program — optimising code is pointless. Change the "+
				"instance type or take it up with the provider, and ask for the host's "+
				"oversubscription ratio."),
	}
}

func (a *analysis) ruleSwap() *Finding {
	m := a.cur.Memory
	if m.SwapTotalKB == 0 {
		return nil
	}
	p := m.SwapPercent()

	var sev Severity
	switch {
	case p >= swapWarnPct:
		sev = SeverityWarning
	case p > swapWatchPct:
		sev = SeverityWatch
	default:
		return nil
	}

	return &Finding{
		Area:     a.msg("交换分区", "swap"),
		Severity: sev,
		Headline: a.msg("交换分区已用 %.1f%%（%s）", "swap is %.1f%% used (%s)",
			p, HumanBytes(m.SwapUsedKB()*1024)),
		Evidence: []Evidence{
			{Source: "/proc/meminfo", Detail: fmt.Sprintf("SwapTotal=%s SwapFree=%s",
				HumanBytes(m.SwapTotalKB*1024), HumanBytes(m.SwapFreeKB*1024))},
		},
		Culprits: a.TopRSS(a.top),
		Advice: a.msg(
			"服务器上一旦有 swap 被实际使用，说明内存已经不够。换入换出会让响应时间上升一个数量级，"+
				"而且往往表现为“随机变慢”，很难在日志里看出来。先找出工作集超标的进程。",
			"Any real swap use on a server means memory already ran short. Paging pushes latency up "+
				"by an order of magnitude and tends to look like \"random slowness\" that no log "+
				"explains. Find the process whose working set is over budget first."),
	}
}

func (a *analysis) ruleDiskSpace() *Finding {
	fs := a.cur.RootFS
	if fs.TotalBytes == 0 {
		return nil
	}

	var sev Severity
	switch {
	case fs.UsedPct >= diskCriticalPct:
		sev = SeverityCritical
	case fs.UsedPct >= diskWarnPct:
		sev = SeverityWarning
	case fs.UsedPct >= diskWatchPct:
		sev = SeverityWatch
	default:
		return nil
	}

	return &Finding{
		Area:     a.msg("磁盘空间", "disk space"),
		Severity: sev,
		Headline: a.msg("%s 已用 %.1f%%，剩余 %s", "%s is %.1f%% used, %s free",
			fs.Path, fs.UsedPct, HumanBytes(fs.FreeBytes)),
		Evidence: []Evidence{
			{Source: "statfs(2)", Detail: fmt.Sprintf("total=%s used=%s free=%s",
				HumanBytes(fs.TotalBytes), HumanBytes(fs.UsedBytes), HumanBytes(fs.FreeBytes))},
			{Source: "plumb", Detail: a.msg(
				"free 取 f_bavail，即普通用户可用空间，不含 root 保留块",
				"free is f_bavail: space available to unprivileged users, "+
					"excluding root's reserved blocks")},
		},
		Advice: a.msg(
			"先定位大文件：du -xh --max-depth=1 / | sort -h。常见去处是日志、容器镜像层、"+
				"以及 /var/lib 下的数据库残留。删完记得确认 df 已回落。",
			"Find the big files first: du -xh --max-depth=1 / | sort -h. The usual suspects are "+
				"logs, container image layers, and database leftovers under /var/lib. After "+
				"deleting, confirm df has actually dropped."),
	}
}

// ruleDirtyPages catches the write stall before it happens.
//
// When dirty pages exceed the kernel's dirty_ratio, every subsequent write()
// blocks synchronously while the kernel flushes. Applications see multi-second
// stalls on trivial writes with no disk showing as busy, which is the hardest
// kind of slowness to attribute after the fact. Watching the dirty counter
// means finding it while it is still the cause rather than the aftermath.
func (a *analysis) ruleDirtyPages() *Finding {
	m := a.cur.Memory

	var sev Severity
	switch {
	case m.DirtyKB >= dirtyWarnKB:
		sev = SeverityWarning
	case m.DirtyKB >= dirtyWatchKB:
		sev = SeverityWatch
	default:
		return nil
	}

	return &Finding{
		Area:     a.msg("脏页", "dirty pages"),
		Severity: sev,
		Headline: a.msg("脏页堆积 %s，内核即将被迫同步刷盘",
			"%s of dirty pages accumulated; the kernel is about to flush synchronously",
			HumanBytes(m.DirtyKB*1024)),
		Evidence: []Evidence{
			{Source: "/proc/meminfo", Detail: fmt.Sprintf("Dirty=%s Writeback=%s",
				HumanBytes(m.DirtyKB*1024), HumanBytes(m.WritebackKB*1024))},
			{Source: "/proc/meminfo", Detail: fmt.Sprintf("MemTotal=%s", HumanBytes(m.TotalKB*1024))},
		},
		Culprits: a.TopIO(a.top),
		Advice: a.msg(
			"脏页超过 vm.dirty_ratio 后，内核会同步刷盘，之后所有 write() 都会阻塞数秒，"+
				"应用表现为“随机卡顿”且日志里查不到原因。找出写入突增的进程；"+
				"若属正常业务量，调低 vm.dirty_ratio / vm.dirty_background_ratio 可以让刷盘更平滑。",
			"Past vm.dirty_ratio the kernel flushes synchronously, after which every write() blocks "+
				"for seconds: the application stutters at random and no log explains it. Find the "+
				"process whose writes spiked; if the volume is legitimate, lowering vm.dirty_ratio / "+
				"vm.dirty_background_ratio makes flushing smoother."),
	}
}

// ruleNetwork catches the slowness that is not on this machine at all: packets
// being lost in transit, and connections refused at the listen queue.
//
// Both are invisible to every other rule. A server retransmitting a fifth of
// its segments looks idle in CPU, memory and disk numbers while every client
// times out; a listen-queue overflow is a connection the application never
// saw, so its own logs report nothing wrong.
func (a *analysis) ruleNetwork() *Finding {
	n := a.net
	if !n.Measured {
		return nil
	}

	var aspects []string
	sev := SeverityOK

	if n.RetransPct >= retransWarnPct {
		sev = SeverityWarning
	} else if n.RetransPct >= retransWatchPct {
		if sev.Rank() > SeverityWatch.Rank() {
			sev = SeverityWatch
		}
	}
	if n.RetransPct >= retransWatchPct {
		aspects = append(aspects, a.msg("重传率 %.1f%%", "retransmit rate %.1f%%", n.RetransPct))
	}

	// Any listen drop in the window is a refused connection: there is no
	// tolerable rate of turning clients away, so this alone is a warning.
	drops := n.ListenDrops
	if drops > 0 {
		if sev.Rank() > SeverityWarning.Rank() {
			sev = SeverityWarning
		}
		aspects = append(aspects, a.msg("accept 队列丢弃了 %d 个连接",
			"the accept queue dropped %d connections", drops))
	}

	if len(aspects) == 0 {
		return nil
	}

	evidence := []Evidence{
		{Source: "/proc/net/snmp", Detail: a.msg(
			"本窗口发送 %d 段，重传 %d 段（%.1f%%）",
			"this window sent %d segments and retransmitted %d (%.1f%%)",
			n.OutSegs, n.RetransSegs, n.RetransPct)},
	}
	if drops > 0 {
		evidence = append(evidence, Evidence{
			Source: "/proc/net/netstat",
			Detail: a.msg("ListenDrops=%d，其中 ListenOverflows=%d（队列满）",
				"ListenDrops=%d, of which ListenOverflows=%d (queue full)",
				n.ListenDrops, n.ListenOverflows)})
	}
	if n.SyncookiesFailed > 0 {
		evidence = append(evidence, Evidence{
			Source: "/proc/net/netstat",
			Detail: a.msg("SyncookiesFailed=%d —— 收到的 ACK 对不上发出去的 cookie，常见于伪造来源地址的连接风暴",
				"SyncookiesFailed=%d — ACKs that matched no cookie we sent, typical of a "+
					"connection storm with spoofed source addresses", n.SyncookiesFailed)})
	}

	advice := a.msg(
		"重传率高先分清丢的是包还是心：内网 1% 都嫌多（查交换机、网卡降速、缓冲区），"+
			"跨机房 2–3% 属常态。netstat -s | grep -i retrans 能看累计分布。",
		"With a high retransmit rate, settle first whether packets are really being lost: on a LAN "+
			"even 1% is too much (check switches, NIC downshifts, buffers), while across data "+
			"centres 2–3% is ordinary. netstat -s | grep -i retrans shows the cumulative breakdown.")
	if drops > 0 {
		advice = a.msg(
			"accept 队列在丢连接：应用收不过来（accept 循环慢）或 backlog 太小。"+
				"ss -lnt 看 Recv-Q/Send-Q；调大 net.core.somaxconn 和应用的 backlog 之前，"+
				"先确认应用不是卡在别的资源上 —— 队列满通常只是别处慢的最后一环。"+
				"重传问题另见 netstat -s | grep -i retrans。",
			"The accept queue is dropping connections: the application cannot keep up (slow accept "+
				"loop) or the backlog is too small. ss -lnt shows Recv-Q/Send-Q; before raising "+
				"net.core.somaxconn and the application's backlog, confirm the application is not "+
				"stuck on something else — a full queue is usually the last link in a chain that "+
				"starts elsewhere. For retransmission, see netstat -s | grep -i retrans.")
	}

	return &Finding{
		Area:     a.msg("网络", "network"),
		Severity: sev,
		Headline: strings.Join(aspects, a.msg("，", ", ")) +
			a.msg(" —— 慢的可能不是这台机器", " — the slow thing may not be this machine"),
		Evidence: evidence,
		Advice:   advice,
	}
}

// ruleCPUThrottle catches the one way a workload can be slow that nothing
// else in this report can see.
//
// When a cgroup spends its CFS quota it is stopped until the next period, and
// a stopped task is not runnable. That one fact hides it from every other
// measurement here:
//   - PSI cpu `some` counts runnable-but-unscheduled time, so throttling
//     lowers it rather than raising it.
//   - The load average counts runnable and uninterruptible tasks. Same
//     reason: it does not move.
//   - Utilisation is measured against time the task was denied, so a pinned
//     container reports modest usage.
//
// A container can lose a third of its scheduling opportunities while the CPU
// section of this report reads clean, which is why this rule exists rather
// than being folded into ruleCPUPressure.
//
// What makes it worth stating firmly is that nr_throttled has exactly one
// cause: the kernel stops a cgroup when it has consumed its own quota inside
// the period, whatever the rest of the machine is doing. It is not a
// heuristic. It also separates the two questions a Kubernetes operator cannot
// otherwise tell apart — "is my limit too small" and "is the node too busy" —
// because the second shows up as task queueing and the first does not.
func (a *analysis) ruleCPUThrottle() *Finding {
	c := a.cur.Cgroup
	// A quota that was never installed cannot throttle, and a cpu.stat with
	// no nr_periods means no quota is being enforced right now. Both read as
	// "not applicable" rather than "throttled zero times".
	if !c.V2 || c.CPUQuotaCores <= 0 || !c.Throttle.Known || c.Throttle.Periods == 0 {
		return nil
	}
	// The window is preferred over the cumulative counters: a pod at 31%
	// cumulative and 80% in the last second is in trouble right now, and one
	// at 31% and 0% got better some time ago. Both the finding and the
	// overview read a.throttle, so they cannot disagree.
	inWindow := a.throttle.Measured
	periods, throttled := c.Throttle.Periods, c.Throttle.Throttled
	p := c.Throttle.ThrottledPct()
	if inWindow {
		periods, throttled, p = a.throttle.Periods, a.throttle.Throttled, a.throttle.Pct()
	}

	var sev Severity
	switch {
	case p >= a.th.CPUThrottleCritical:
		sev = SeverityCritical
	case p >= a.th.CPUThrottleWarn:
		sev = SeverityWarning
	case p >= a.th.CPUThrottleWatch:
		sev = SeverityWatch
	default:
		return nil
	}

	// Explicit argument indices, because the two languages order the two
	// counts differently and share one argument list: without them "2617 of
	// 8431" is one edit away from printing as "8431 of 2617". Note that Go
	// wants the precision before the index — "%.1[3]f", not "%[3].1f", which
	// silently renders as %!f(BADINDEX).
	headline := a.msg(
		"你的 CPU 限额正在掐停你自己 —— 本窗口 %[2]d 个调度周期里有 %[1]d 个（%.1[3]f%%）把配额用完后被停",
		"your CPU limit is stopping you — %[1]d of the %[2]d scheduling periods in this window "+
			"(%.1[3]f%%) ran out of quota and were stopped",
		throttled, periods, p)
	statDetail := a.msg(
		"本窗口 nr_throttled 增加 %d、nr_periods 增加 %d = %.1f%%；"+
			"累计 nr_throttled=%d / nr_periods=%d，累计被停 %.1f 秒",
		"this window: nr_throttled +%d, nr_periods +%d = %.1f%%; "+
			"cumulative nr_throttled=%d / nr_periods=%d, %.1f seconds stopped in total",
		throttled, periods, p,
		c.Throttle.Throttled, c.Throttle.Periods, c.Throttle.ThrottledSeconds())
	if !inWindow {
		headline = a.msg(
			"你的 CPU 限额在掐停你自己 —— 自这个 cgroup 创建以来 %[2]d 个调度周期里有 %[1]d 个"+
				"（%.1[3]f%%）把配额用完后被停（累计值，不代表当下）",
			"your CPU limit has been stopping you — %[1]d of this cgroup's %[2]d scheduling "+
				"periods (%.1[3]f%%) ran out of quota and were stopped "+
				"(cumulative, not necessarily right now)",
			throttled, periods, p)
		statDetail = a.msg(
			"nr_throttled=%d / nr_periods=%d = %.1f%%，累计被停 %.1f 秒（自 cgroup 创建以来累计）",
			"nr_throttled=%d / nr_periods=%d = %.1f%%, %.1f seconds stopped in total "+
				"(cumulative since the cgroup was created)",
			c.Throttle.Throttled, c.Throttle.Periods, p, c.Throttle.ThrottledSeconds())
	}

	f := &Finding{
		Area:     a.msg("CPU 配额", "CPU quota"),
		Severity: sev,
		Headline: headline,
		Evidence: []Evidence{
			{Source: "/sys/fs/cgroup/cpu.stat", Detail: statDetail},
			{Source: "/sys/fs/cgroup/cpu.max", Detail: a.msg(
				"配额 %.2f 核（宿主机 %d 核）",
				"quota %.2f cores (host has %d)", c.CPUQuotaCores, c.HostCores)},
			// The reason the other rules stayed quiet belongs in the
			// evidence: without it a reader sees "throttled 30%" next to a
			// clean CPU section and has to choose which to believe.
			{Source: "plumb", Detail: a.msg(
				"被限流的任务不处于可运行状态，因此既不计入 PSI cpu some，也不计入 load —— "+
					"这是“CPU 一节看起来正常、服务却很慢”的唯一一种成因",
				"a throttled task is not runnable, so it counts toward neither PSI cpu `some` "+
					"nor the load average — this is the only way the CPU section can read clean "+
					"while the service is slow")},
		},
		Culprits: a.TopCPU(a.top),
	}

	// A cumulative count cannot say whether this is still happening, and
	// saying so is the difference between a measurement and a suspicion.
	if !inWindow {
		// Two different reasons for not having a rate, and they call for
		// different advice: telling someone who already passed -d 1s to pass
		// -d 1s is the kind of line that makes a reader stop reading.
		detail := a.msg(
			"这是自 cgroup 创建以来的累计值，无法判断限流是否仍在发生；"+
				"加 -d 1s 再跑一次，窗口内的增量才是当下的速率",
			"this is cumulative since the cgroup was created and cannot say whether throttling is "+
				"still happening; run again with -d 1s — the increase within the window is the "+
				"current rate")
		if a.hasPrev {
			detail = a.msg(
				"本窗口内没有新增的调度周期（%s 内 nr_periods 未增长），"+
					"因此只能给出自 cgroup 创建以来的累计值 %.1f%%",
				"no enforcement period elapsed in this window (nr_periods did not grow over %s), "+
					"so only the cumulative %.1f%% since the cgroup was created is available",
				a.interval, c.Throttle.ThrottledPct())
		}
		f.Evidence = append(f.Evidence, Evidence{Source: "plumb", Detail: detail})
	}

	// The attribution that makes this worth more than a Prometheus counter:
	// whether the waiting is all at the quota, or whether tasks are also
	// queueing for CPU behind someone else.
	cpuPSI := a.pressure().CPU
	if cpuPSI.HasSome {
		if cpuPSI.Some.Avg10 >= a.th.CPUSomeWatch {
			f.Evidence = append(f.Evidence, Evidence{Source: a.pressureSource("cpu"), Detail: a.msg(
				"同期 cpu some=%.2f%%，也过了 %.0f%% 的关注线 —— 除了撞自己的配额，任务还在排队等 CPU，两件事叠加",
				"cpu some=%.2f%% over the same window, also past the %.0f%% watch line — besides "+
					"hitting your own quota, tasks are queueing for CPU: the two compound",
				cpuPSI.Some.Avg10, a.th.CPUSomeWatch)})
		} else {
			f.Evidence = append(f.Evidence, Evidence{Source: a.pressureSource("cpu"), Detail: a.msg(
				"同期 cpu some=%.2f%%，未过 %.0f%% 关注线 —— 没有任务在排队等 CPU，"+
					"等待全部来自配额限流本身，不是宿主机的争抢",
				"cpu some=%.2f%% over the same window, below the %.0f%% watch line — no task is "+
					"queueing for CPU; all of the waiting is the throttling itself, "+
					"not contention on the host",
				cpuPSI.Some.Avg10, a.th.CPUSomeWatch)})
		}
	}

	advice := a.msg(
		"限流只有一个成因：这个 cgroup 在自己那个周期里把配额用完了，与宿主机忙不忙无关。"+
			"处置也就很直接 —— 要么程序确实需要这么多 CPU（调大 resources.limits.cpu），"+
			"要么它只是突刺型负载（去掉 CPU limit、只保留 requests：空闲时能跑满，"+
			"争抢时按权重调度，不会在周期末尾被硬停）。"+
			"另外注意：5.4 之前的内核有个已知的 CFS 配额过度限流缺陷，同样的 limit 在老内核上会被多掐；"+
			"升不了内核时，把 period 调大（cpu.cfs_period_us）比调大 quota 更有效。",
		"Throttling has one cause: this cgroup spent its own quota inside the period, "+
			"independent of how busy the host is. The fix is correspondingly direct — either the "+
			"program genuinely needs that much CPU (raise resources.limits.cpu), or it is bursty "+
			"(drop the CPU limit and keep only requests: it can then use idle CPU freely and is "+
			"scheduled by weight when contended, instead of being hard-stopped at the end of a "+
			"period). One caveat: kernels before 5.4 carry a known CFS over-throttling bug, so the "+
			"same limit throttles more than it should. Where a kernel upgrade is not possible, "+
			"widening the period (cpu.cfs_period_us) helps more than widening the quota.")
	if cpuPSI.HasSome && cpuPSI.Some.Avg10 >= a.th.CPUSomeWatch {
		advice = a.msg(
			"两件事同时成立：你的配额把你掐停，而节点上也在排队。"+
				"只调大限额在这种状态下收效有限 —— 那只是让你在一个拥挤的地方多抢一点，"+
				"反而可能加剧排长队。先解决节点侧的争抢（看上面 CPU 一节的元凶进程），"+
				"再回头判断这个 Pod 的配额是否合理。",
			"Two things hold at once: your quota is stopping you, and tasks are queueing on the "+
				"node. Raising the limit alone will not help much there — it only lets you grab a "+
				"larger share of a crowded machine and can lengthen the queue. Settle the "+
				"contention on the node first (see the culprits in the CPU section above), then "+
				"come back to whether this pod's quota is right.")
	}
	f.Advice = advice

	f.Evidence = append(f.Evidence, a.thresholdNote(
		"cpu_throttle_watch", "cpu_throttle_warn", "cpu_throttle_critical")...)
	return f
}

// ruleCgroupMemory watches the memory limit of the cgroup this run sits in.
//
// memory.max is a wall, not a slope: past it the OOM killer takes the largest
// process in the cgroup without asking, which on a container means the
// container's main process dies. /proc/meminfo cannot warn about this — in a
// container it reports the host's memory — so the limit itself is the only
// honest thing to measure.
func (a *analysis) ruleCgroupMemory() *Finding {
	c := a.cur.Cgroup
	if !c.V2 || c.MemoryMaxKB == 0 {
		return nil
	}
	p := c.MemoryUsedPercent()

	var sev Severity
	switch {
	case p >= a.th.CgroupMemWarn:
		sev = SeverityWarning
	case p >= a.th.CgroupMemWatch:
		sev = SeverityWatch
	default:
		return nil
	}

	f := &Finding{
		Area:     a.msg("内存配额", "memory quota"),
		Severity: sev,
		Headline: a.msg("cgroup 内存限额已用 %.1f%%（%s / %s）",
			"the cgroup memory limit is %.1f%% used (%s / %s)",
			p, HumanBytes(c.MemoryCurrentKB*1024), HumanBytes(c.MemoryMaxKB*1024)),
		Evidence: []Evidence{
			{Source: "/sys/fs/cgroup/memory.max", Detail: a.msg(
				"限额 %s，当前用量 %s，使用率 %.1f%%",
				"limit %s, current %s, %.1f%% used",
				HumanBytes(c.MemoryMaxKB*1024), HumanBytes(c.MemoryCurrentKB*1024), p)},
		},
		Culprits: a.TopRSS(a.top),
		Advice: a.msg(
			"配额到顶不是变慢，是 OOM kill：内核会直接杀掉 cgroup 里最大的进程。"+
				"先看是谁在涨（进程列表按常驻排序）；是泄漏就堵泄漏，是业务量到了就调大限额 —— "+
				"调大前先确认宿主机上真的有那么多内存，否则只是把死期换成了宿主机上的争抢。",
			"Hitting the quota is not slowness, it is an OOM kill: the kernel kills the largest "+
				"process in the cgroup outright. Look at who is growing (the list is sorted by "+
				"resident size); plug a leak, or raise the limit if the workload really needs it — "+
				"before raising it, confirm the host actually has that memory free, or you have "+
				"only traded death here for contention there."),
	}
	// The counters are cumulative since the cgroup was created, so they are
	// history rather than a rate, and are reported in that order: touching
	// the ceiling is a stall, invoking the killer is worse, and killing is
	// the outcome.
	src, scope := a.memoryEventsSource()
	if c.MemoryBreaches > 0 {
		f.Evidence = append(f.Evidence, Evidence{Source: src, Detail: a.msg(
			"max=%d —— 已经 %d 次顶到上限，每一次都是一次同步回收停顿（%s）",
			"max=%d — the ceiling was touched %d times, each one a synchronous reclaim stall (%s)",
			c.MemoryBreaches, c.MemoryBreaches, scope)})
	}
	if c.MemoryOOMs > 0 {
		f.Evidence = append(f.Evidence, Evidence{Source: src, Detail: a.msg(
			"oom=%d —— 已经 %d 次触发 OOM killer（%s）",
			"oom=%d — the OOM killer has been invoked %d times (%s)",
			c.MemoryOOMs, c.MemoryOOMs, scope)})
	}
	if c.OOMKills > 0 {
		f.Evidence = append(f.Evidence, Evidence{Source: src, Detail: a.msg(
			"oom_kill=%d —— 这个 cgroup 自创建以来已被 OOM 杀过 %d 次 —— "+
				"配额不够是已经被发生过的事，不是预测",
			"oom_kill=%d — this cgroup has been OOM-killed %d times since it was created — the "+
				"quota being too small is something that already happened, not a forecast",
			c.OOMKills, c.OOMKills)})
	}
	f.Evidence = append(f.Evidence, a.thresholdNote("cgroup_mem_watch", "cgroup_mem_warn")...)
	return f
}

// memoryEventsSource names the file the memory counters came from, and a
// parenthetical saying whose allocations they include.
//
// memory.events counts hierarchically, so on a cgroup with children it would
// blame this workload for its descendants' allocations. Where the kernel
// offers memory.events.local that file is read instead, and the report has to
// say which of the two a number came from: 40 OOM kills is a different
// finding depending on whether they were this pod's or its children's.
func (a *analysis) memoryEventsSource() (source, scope string) {
	if a.cur.Cgroup.EventsLocal {
		return "/sys/fs/cgroup/memory.events.local", a.msg(
			"仅本层，不含子 cgroup", "this level only, excluding child cgroups")
	}
	return "/sys/fs/cgroup/memory.events", a.msg(
		"层级汇总，含子 cgroup", "hierarchical total, including child cgroups")
}
