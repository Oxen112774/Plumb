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
	"strconv"
	"strings"
	"time"

	"github.com/Oxen112774/plumb/internal/config"
	"github.com/Oxen112774/plumb/internal/diagnose"
	"github.com/Oxen112774/plumb/internal/i18n"
	"github.com/Oxen112774/plumb/internal/probe"
	"github.com/Oxen112774/plumb/internal/report"
	"github.com/Oxen112774/plumb/internal/term"
	"github.com/Oxen112774/plumb/internal/voice"
)

// version is a var, not a const, so a release build can stamp it with
// -ldflags "-X main.version=..." rather than keeping the same number correct in
// two places. A binary that reports the wrong version is worse than one that
// reports none: it is the first thing anyone checks when a fix does not land.
var version = "1.0.1"

// stdoutIsTerminal reports whether standard output is a console rather than a
// pipe, a file or a CI log.
func stdoutIsTerminal() bool {
	fi, err := os.Stdout.Stat()
	if err != nil {
		return false
	}
	return fi.Mode()&os.ModeCharDevice != 0
}

// errUsage means the flag package has already printed both the error and the
// usage text, so main must not add a line of its own on top of it.
var errUsage = errors.New("usage")

func main() {
	// Subcommands are detected before flag parsing because the flag package
	// treats "config" as a non-flag argument and would error on it.
	if len(os.Args) >= 2 && os.Args[1] == "config" {
		if err := runConfig(os.Args[2:], os.Stdout); err != nil {
			fmt.Fprintln(os.Stderr, "plumb config: "+err.Error())
			os.Exit(1)
		}
		return
	}

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
	lang      i18n.Lang
	glyphs    term.Glyphs
	verbose   bool
	raw       bool
	paste     bool

	// thresholds are the cut-offs this run judges by. They are carried
	// in settings rather than read from config at the point of use so
	// that one place decides them, and so a report can name them.
	thresholds diagnose.Thresholds
}

// run is the whole program minus the exit codes. usage is where the flag package
// writes its parser errors, which is the caller's stderr in real use and
// io.Discard in tests.
func run(args []string, usage io.Writer) error {
	// Load config first so flags can override it. A config that fails to parse
	// is reported and ignored rather than fatal: a broken config file should
	// not make the tool unusable.
	cfg, cfgErr := config.Load()

	fs := flag.NewFlagSet("plumb", flag.ContinueOnError)
	fs.SetOutput(usage)
	var (
		interval = fs.Duration("d", cfg.Interval,
			"两次采样之间的间隔，用于把累计计数器换算成速率；设为 0 则只采样一次")
		mount   = fs.String("mount", "/", "检查剩余空间的挂载点")
		top     = fs.Int("top", cfg.Top, "每个判定最多列出几个元凶进程")
		asJSON  = fs.Bool("json", false, "以 JSON 输出，便于接入其他工具")
		noColor = fs.Bool("no-color", false, "禁用 ANSI 颜色")
		ascii   = fs.Bool("ascii", false, "强制使用纯 ASCII 字符（不使用 Unicode 框线和块字符）")
		raw     = fs.Bool("raw", false, "纯文本输出，无颜色无框线，适合写日志或管道")
		paste   = fs.Bool("paste", false, "输出为可直接粘贴到论坛/聊天求助的格式")
		verbose = fs.Bool("v", false, "专家模式：显示更多底层字段（内核栈、打开的文件、完整命令行）")
		lang    = fs.String("lang", cfg.Lang, "输出语言：zh / en / auto（auto 从系统 locale 检测）")
		style   = fs.String("style", cfg.Style,
			"报告版式：compact（默认，带压力条）/ raw（纯文本）/ paste（求助粘贴格式）")
		assert = fs.Bool("assert", false,
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
			return nil
		}
		return errUsage
	}

	if *style != config.StyleCompact && *style != config.StyleRaw && *style != config.StylePaste {
		return fmt.Errorf("-style 只能是 compact / raw / paste，收到 %q", *style)
	}

	set := map[string]bool{}
	fs.Visit(func(f *flag.Flag) { set[f.Name] = true })

	if *showVersion {
		fmt.Println("plumb " + version)
		if cfgErr != nil {
			fmt.Fprintln(os.Stderr, "警告：配置文件读取失败："+cfgErr.Error())
		}
		return nil
	}

	// --- terminal capability detection ---
	cap := term.Probe()
	useColor := !*noColor && !*raw && !*paste && cap.Color
	if cfg.Color == "off" && !set["no-color"] {
		useColor = false
	}

	// Glyphs: Unicode unless --ascii, config says ascii, or the terminal is
	// not UTF-8 capable. A non-UTF-8 terminal gets ASCII to avoid mojibake.
	var glyphs term.Glyphs
	if *ascii || cfg.Terminal == "ascii" || !cap.UTF8 {
		glyphs = term.ASCII()
	} else {
		glyphs = term.Unicode()
	}

	// Language: explicit flag > config > auto-detect.
	resolvedLang := i18n.Resolve(*lang)

	color := useColor

	// -why is answered before anything else, because it does none of the things
	// the rest of the program does: it does not sample, does not read /proc and
	// does not produce a report. Someone reading the tree on their laptop should
	// not be told that their machine has no /proc.
	if set["why"] {
		for _, name := range []string{
			"json", "voice", "watch", "every", "rounds", "assert", "ai",
			"root", "prev-root", "d", "mount", "top", "files",
			"raw", "paste", "v", "lang", "ascii", "style",
		} {
			if set[name] {
				return fmt.Errorf("-why 单独用：它查的是内置的知识树，"+
					"不采样、也不出报告，所以 -%s 和它放在一起没有意义", name)
			}
		}
		return writeWhy(os.Stdout, *why, color, resolvedLang)
	}

	// -ai 补的那段话只出现在给人读的输出里。单独打 -ai 时它没有去处，于是让
	// 它隐含 -voice，而不是收下这个 flag 却什么也不做。
	if *useAI {
		if *asJSON {
			return fmt.Errorf("-ai 补的是一段给人读的话，和给机器读的 -json 不能同时用")
		}
		*speak = true
	}
	if *asJSON && (*speak || *watch) {
		return fmt.Errorf("-json 给机器读，-voice / -watch 给人读，不能同时用")
	}
	if *assert && *watch {
		return fmt.Errorf("-assert 靠退出码报信，是给一次性巡检用的；" +
			"-watch 一直不退出，两者不能同时用")
	}
	// -every / -rounds 只在 -watch 里有意义。收下却不生效，等于教用户别信这个工具。
	if !*watch && (set["every"] || set["rounds"]) {
		return fmt.Errorf("-every 与 -rounds 只对 -watch 有意义，" +
			"请补上 -watch，或把它们去掉")
	}
	// -paste and -raw are text-only modes; they conflict with json/voice.
	if *paste && (*asJSON || *speak || *watch) {
		return fmt.Errorf("-paste 是纯文本求助格式，和 -json / -voice / -watch 不能同时用")
	}
	if *raw && (*asJSON || *speak || *watch) {
		return fmt.Errorf("-raw 是纯文本模式，和 -json / -voice / -watch 不能同时用")
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
		noColor:   !color,
		model:     model,
		lang:      resolvedLang,
		glyphs:    glyphs,
		verbose:   *verbose,
		raw:       *raw,
		paste:     *paste,

		thresholds: cfg.Thresholds.WithDefaults(),
	}

	if *watch {
		return watchLoop(rootFS, s, *every, *rounds)
	}

	rep := diagnoseOnce(rootFS, s)

	opts := report.RenderOptions{
		Lang:    resolvedLang,
		Glyphs:  glyphs,
		Color:   color,
		Verbose: *verbose,
		Width:   cap.Width,
	}

	switch {
	case *asJSON:
		if err := report.JSON(os.Stdout, rep); err != nil {
			return err
		}
	case *speak:
		if err := voice.Write(os.Stdout, rep, !s.noColor, s.model, s.lang); err != nil {
			return err
		}
	case *paste:
		if err := report.Paste(os.Stdout, rep, opts); err != nil {
			return err
		}
	case *raw:
		if err := report.Raw(os.Stdout, rep, opts); err != nil {
			return err
		}
	// -style is the standing version of -raw / -paste: an explicit flag has
	// already been handled above, so what reaches here is the config's choice.
	case *style == config.StyleRaw:
		if err := report.Raw(os.Stdout, rep, opts); err != nil {
			return err
		}
	case *style == config.StylePaste:
		if err := report.Paste(os.Stdout, rep, opts); err != nil {
			return err
		}
	default:
		if err := report.Hardcore(os.Stdout, rep, opts); err != nil {
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

	return diagnose.Analyze(prev, cur, diagnose.Options{
		Interval:   s.interval,
		Top:        s.top,
		Thresholds: s.thresholds,
		Lang:       s.lang,
	})
}

// watchLoop keeps looking, and only speaks when the picture changes.
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
			change = voice.Change(prev, cur, s.lang)
			speak = change != ""
		}

		if speak {
			if round == 1 {
				fmt.Fprintf(os.Stdout,
					"%s %s%s\n\n",
					i18n.Get(s.lang, i18n.WatchStart),
					every,
					i18n.Get(s.lang, i18n.WatchStart2))
			} else {
				fmt.Fprintf(os.Stdout, "\n—— %s：%s ——\n\n",
					i18n.Get(s.lang, i18n.WatchChanged), change)
			}
			if err := voice.Write(os.Stdout, rep, !s.noColor, s.model, s.lang); err != nil {
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

// --- plumb config subcommand ---

// runConfig handles "plumb config" and its sub-actions.
//
//	plumb config           show current effective config
//	plumb config --reset   delete the config file (restore defaults)
//	plumb config lang en   set a single key
func runConfig(args []string, w io.Writer) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}

	if len(args) == 0 {
		p := config.Path()
		if p == "" {
			p = "(无法确定路径)"
		}
		fmt.Fprintf(w, "配置文件: %s\n\n", p)
		fmt.Fprintf(w, "lang     = %s\n", cfg.Lang)
		fmt.Fprintf(w, "color    = %s\n", cfg.Color)
		fmt.Fprintf(w, "style    = %s\n", cfg.Style)
		fmt.Fprintf(w, "terminal = %s\n", cfg.Terminal)
		fmt.Fprintf(w, "interval = %s\n", cfg.Interval)
		fmt.Fprintf(w, "top      = %d\n", cfg.Top)
		fmt.Fprintf(w, "\n[thresholds]   判定分界线（括号内是 plumb 的内置值）\n")
		moved := make(map[string]diagnose.Delta)
		for _, d := range cfg.Thresholds.Changed() {
			moved[d.Name] = d
		}
		names := cfg.Thresholds.Names()
		// The column is as wide as the longest key rather than a hardcoded
		// one: a key longer than the constant silently breaks the alignment,
		// and a table that is ragged looks like it was not finished.
		width := 0
		for _, name := range names {
			if len(name) > width {
				width = len(name)
			}
		}
		for _, name := range names {
			v, _ := cfg.Thresholds.Get(name)
			line := fmt.Sprintf("  %-*s  %s", width, name, num(v))
			if d, ok := moved[name]; ok {
				line += fmt.Sprintf("   ← 已改，内置 %s", num(d.Built))
			}
			fmt.Fprintln(w, line)
		}
		if cfg.AI.URL != "" || cfg.AI.Key != "" || cfg.AI.Model != "" {
			fmt.Fprintf(w, "\n[ai]\n")
			fmt.Fprintf(w, "url   = %s\n", cfg.AI.URL)
			fmt.Fprintf(w, "model = %s\n", cfg.AI.Model)
			if cfg.AI.Key != "" {
				fmt.Fprintf(w, "key   = (已设置，%d 字符)\n", len(cfg.AI.Key))
			}
		}
		fmt.Fprintf(w, "\n修改: plumb config <key> <value>\n")
		fmt.Fprintf(w, "重置: plumb config --reset\n")
		return nil
	}

	if args[0] == "--reset" || args[0] == "-reset" {
		p := config.Path()
		if p == "" {
			return fmt.Errorf("无法确定配置文件路径")
		}
		if err := os.Remove(p); err != nil && !os.IsNotExist(err) {
			return err
		}
		fmt.Fprintln(w, "已重置为默认值（配置文件已删除）")
		return nil
	}

	if len(args) < 2 {
		return fmt.Errorf("用法: plumb config <key> <value>  或  plumb config --reset")
	}
	key, val := args[0], strings.Join(args[1:], " ")

	if err := setConfigKey(&cfg, key, val); err != nil {
		return err
	}
	if err := config.Save(cfg); err != nil {
		return err
	}
	fmt.Fprintf(w, "已设置 %s = %s\n", key, val)
	return nil
}

func setConfigKey(cfg *config.Config, key, val string) error {
	switch key {
	case "lang":
		cfg.Lang = val
	case "color":
		cfg.Color = val
	case "style":
		switch val {
		case config.StyleCompact, config.StyleRaw, config.StylePaste:
			cfg.Style = val
		default:
			return fmt.Errorf("style 只能是 compact / raw / paste，收到 %q", val)
		}
	case "terminal":
		cfg.Terminal = val
	case "interval":
		d, err := time.ParseDuration(val)
		if err != nil {
			return fmt.Errorf("interval 需要时长格式，如 1s、500ms: %w", err)
		}
		cfg.Interval = d
	case "top":
		var n int
		if _, err := fmt.Sscanf(val, "%d", &n); err != nil {
			return fmt.Errorf("top 需要整数: %w", err)
		}
		cfg.Top = n
	default:
		// Anything left is tried as a threshold name before being called
		// unknown: the cut-offs are the longest list of keys plumb has, and
		// a "set" that knows some of them but not others is a trap.
		f, err := strconv.ParseFloat(val, 64)
		if err != nil {
			return fmt.Errorf("%s 需要数字: %w", key, err)
		}
		if !cfg.Thresholds.Set(key, f) {
			// A known key with an impossible value is a different mistake
			// from a misspelled key, and the fix differs.
			if _, known := cfg.Thresholds.Get(key); known {
				return fmt.Errorf("阈值 %s 不能是负数: %s", key, val)
			}
			return fmt.Errorf("未知配置项 %q（可用: lang, color, style, terminal, "+
				"interval, top, 以及 [thresholds] 段里的键名）", key)
		}
		return nil
	}
	return nil
}

// num formats a threshold the way a human would type it back: no trailing
// zeros, no exponent.
func num(v float64) string {
	return strconv.FormatFloat(v, 'f', -1, 64)
}
