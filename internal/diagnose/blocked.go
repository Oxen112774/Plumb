package diagnose

import (
	"fmt"
	"sort"
	"strings"

	"github.com/Oxen112774/plumb/internal/i18n"
	"github.com/Oxen112774/plumb/internal/probe"
)

// Thresholds for the two rules in this file.
const (
	// A few D-state threads are ordinary. A database committing a transaction,
	// or any process doing one synchronous write, shows one at any given
	// instant, so a count of one or two is not by itself a finding.
	blockedThreadsWarn = 4

	// coreSkewIdleAvg is the machine-average ceiling above which a saturated
	// core stops being a skew problem. Past it the machine is simply busy, and
	// the CPU pressure rule already covers that.
	coreSkewIdleAvg = 70.0
)

// HeldFilesHeader marks the line in a culprit note below which the files a
// blocked process holds open are listed, one per line.
//
// It is exported because package voice reads these notes too, to say in one
// sentence what a blocked process is holding. A marker written out in two
// packages is a marker that will drift in one of them.
const (
	HeldFilesHeader   = "持有文件："
	HeldFilesHeaderEN = "holds:"
)

// HeldFilesHeaderFor returns the marker for a language.
func HeldFilesHeaderFor(lang i18n.Lang) string {
	if lang == i18n.EN {
		return HeldFilesHeaderEN
	}
	return HeldFilesHeader
}

// ruleBlockedThreads reports the processes the kernel has stopped dead.
//
// A thread in D state is in uninterruptible sleep. It is not runnable, and it
// cannot be stopped: SIGKILL does not land until the I/O it is waiting on
// completes, which is why a truly stuck process survives `kill -9`. That is
// the kernel's own definition of "stuck", and it is the one thing a throughput
// ranking cannot show — a thread blocked on a flush moves almost no data while
// it waits, so /proc/<pid>/io, and every dashboard built on it, reports the
// most stuck process on the machine as idle.
//
// The finding names three things an operator needs at once: which process,
// what the kernel function it is parked in means, and which files it holds
// open. The last one is how the wait gets attached to something actionable —
// the kernel will not say which descriptor is stuck, but a process blocked on
// a file-system flush with one database file open is not ambiguous.
func (a *analysis) ruleBlockedThreads() *Finding {
	blocked := a.cur.Threads.BlockedProcesses()
	if len(blocked) == 0 {
		return nil
	}

	type entry struct {
		pt      probe.ProcessThreads
		waits   []Wait
		storage int // threads waiting on storage or memory, not on anything else
	}

	var (
		entries        []entry
		storageThreads int
	)
	for _, bp := range blocked {
		e := entry{pt: bp}
		for _, th := range bp.BlockedOn {
			w := TranslateWChan(th.WChan, a.lang)
			e.waits = append(e.waits, w)
			if w.Blocking() {
				e.storage++
			}
		}
		storageThreads += e.storage
		entries = append(entries, e)
	}

	// Worst first, then by name so the output is stable between runs.
	sort.SliceStable(entries, func(i, j int) bool {
		if entries[i].storage != entries[j].storage {
			return entries[i].storage > entries[j].storage
		}
		return entries[i].pt.Name < entries[j].pt.Name
	})

	// D state on its own is evidence, not proof: a driver may use
	// uninterruptible sleep for a routine poll. Reporting the count is honest
	// either way; what the severity says is how much it should worry someone.
	// The pressure this is weighed against has to be the same pressure the
	// rest of the report quotes. On a container with per-cgroup PSI that is
	// the cgroup's own file, not /proc/pressure/io: the system-wide number
	// includes every other tenant on the host, so a noisy neighbour would
	// push this finding up a severity while the blocked threads have nothing
	// to do with it.
	ioP := a.pressure().IO
	ioStalling := ioP.HasFull && ioP.Full.Avg10 >= psiIOFullWarn
	sev := SeverityInfo
	switch {
	case a.cur.Threads.Blocked > 0 && (ioStalling || storageThreads >= blockedThreadsWarn):
		// Threads blocked on storage *and* PSI confirming that IO is stalling
		// tasks is a stronger claim than either measurement alone: it is the
		// difference between "one transaction happens to be committing" and
		// "the disk is holding the machine up".
		sev = SeverityWarning
	case storageThreads > 0:
		sev = SeverityWatch
	}

	var headline string
	if storageThreads > 0 {
		headline = a.msg("有 %d 个线程被内核堵住，动不了也杀不掉",
			"%d threads are blocked by the kernel: they cannot move and cannot be killed",
			storageThreads)
	} else {
		headline = a.msg("%d 个线程处于不可中断睡眠，但等待点不是磁盘或内存",
			"%d threads are in uninterruptible sleep, but not waiting on storage or memory",
			a.cur.Threads.Blocked)
	}

	evidence := []Evidence{
		{Source: "/proc/<pid>/task", Detail: a.msg(
			"全机 %d 个线程中 %d 个处于 D 状态（不可中断睡眠，信号无法送达）",
			"%d of the machine's %d threads are in D state (uninterruptible sleep; signals do not land)",
			a.cur.Threads.Blocked, a.cur.Threads.Total)},
		{Source: "/proc/<pid>/wchan", Detail: a.msg(
			"wchan 给出线程此刻停在内核的哪个函数上，即它在等什么",
			"wchan names the kernel function the thread is parked in, which is what it is waiting for")},
		{Source: "/proc/<pid>/fd", Detail: a.msg(
			"被堵住的进程持有的普通文件；socket、pipe、anon_inode 与 /dev 已滤掉",
			"regular files held by the blocked processes; sockets, pipes, anon_inode and /dev are filtered out")},
		{Source: "plumb", Detail: a.msg(
			"D 状态是某一瞬间的采样：一两个通常是正常的同步写，数量持续偏高才说明存储真的顶不住",
			"D state is a sample of one instant: one or two is usually an ordinary synchronous "+
				"write; a count that stays high is what says storage really cannot keep up")},
	}
	// Kernel stacks, when the kernel exposes them, name the subsystem the
	// wait belongs to. wchan cannot: it is the innermost frame, and io_schedule
	// is where every block-I/O wait bottoms out. The subsystem is what
	// separates remedies that are not interchangeable.
	if line := a.BlockedSubsystemLine(); line != "" {
		evidence = append(evidence, Evidence{
			// Same abbreviation the wchan row above uses: the real path is
			// /proc/<pid>/task/<tid>/stack, and the full form would break the
			// source column the other rows line up in.
			Source: "/proc/<pid>/stack",
			Detail: line,
		})
	}
	if ioStalling {
		evidence = append(evidence, Evidence{
			// The label must name the file the number actually came from,
			// or the reader is sent to check a file that does not hold it.
			Source: a.pressureSource("io"),
			Detail: a.msg("full avg10=%.2f：与上面的被堵线程互相印证",
				"full avg10=%.2f: corroborates the blocked threads above",
				ioP.Full.Avg10),
		})
	}

	culprits := make([]Culprit, 0, len(entries))
	for _, e := range entries {
		var note strings.Builder
		for i, th := range e.pt.BlockedOn {
			w := e.waits[i]
			note.WriteString(a.msg("线程 %d：%s", "thread %d: %s", th.TID, w.Short))
			if w.Symbol != "" {
				fmt.Fprintf(&note, "  [%s]", w.Symbol)
			}
			// The stack names the subsystem the wait belongs to, which wchan
			// cannot: wchan is the innermost frame and is usually io_schedule.
			// The deciding frame is printed so the call can be checked.
			if th.Stack.Subsystem != "" {
				fmt.Fprintf(&note, "\n  %s", a.msg("内核栈：%s（依据 %s）",
					"kernel stack: %s (decided by %s)", a.subsystemName(th.Stack.Subsystem), th.Stack.DecidedBy))
			}
			note.WriteString("\n")
		}
		if set, ok := a.cur.OpenFiles[e.pt.PID]; ok && len(set.Paths) > 0 {
			// One path per line: a long list wrapped to the column budget
			// breaks paths mid-component, and the path is the piece of
			// evidence here that has to survive being read aloud.
			note.WriteString(HeldFilesHeaderFor(a.lang))
			for _, p := range set.Paths {
				note.WriteString("\n  " + p)
			}
			if set.Truncated() {
				note.WriteString("\n  " + a.msg("另有 %d 个未列出（-files 0 可全部列出）",
					"%d more not listed (-files 0 lists them all)",
					set.Distinct-len(set.Paths)))
			}
		}

		culprits = append(culprits, Culprit{
			PID:     e.pt.PID,
			Name:    e.pt.Name,
			Note:    strings.TrimRight(note.String(), "\n"),
			Cmdline: a.cmdlineFor(e.pt.PID),
		})
	}

	advice := a.msg(
		"D 状态的线程连 SIGKILL 都不会生效 —— 它会挂到 IO 完成为止，"+
			"所以碰到杀不掉的进程，别反复 kill：先看它卡在哪个内核函数、手里拿着哪个文件，"+
			"那才是真正在等的东西。",
		"A thread in D state will not take a SIGKILL — it hangs until the I/O returns. So "+
			"when a process will not die, stop killing it: look at which kernel function it is "+
			"parked in and which file it holds. That is what it is actually waiting for.")
	if subs := a.blockedSubsystems(); len(subs) > 0 {
		// The stack answer wins over the wchan answer when both exist. wchan
		// names the innermost frame — nearly always io_schedule — while the
		// stack names the subsystem that decided to wait. "This is NFS and
		// this machine is fine" is actionable; "this is I/O" is not.
		if s := TranslateSubsystem(subs[0], a.lang); s.Advice != "" {
			advice = joinSentences(s.Advice, advice, a.lang)
		}
	} else if len(entries) > 0 && entries[0].storage > 0 {
		for _, w := range entries[0].waits {
			if w.Blocking() && w.Advice != "" {
				advice = joinSentences(w.Advice, advice, a.lang)
				break
			}
		}
	}

	return &Finding{
		Area:     a.msg("被堵住的线程", "blocked threads"),
		Severity: sev,
		Headline: headline,
		Evidence: evidence,
		Culprits: culprits,
		Advice:   advice,
	}
}

// cmdlineFor returns a process's command line from the current snapshot.
func (a *analysis) cmdlineFor(pid int) string {
	for _, p := range a.cur.Procs {
		if p.PID == pid {
			return p.Cmdline
		}
	}
	return ""
}

// joinSentences glues two advice sentences together. Chinese ends a sentence
// with a full-width stop and needs no space after it; an English fragment does.
func joinSentences(a, b string, lang i18n.Lang) string {
	if a == "" {
		return b
	}
	if b == "" {
		return a
	}
	if r := []rune(a); len(r) > 0 && strings.ContainsRune("。！？；", r[len(r)-1]) {
		return a + b
	}
	if lang == i18n.EN {
		return a + " " + b
	}
	return a + b
}

// ruleCoreSkew catches the single-threaded process pinning one core.
//
// The machine average hides this completely. One core at 99% of four reads as
// 42% busy, which looks healthy, so nothing complains while every request that
// touches the single-threaded component queues behind that one core. Plenty of
// production software is still single-threaded per worker, and the fix is
// structural — shard the work, run more workers — not "add more cores", which
// is what the healthy-looking average would suggest.
func (a *analysis) ruleCoreSkew() *Finding {
	if !a.cpuMeasured {
		return nil
	}
	loads := a.cur.CoreLoads(a.prev)
	if len(loads) < 2 {
		return nil // a single core cannot be skewed relative to itself
	}

	var peaks []probe.CoreLoad
	for _, l := range loads {
		if l.Peaked() {
			peaks = append(peaks, l)
		}
	}
	if len(peaks) == 0 {
		return nil
	}
	// If the whole machine is busy this is not a skew problem, and the CPU
	// pressure rule already covers it.
	if a.cpu.Busy >= coreSkewIdleAvg {
		return nil
	}
	sort.Slice(peaks, func(i, j int) bool { return peaks[i].ID < peaks[j].ID })

	// Under a CPU quota the per-core lines describe the host, not this
	// workload: /proc/stat lists every host core regardless of what this
	// cgroup may run on, so a co-tenant's process can be the one pinning
	// core 0. Two facts — "a core is full" and "a process of ours is busy" —
	// do not license naming that process as the cause, so inside a quota the
	// rule demands the one piece of evidence that actually links them:
	// field 39 of /proc/<pid>/stat, the CPU the task last ran on.
	quota := a.cur.Cgroup.V2 && a.cur.Cgroup.CPUQuotaCores > 0
	culprits := a.TopCPU(1)
	linked := len(culprits) > 0 && culprits[0].LastCPUKnown && onCore(culprits[0].LastCPU, peaks)
	if quota && !linked {
		return nil
	}

	names := make([]string, 0, len(peaks))
	busiest := peaks[0].Busy
	for _, p := range peaks {
		names = append(names, fmt.Sprintf("CPU%d", p.ID))
		if p.Busy > busiest {
			busiest = p.Busy
		}
	}
	// The headline percentage is the fullest core of them all, so it matches
	// "已经跑满" rather than whichever core happened to have the lowest id.
	headline := a.msg("%s 已经跑满（%.0f%%），整机平均却只有 %.1f%%",
		"%s is saturated (%.0f%%) while the machine average is only %.1f%%",
		strings.Join(names, a.msg("、", ", ")), busiest, a.cpu.Busy)

	evidence := []Evidence{
		{Source: "plumb", Detail: a.msg("%d 个核心中有 %d 个被单个线程占满，其余核心空闲",
			"%d of %d cores are held solid by a single thread; the rest are idle",
			len(peaks), len(loads))},
		{Source: "/proc/stat", Detail: a.msg("整机 busy=%.1f%% user=%.1f%% system=%.1f%%",
			"machine busy=%.1f%% user=%.1f%% system=%.1f%%",
			a.cpu.Busy, a.cpu.User, a.cpu.System)},
	}
	for _, p := range peaks {
		evidence = append(evidence, Evidence{
			Source: "/proc/stat",
			Detail: fmt.Sprintf("cpu%d busy=%.1f%% iowait=%.1f%% steal=%.1f%%",
				p.ID, p.Busy, p.IOWait, p.Steal),
		})
	}
	if linked {
		evidence = append(evidence, Evidence{
			Source: "/proc/<pid>/stat",
			Detail: a.msg("PID %d 上一次运行在 CPU%d 上（字段 39 processor）—— 与上面那个满核对得上，这是指认它的依据",
				"PID %d last ran on CPU%d (field 39, processor) — it matches the saturated core "+
					"above, which is what licenses naming it",
				culprits[0].PID, culprits[0].LastCPU),
		})
	}
	if quota {
		evidence = append(evidence, Evidence{
			Source: "plumb",
			Detail: a.msg("逐核行来自 /proc/stat，是宿主机的视图；本 cgroup 只有 %s 配额，"+
				"所以那个满核未必是本工作负载在用 —— 已知的只是元凶进程最后确实落在它上面",
				"The per-core lines come from /proc/stat and describe the host; this cgroup has "+
					"only %s of quota, so the saturated core may not be one this workload runs on "+
					"— all that is known is that the culprit process was last seen on it",
				fmt.Sprintf("%s%s", trimFloat(a.cur.Cgroup.CPUQuotaCores), a.msg(" 核", " cores"))),
		})
	}

	return &Finding{
		Area:     a.msg("单核跑满", "one core saturated"),
		Severity: SeverityWatch,
		Headline: headline,
		Evidence: evidence,
		Culprits: a.TopCPU(1),
		Advice: a.msg(
			"单个核心满负荷而其他核心空闲，说明瓶颈是“一个线程干完全部活”，"+
				"不是 CPU 不够。加核心对这种结构没有任何帮助 —— 要动的是并发方式："+
				"多开 worker 进程、把请求分片，或者把这个组件从单线程模型里拆出来。"+
				"反过来，如果这台机器还要继续跑别的服务，被占满的那个核心也会让它们一起变慢。",
			"One core saturated while the others idle means the bottleneck is \"one thread doing "+
				"all the work\", not a shortage of CPU. Adding cores does nothing for this shape — "+
				"what has to change is the concurrency: more worker processes, sharded requests, or "+
				"this component pulled out of a single-threaded model. Conversely, if this machine "+
				"also runs other services, the saturated core will slow them down with it."),
	}
}

// onCore reports whether a CPU number is one of the cores that peaked.
//
// This is the whole of the link between a per-core reading and a process: the
// kernel says which core the task last ran on, and the report says which cores
// are full. Matching them is what makes an accusation a measurement.
func onCore(cpu int, peaks []probe.CoreLoad) bool {
	for _, p := range peaks {
		if p.ID == cpu {
			return true
		}
	}
	return false
}
