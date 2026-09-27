// Command plumb diagnoses why a Linux server is slow.
//
// It is deliberately not a monitoring agent. It does not stay resident, does
// not store metrics, and does not draw graphs. It samples the machine twice,
// a second apart, and answers one question: what is making this server slow,
// and who is responsible.
//
// The design constraint that everything else follows from: no external
// process is ever executed. Every number comes from a direct read of /proc or
// /sys. Shelling out to df/ps/ss costs a fork+exec per call and depends on the
// output format of whichever coreutils version is installed — and it gets
// slowest exactly when the machine is saturated, which is when the answer
// matters.
package main

import (
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/Oxen112774/plumb/internal/diagnose"
	"github.com/Oxen112774/plumb/internal/probe"
	"github.com/Oxen112774/plumb/internal/report"
)

const version = "0.1.0"

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "plumb: "+err.Error())
		os.Exit(1)
	}
}

func run(args []string) error {
	fs := flag.NewFlagSet("plumb", flag.ExitOnError)
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
	)
	if err := fs.Parse(args); err != nil {
		return err
	}

	if *showVersion {
		fmt.Println("plumb " + version)
		return nil
	}

	rootFS := probe.NewRoot(*root)
	if !rootFS.Exists("proc") {
		return fmt.Errorf("在 %s 下找不到 proc 目录\n"+
			"       plumb 需要 Linux 的 /proc。若想在当前的系统上查看演示输出：\n"+
			"       plumb -root testdata/demo/cur -prev-root testdata/demo/prev -d 1s",
			rootFS.Path("proc"))
	}

	// Two samples, because most of what matters is a rate: a process that has
	// read 40 GB since boot tells you nothing, one reading 40 MB/s right now
	// tells you everything.
	var prev probe.Snapshot
	if *prevRoot != "" {
		prev = probe.Take(probe.NewRoot(*prevRoot), *mount)
	} else {
		prev = probe.Take(rootFS, *mount)
		if *interval > 0 {
			time.Sleep(*interval)
		}
	}
	cur := probe.Take(rootFS, *mount)

	rep := diagnose.Analyze(prev, cur, diagnose.Options{Interval: *interval, Top: *top})

	if *asJSON {
		if err := report.JSON(os.Stdout, rep); err != nil {
			return err
		}
	} else if err := report.Text(os.Stdout, rep, !*noColor); err != nil {
		return err
	}

	if *assert && rep.HasProblems() {
		os.Exit(2)
	}
	return nil
}
