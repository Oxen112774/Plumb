// Command plumb diagnoses why a Linux server is slow.
//
// It is deliberately not a monitoring agent. It does not store metrics, does
// not draw graphs, and does not need configuring. It samples the machine twice,
// a second apart, and answers one question: what is making this server slow,
// and who is responsible.
//
// The design constraint that everything else follows from: no external process
// is ever executed. Every number comes from a direct read of /proc or /sys.
// Shelling out to df/ps/ss costs a fork+exec per call and depends on the output
// format of whichever coreutils version is installed — and it gets slowest
// exactly when the machine is saturated, which is when the answer matters.
//
// -watch is the one mode that stays resident, and it earns its exception the
// same way the rest does: it reads /proc, keeps nothing, and writes nothing but
// sentences. The reason it exists at all is that "it is still watching" cannot
// be faked by a program that has already exited.
package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/Oxen112774/plumb/internal/diagnose"
	"github.com/Oxen112774/plumb/internal/probe"
	"github.com/Oxen112774/plumb/internal/report"
	"github.com/Oxen112774/plumb/internal/voice"
)

const version = "0.4.0"

// errUsage means the flag package has already printed both the error and the
// usage text, so main must not add a line of its own on top of it.
var errUsage = errors.New("usage")

func main() {
	if err := run(os.Args[1:], os.Stderr); err != nil {
		if !errors.Is(err, errUsage) {
			fmt.Fprintln(os.Stderr, "plumb: "+err.Error())
		}
		os.Exit(1)
	}
}

// settings is what every sampling path shares.
type settings struct {
	mount     string
	interval  time.Duration
	top       int
	openFiles int
	prevRoot  string
	noColor   bool
	model     *voice.Model
}

// run is the whole program minus the exit codes. usage is where the flag package
// writes its parser errors, which is the caller's stderr in real use and
// io.Discard in tests.
func run(args []string, usage io.Writer) error {
	// ContinueOnError rather than ExitOnError: with ExitOnError the flag package
	// exits with status 2, which is exactly what -assert uses to mean "this
	// machine has a problem". A mistyped flag would then be indistinguishable
	// from a real alert — the one confusion a monitoring tool must not ship.
	fs := flag.NewFlagSet("plumb", flag.ContinueOnError)
	fs.SetOutput(usage)
	var (
		interval = fs.Duration("d", time.Second,
			"两次采样之间的间隔，用于把累计计数器换算成速率；设为 0 则只采样一次")
		mount   = fs.String("mount", "/", "检查剩余空间的挂载点")
		top     = fs.Int("top", 3, "每个判定最多列出几个元凶进程")
		asJSON  = fs.Bool("json", false, "以 JSON 输出，便于接入其他工具")
		noColor = fs.Bool("no-color", false, "禁用 ANSI 颜色")
		assert  = fs.Bool("assert", false,
			"发现警告及以上问题时以退出码 2 结束，便于接入巡检脚本")
		showVersion = fs.Bool("version", false, "打印版本")
		root        = fs.String("root", probe.DefaultRoot,
			"文件系统根；指向 fixture 目录即可在非 Linux 上运行")
		prevRoot = fs.String("prev-root", "",
			"上一次采样的根目录；用于复现演示，给出后跳过等待")
		openFiles = fs.Int("files", 6,
			"每个被堵住的进程最多列出几个它打开的文件；设为 0 表示不限")

		speak = fs.Bool("voice", false,
			"用人话输出：先说这是什么性质的问题、你现在该做什么，再给数字")
		watch = fs.Bool("watch", false,
			"一直盯着，只在情况变化时说话（隐含 -voice）")
		every  = fs.Duration("every", 30*time.Second, "-watch 每隔多久看一次")
		rounds = fs.Int("rounds", 0,
			"-watch 最多看几轮后退出；0 表示一直看下去（在演示数据上验证时用）")
		useAI = fs.Bool("ai", false,
			"用模型补一段综合判断；需要环境变量 PLUMB_AI_URL 与 PLUMB_AI_MODEL")

		why = fs.String("why", "",
			"查一片知识叶子：给内核等待点符号、判定项名字或 /proc 文件名；\n"+
				"      不给值就列出全部。不采样、不出报告，任何平台上都能用")
	)
	if err := fs.Parse(bareWhy(args)); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil // -h: the usage text was the answer, and it is a success
		}
		return errUsage
	}

	set := map[string]bool{}
	fs.Visit(func(f *flag.Flag) { set[f.Name] = true })

	if *showVersion {
		fmt.Println("plumb " + version)
		return nil
	}

	// -why is answered before anything else, because it does none of the things
	// the rest of the program does: it does not sample, does not read /proc and
	// does not produce a report. Someone reading the tree on their laptop should
	// not be told that their machine has no /proc.
	//
	// The only flag that means anything alongside it is -no-color, so anything
	// else is refused rather than quietly ignored. Silently dropping a flag
	// someone typed is how a tool teaches them not to trust it.
	if set["why"] {
		for _, name := range []string{
			"json", "voice", "watch", "every", "rounds", "assert", "ai",
			"root", "prev-root", "d", "mount", "top", "files",
		} {
			if set[name] {
				return fmt.Errorf("-why 单独用：它查的是内置的知识树，"+
					"不采样、也不出报告，所以 -%s 和它放在一起没有意义", name)
			}
		}
		return writeWhy(os.Stdout, *why, !*noColor)
	}

	if *asJSON && (*speak || *watch) {
		return fmt.Errorf("-json 给机器读，-voice / -watch 给人读，不能同时用")
	}
	if *assert && *watch {
		return fmt.Errorf("-assert 靠退出码报信，是给一次性巡检用的；" +
			"-watch 一直不退出，两者不能同时用")
	}

	rootFS := probe.NewRoot(*root)
	if !rootFS.Exists("proc") {
		return fmt.Errorf("在 %s 下找不到 proc 目录\n"+
			"       plumb 需要 Linux 的 /proc。若想在当前的系统上查看演示输出：\n"+
			"       plumb -root testdata/demo/cur -prev-root testdata/demo/prev -d 1s",
			rootFS.Path("proc"))
	}

	model, err := voice.FromEnv(*useAI)
	if err != nil {
		return err
	}

	s := settings{
		mount:     *mount,
		interval:  *interval,
		top:       *top,
		openFiles: *openFiles,
		prevRoot:  *prevRoot,
		noColor:   *noColor,
		model:     model,
	}

	if *watch {
		return watchLoop(rootFS, s, *every, *rounds)
	}

	rep := diagnoseOnce(rootFS, s)
	switch {
	case *asJSON:
		if err := report.JSON(os.Stdout, rep); err != nil {
			return err
		}
	case *speak:
		if err := voice.Write(os.Stdout, rep, !s.noColor, s.model); err != nil {
			return err
		}
	default:
		if err := report.Text(os.Stdout, rep, !s.noColor); err != nil {
			return err
		}
	}

	if *assert && rep.HasProblems() {
		os.Exit(2)
	}
	return nil
}

// diagnoseOnce samples the machine twice and analyses the difference.
func diagnoseOnce(root probe.Root, s settings) diagnose.Report {
	// Two samples, because most of what matters is a rate: a process that has
	// read 40 GB since boot tells you nothing, one reading 40 MB/s right now
	// tells you everything.
	var prev probe.Snapshot
	if s.prevRoot != "" {
		prev = probe.Take(probe.NewRoot(s.prevRoot), s.mount)
	} else {
		prev = probe.Take(root, s.mount)
		if s.interval > 0 {
			time.Sleep(s.interval)
		}
	}
	cur := probe.Take(root, s.mount)

	// The thread tables are read for the later sample only. "What is stuck
	// right now" is a question about an instant, not a rate, and walking every
	// thread table is the most expensive thing this tool does — there is no
	// reason to pay for it twice.
	probe.AttachThreadScan(root, &cur, s.openFiles)

	return diagnose.Analyze(prev, cur, diagnose.Options{Interval: s.interval, Top: s.top})
}

// watchLoop keeps looking, and only speaks when the picture changes.
//
// It exists because reassurance is not a matter of wording. A tool that prints a
// kind sentence and exits has not stayed with anybody; what makes a watcher
// reassuring is that it is still there on the next round, and that when it does
// speak it has something new to say. So it stays quiet on an unchanged machine —
// the alternative is a paragraph per round, which is how a watcher gets ignored
// and then stops being a watcher.
func watchLoop(root probe.Root, s settings, every time.Duration, rounds int) error {
	if every <= 0 {
		every = 30 * time.Second
	}

	var prev voice.State
	for round := 1; ; round++ {
		rep := diagnoseOnce(root, s)
		cur := voice.StateOf(rep)

		change := ""
		speak := round == 1
		if round > 1 {
			change = voice.Change(prev, cur)
			speak = change != ""
		}

		if speak {
			if round == 1 {
				fmt.Fprintf(os.Stdout,
					"开始盯着这台机器：每 %s 看一次，只在情况变化时说话。\n\n", every)
			} else {
				fmt.Fprintf(os.Stdout, "\n—— 情况变了：%s ——\n\n", change)
			}
			if err := voice.Write(os.Stdout, rep, !s.noColor, s.model); err != nil {
				return err
			}
		}
		prev = cur

		if rounds > 0 && round >= rounds {
			return nil
		}
		time.Sleep(every)
	}
}
