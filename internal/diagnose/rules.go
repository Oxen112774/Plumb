package diagnose

import "fmt"

// PSI thresholds.
//
// PSI reports the share of wall-clock time tasks spent stalled, so a value
// reads directly as "the machine was unable to make progress for N% of the
// last 10 seconds". `full` is the harsher of the two lines — every runnable
// task blocked, no forward progress anywhere — which is why its thresholds
// sit far below `some`'s: 2% of the machine being entirely frozen is already
// worth a warning.
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
)

// ruleIOPressure is the highest-value rule in the set: storage stalls are the
// most common cause of "the server is slow" and the one a utilisation graph
// hides most effectively.
func (a *analysis) ruleIOPressure() *Finding {
	io := a.cur.Pressure.IO
	if !io.HasFull {
		return a.ruleIOWaitFallback()
	}

	var sev Severity
	switch {
	case io.Full.Avg10 >= psiIOFullCritical:
		sev = SeverityCritical
	case io.Full.Avg10 >= psiIOFullWarn:
		sev = SeverityWarning
	case io.Some.Avg10 >= psiIOSomeWarn:
		sev = SeverityWarning
	case io.Some.Avg10 >= psiIOSomeWatch:
		sev = SeverityWatch
	default:
		return nil
	}

	f := &Finding{
		Area:     "磁盘 IO",
		Severity: sev,
		Headline: fmt.Sprintf("磁盘让整机停摆 —— 过去 10 秒有 %.1f%% 的时间所有任务都在等它", io.Full.Avg10),
		Evidence: []Evidence{
			{Source: "/proc/pressure/io", Detail: fmt.Sprintf(
				"full avg10=%.2f avg60=%.2f avg300=%.2f",
				io.Full.Avg10, io.Full.Avg60, io.Full.Avg300)},
			{Source: "/proc/pressure/io", Detail: fmt.Sprintf(
				"some avg10=%.2f（至少一个任务在等磁盘）", io.Some.Avg10)},
		},
		Culprits: a.TopIO(a.top),
		Advice: "先确认是谁在写：pidstat -d 1 或 iotop -o。数据库重点看全表扫描、fsync 过频、刷盘策略；" +
			"日志服务重点看级别和同步落盘。多数情况下降低队列深度（合并小写、延长 write-back）比换盘更有效。",
	}
	if !a.hasPrev {
		f.Culprits = nil
		f.Evidence = append(f.Evidence, Evidence{
			Source: "plumb",
			Detail: "单次采样无法定位元凶进程；加 -d 1s 再跑一次即可列出",
		})
	}
	return f
}

// ruleIOWaitFallback approximates IO pressure from the CPU iowait share,
// used only when PSI is missing.
func (a *analysis) ruleIOWaitFallback() *Finding {
	if !a.hasPrev || a.cpu.IOWait < iowaitWarn {
		return nil
	}
	return &Finding{
		Area:     "磁盘 IO",
		Severity: SeverityWarning,
		Headline: fmt.Sprintf("CPU 有 %.1f%% 的时间花在等磁盘上", a.cpu.IOWait),
		Evidence: []Evidence{
			{Source: "/proc/stat", Detail: fmt.Sprintf("iowait 占本窗口 %.2f%%", a.cpu.IOWait)},
			{Source: "plumb", Detail: "该内核未启用 PSI，判据退化为 iowait 占比，精度低于 /proc/pressure/io"},
		},
		Culprits: a.TopIO(a.top),
		Advice:   "同上：先定位写入方，再决定是降级日志、调整刷盘策略还是换盘。",
	}
}

func (a *analysis) ruleMemoryPressure() *Finding {
	mem := a.cur.Pressure.Memory
	if !mem.HasFull {
		return a.ruleMemUsedFallback()
	}

	var sev Severity
	switch {
	case mem.Full.Avg10 >= psiMemFullCritical:
		sev = SeverityCritical
	case mem.Full.Avg10 >= psiMemFullWarn:
		sev = SeverityWarning
	case mem.Some.Avg10 >= psiMemSomeWatch:
		sev = SeverityWatch
	default:
		return nil
	}

	return &Finding{
		Area:     "内存",
		Severity: sev,
		Headline: fmt.Sprintf("内存不足 —— 过去 10 秒有 %.1f%% 的时间系统在等内存回收", mem.Full.Avg10),
		Evidence: []Evidence{
			{Source: "/proc/pressure/memory", Detail: fmt.Sprintf(
				"full avg10=%.2f avg60=%.2f avg300=%.2f",
				mem.Full.Avg10, mem.Full.Avg60, mem.Full.Avg300)},
			{Source: "/proc/meminfo", Detail: fmt.Sprintf(
				"MemTotal=%s MemAvailable=%s SwapUsed=%s",
				HumanBytes(a.cur.Memory.TotalKB*1024),
				HumanBytes(a.cur.Memory.AvailableKB*1024),
				HumanBytes(a.cur.Memory.SwapUsedKB()*1024))},
		},
		Culprits: a.TopRSS(a.top),
		Advice: "内存压力表现为回收停顿，不是单纯的百分比高。看下面谁的工作集最大；" +
			"如果能限制它的常驻内存就先限制，否则只能加内存。注意 swap 一旦被大量使用，响应时间会上升一个数量级。",
	}
}

func (a *analysis) ruleMemUsedFallback() *Finding {
	p := a.cur.Memory.UsedPercent()
	if p < memUsedWarn {
		return nil
	}
	return &Finding{
		Area:     "内存",
		Severity: SeverityWarning,
		Headline: fmt.Sprintf("内存使用率 %.1f%%", p),
		Evidence: []Evidence{
			{Source: "/proc/meminfo", Detail: fmt.Sprintf("MemAvailable %s / MemTotal %s",
				HumanBytes(a.cur.Memory.AvailableKB*1024), HumanBytes(a.cur.Memory.TotalKB*1024))},
			{Source: "plumb", Detail: "该内核未启用 PSI，判据退化为使用率，无法区分“占用高但没人在等”和真正卡住"},
		},
		Culprits: a.TopRSS(a.top),
		Advice:   "使用率高本身不一定是问题（page cache 占满是正常的）。先确认是否真有回收停顿。",
	}
}

func (a *analysis) ruleCPUPressure() *Finding {
	cpu := a.cur.Pressure.CPU
	if !cpu.HasSome {
		return a.ruleCPUBusyFallback()
	}

	var sev Severity
	switch {
	case cpu.Some.Avg10 >= psiCPUSomeWarn:
		sev = SeverityWarning
	case cpu.Some.Avg10 >= psiCPUSomeWatch:
		sev = SeverityWatch
	default:
		return nil
	}

	advice := "任务在排队等 CPU。看下面谁在持续占用；如果占用者是你的服务，考虑限流或加核。"
	if a.cpu.SoftIRQ >= 20 {
		advice = "注意 softirq 占比很高：这通常是网络包处理吃掉了 CPU，而不是你的程序。" +
			"检查是否有异常流量或连接风暴，再看网卡队列和 RPS/RFS 配置。"
	}

	f := &Finding{
		Area:     "CPU",
		Severity: sev,
		Headline: fmt.Sprintf("任务在等 CPU —— 过去 10 秒有 %.1f%% 的时间至少一个任务处于可运行但没被调度", cpu.Some.Avg10),
		Evidence: []Evidence{
			{Source: "/proc/pressure/cpu", Detail: fmt.Sprintf(
				"some avg10=%.2f avg60=%.2f avg300=%.2f",
				cpu.Some.Avg10, cpu.Some.Avg60, cpu.Some.Avg300)},
			{Source: "/proc/loadavg", Detail: fmt.Sprintf(
				"load1=%.2f，%d 核，每核 %.2f", a.cur.System.Load[0], a.cur.System.Cores, a.cur.System.LoadPerCore())},
		},
		Culprits: a.TopCPU(a.top),
		Advice:   advice,
	}
	if a.hasPrev {
		f.Evidence = append(f.Evidence, Evidence{Source: "/proc/stat", Detail: fmt.Sprintf(
			"user=%.1f%% system=%.1f%% softirq=%.1f%%", a.cpu.User, a.cpu.System, a.cpu.SoftIRQ)})
	}
	return f
}

func (a *analysis) ruleCPUBusyFallback() *Finding {
	if !a.hasPrev || a.cpu.Busy < cpuBusyWarn {
		return nil
	}
	return &Finding{
		Area:     "CPU",
		Severity: SeverityWarning,
		Headline: fmt.Sprintf("CPU 使用率 %.1f%%", a.cpu.Busy),
		Evidence: []Evidence{
			{Source: "/proc/stat", Detail: fmt.Sprintf("user=%.1f%% system=%.1f%% iowait=%.1f%%",
				a.cpu.User, a.cpu.System, a.cpu.IOWait)},
			{Source: "plumb", Detail: "该内核未启用 PSI，判据退化为使用率"},
		},
		Culprits: a.TopCPU(a.top),
		Advice:   "使用率高不等于有人在等。先看 load per core 是否超过 1。",
	}
}

// ruleSteal surfaces CPU time a hypervisor handed to another tenant.
//
// This is the finding that separates a real diagnosis from a dashboard: no
// amount of tuning inside the guest recovers stolen time, and nothing in
// top/htop/proc looks wrong. Without this number an operator on a noisy
// neighbour host will spend days optimising code that was never the problem.
func (a *analysis) ruleSteal() *Finding {
	if !a.hasPrev {
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
		Area:     "CPU 被抢占",
		Severity: sev,
		Headline: fmt.Sprintf("宿主机抢走了 %.1f%% 的 CPU", a.cpu.Steal),
		Evidence: []Evidence{
			{Source: "/proc/stat", Detail: fmt.Sprintf("steal 占本窗口 CPU 时间的 %.2f%%", a.cpu.Steal)},
			{Source: "/proc/stat", Detail: fmt.Sprintf("同期 user=%.1f%% system=%.1f%% idle=%.1f%%",
				a.cpu.User, a.cpu.System, a.cpu.Idle)},
		},
		Advice: "steal 是虚拟机监控器把物理 CPU 分配给了同一宿主机上的其他租户。" +
			"这部分损耗不在你的程序里 —— 优化代码没有意义，应当换机型或联系服务商，并要求查看宿主机超售比例。",
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
		Area:     "交换分区",
		Severity: sev,
		Headline: fmt.Sprintf("交换分区已用 %.1f%%（%s）", p, HumanBytes(m.SwapUsedKB()*1024)),
		Evidence: []Evidence{
			{Source: "/proc/meminfo", Detail: fmt.Sprintf("SwapTotal=%s SwapFree=%s",
				HumanBytes(m.SwapTotalKB*1024), HumanBytes(m.SwapFreeKB*1024))},
		},
		Culprits: a.TopRSS(a.top),
		Advice: "服务器上一旦有 swap 被实际使用，说明内存已经不够。" +
			"换入换出会让响应时间上升一个数量级，而且往往表现为“随机变慢”，很难在日志里看出来。先找出工作集超标的进程。",
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
		Area:     "磁盘空间",
		Severity: sev,
		Headline: fmt.Sprintf("%s 已用 %.1f%%，剩余 %s", fs.Path, fs.UsedPct, HumanBytes(fs.FreeBytes)),
		Evidence: []Evidence{
			{Source: "statfs(2)", Detail: fmt.Sprintf("total=%s used=%s free=%s",
				HumanBytes(fs.TotalBytes), HumanBytes(fs.UsedBytes), HumanBytes(fs.FreeBytes))},
			{Source: "plumb", Detail: "free 取 f_bavail，即普通用户可用空间，不含 root 保留块"},
		},
		Advice: "先定位大文件：du -xh --max-depth=1 / | sort -h。" +
			"常见去处是日志、容器镜像层、以及 /var/lib 下的数据库残留。删完记得确认 df 已回落。",
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
		Area:     "脏页",
		Severity: sev,
		Headline: fmt.Sprintf("脏页堆积 %s，内核即将被迫同步刷盘", HumanBytes(m.DirtyKB*1024)),
		Evidence: []Evidence{
			{Source: "/proc/meminfo", Detail: fmt.Sprintf("Dirty=%s Writeback=%s",
				HumanBytes(m.DirtyKB*1024), HumanBytes(m.WritebackKB*1024))},
			{Source: "/proc/meminfo", Detail: fmt.Sprintf("MemTotal=%s", HumanBytes(m.TotalKB*1024))},
		},
		Culprits: a.TopIO(a.top),
		Advice: "脏页超过 vm.dirty_ratio 后，内核会同步刷盘，之后所有 write() 都会阻塞数秒，" +
			"应用表现为“随机卡顿”且日志里查不到原因。找出写入突增的进程；" +
			"若属正常业务量，调低 vm.dirty_ratio / vm.dirty_background_ratio 可以让刷盘更平滑。",
	}
}
