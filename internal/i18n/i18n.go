// Package i18n holds plumb's user-facing strings in Chinese and English.
//
// The diagnostic content — kernel wait-point names, evidence sources, the
// knowledge tree — stays in the language of the thing it describes: a kernel
// symbol is ASCII everywhere, and the knowledge tree's technical prose is
// Chinese because that is what the project's author writes. What this package
// translates is the chrome around it: section headings, severity labels,
// button-like verbs, and the sentences that tell a person what they are
// looking at. That is the part a non-Chinese reader cannot guess.
//
// Detection is from environment variables, in the order glibc uses: LC_ALL,
// LC_MESSAGES, LANG. Anything starting with "zh" (zh_CN, zh_TW, zh) is
// Chinese; everything else is English. "auto" means detect; an explicit
// "zh" or "en" overrides.
package i18n

import (
	"os"
	"strings"
)

// Lang is a resolved output language.
type Lang string

const (
	ZH Lang = "zh"
	EN Lang = "en"
)

// Resolve turns a config value ("zh", "en", "auto") into a concrete language.
func Resolve(s string) Lang {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "zh", "zh_cn", "zh_tw", "chinese":
		return ZH
	case "en", "en_us", "en_gb", "english":
		return EN
	}
	return Detect()
}

// Detect reads the locale from the environment.
func Detect() Lang {
	for _, v := range []string{
		os.Getenv("LC_ALL"),
		os.Getenv("LC_MESSAGES"),
		os.Getenv("LANG"),
	} {
		if IsNoLocale(v) {
			continue
		}
		if strings.HasPrefix(strings.ToLower(v), "zh") {
			return ZH
		}
		return EN
	}
	return ZH // no locale set: the project's primary audience is Chinese,
	// and a bare Linux container often has LANG unset despite being used by
	// a Chinese operator. An English server will have LANG=en_US.UTF-8 set.
}

// IsNoLocale reports whether a locale value means "no language chosen".
//
// C, POSIX and their UTF-8 variants are one setting with several spellings,
// and C.UTF-8 is what Debian and Ubuntu fall back to when no locale has been
// generated — which is the default state of a fresh cloud image. Treating it
// as a language would answer "English" on a machine whose operator never
// made that choice, so all four spellings fall through to the next variable.
func IsNoLocale(v string) bool {
	switch v {
	case "", "C", "C.UTF-8", "C.utf8", "POSIX", "POSIX.UTF-8":
		return true
	}
	return false
}

// T is a translation key.
type T string

const (
	Title             T = "title"
	Sampling          T = "sampling"
	SingleSample      T = "single_sample"
	TwoSamples        T = "two_samples"
	Findings          T = "findings"
	NoBottleneck      T = "no_bottleneck"
	Culprits          T = "culprits"
	Evidence          T = "evidence"
	Advice            T = "advice"
	Overview          T = "overview"
	Unmeasured        T = "unmeasured"
	CPU               T = "cpu"
	Memory            T = "memory"
	Load              T = "load"
	Threads           T = "threads"
	ThreadsTotal      T = "threads_total"
	ThreadsBlocked    T = "threads_blocked"
	ThreadsNone       T = "threads_none"
	ThreadSubsystem   T = "thread_subsystem"
	Pressure          T = "pressure"
	CgroupQuota       T = "cgroup_quota"
	CgroupCgroup      T = "cgroup_this"
	CgroupSystem      T = "cgroup_system"
	Network           T = "network"
	Cores             T = "cores"
	SeverityCritical  T = "sev_critical"
	SeverityWarning   T = "sev_warning"
	SeverityWatch     T = "sev_watch"
	SeverityOK        T = "sev_ok"
	SeverityInfo      T = "sev_info"
	KernelWaitPoint   T = "kind_wchan"
	KindArea          T = "kind_area"
	KindProc          T = "kind_proc"
	KindStack         T = "kind_stack"
	ConfidenceCertain T = "conf_certain"
	ConfidenceRough   T = "conf_rough"
	ConfidenceName    T = "conf_nameonly"
	ConfidenceLabel   T = "confidence_label"
	WhatIsIt          T = "what_is"
	WhyItHappens      T = "why_happens"
	HowToFix          T = "how_fix"
	UnknownParts      T = "unknown_parts"
	Sources           T = "sources"
	TreeHasLeaves     T = "tree_has_leaves"
	TreeHowToQuery    T = "tree_howto"
	TreeFooter        T = "tree_footer"
	TreeFullEntry     T = "tree_full_entry"
	WatchStart        T = "watch_start"
	WatchStart2       T = "watch_start2"
	WatchChanged      T = "watch_changed"
	PasteHeader       T = "paste_header"
	PasteFooter       T = "paste_footer"
)

var table = map[Lang]map[T]string{
	ZH: {
		Title:             "plumb · 服务器卡顿归因",
		Sampling:          "采样",
		SingleSample:      "单次采样（无速率）",
		TwoSamples:        "两次采样",
		Findings:          "判定",
		NoBottleneck:      "未发现明显瓶颈",
		Culprits:          "元凶进程",
		Evidence:          "证据",
		Advice:            "怎么办",
		Overview:          "概况",
		Unmeasured:        "未能测到（这些项不参与判定）",
		CPU:               "CPU",
		Memory:            "内存",
		Load:              "负载",
		Threads:           "线程",
		ThreadsTotal:      "共",
		ThreadsBlocked:    "个处于 D 状态（不可中断，kill 不掉）",
		ThreadsNone:       "，没有被卡住的",
		ThreadSubsystem:   "内核栈判定",
		Pressure:          "压力",
		CgroupQuota:       "限额",
		CgroupCgroup:      "（本 cgroup）",
		CgroupSystem:      "（系统级，含其他租户）",
		Network:           "网络",
		Cores:             "核心",
		SeverityCritical:  "严重",
		SeverityWarning:   "警告",
		SeverityWatch:     "注意",
		SeverityOK:        "正常",
		SeverityInfo:      "信息",
		KernelWaitPoint:   "内核等待点",
		KindArea:          "判定项",
		KindProc:          "数据来源",
		KindStack:         "等待子系统",
		ConfidenceCertain: "有依据",
		ConfidenceRough:   "只说方向",
		ConfidenceName:    "只知症状",
		ConfidenceLabel:   "可信度",
		WhatIsIt:          "它是什么",
		WhyItHappens:      "为什么会出现",
		HowToFix:          "怎么处置",
		UnknownParts:      "看不到的地方",
		Sources:           "出处",
		TreeHasLeaves:     "plumb 的知识树里有",
		TreeHowToQuery:    "查一片：plumb -why <键>。键可以是内核等待点符号、判定项的名字，或者 /proc 里的文件名。大小写随意，前缀能对上就行。",
		TreeFooter:        "不确定的地方，每一片里都写着 —— 这一棵树不含糊其辞。",
		TreeFullEntry:     "看一片的全文：plumb -why <键>",
		WatchStart:        "开始盯着这台机器：每",
		WatchStart2:       "看一次，只在情况变化时说话。",
		WatchChanged:      "情况变了",
		PasteHeader:       "===== plumb 诊断报告（可直接粘贴求助）=====",
		PasteFooter:       "===== 报告结束 =====",
	},
	EN: {
		Title:             "plumb · server stall attribution",
		Sampling:          "sampled",
		SingleSample:      "single sample (no rates)",
		TwoSamples:        "two samples",
		Findings:          "findings",
		NoBottleneck:      "no obvious bottleneck",
		Culprits:          "culprit processes",
		Evidence:          "evidence",
		Advice:            "what to do",
		Overview:          "overview",
		Unmeasured:        "not measured (excluded from verdict)",
		CPU:               "CPU",
		Memory:            "memory",
		Load:              "load",
		Threads:           "threads",
		ThreadsTotal:      "",
		ThreadsBlocked:    "in D state (uninterruptible, kill -9 won't work)",
		ThreadsNone:       ", none blocked",
		ThreadSubsystem:   "stack says",
		Pressure:          "pressure",
		CgroupQuota:       "quota",
		CgroupCgroup:      "(this cgroup)",
		CgroupSystem:      "(system-wide, other tenants included)",
		Network:           "network",
		Cores:             "cores",
		SeverityCritical:  "CRITICAL",
		SeverityWarning:   "WARNING",
		SeverityWatch:     "WATCH",
		SeverityOK:        "OK",
		SeverityInfo:      "INFO",
		KernelWaitPoint:   "kernel wait-point",
		KindArea:          "check",
		KindProc:          "data source",
		KindStack:         "wait subsystem",
		ConfidenceCertain: "evidence-backed",
		ConfidenceRough:   "direction only",
		ConfidenceName:    "symptom only",
		ConfidenceLabel:   "confidence",
		WhatIsIt:          "what it is",
		WhyItHappens:      "why it shows up",
		HowToFix:          "how to handle",
		UnknownParts:      "what plumb cannot see",
		Sources:           "sources",
		TreeHasLeaves:     "plumb's knowledge tree has",
		TreeHowToQuery:    "Look up a leaf: plumb -why <key>. A key is a kernel wait-point symbol, a check name, or a /proc file name. Case-insensitive; prefix match works.",
		TreeFooter:        "Every leaf says where it is uncertain — this tree does not hand-wave.",
		TreeFullEntry:     "Read a leaf in full: plumb -why <key>",
		WatchStart:        "Watching this machine: every",
		WatchStart2:       ", speaking only when the picture changes.",
		WatchChanged:      "picture changed",
		PasteHeader:       "===== plumb diagnosis (ready to paste for help) =====",
		PasteFooter:       "===== end of report =====",
	},
}

// Get returns the string for key in lang, falling back to English then to the
// key itself. A missing translation is visible as the raw key rather than an
// empty string — an empty string in a UI is a bug nobody notices.
func Get(lang Lang, key T) string {
	if m, ok := table[lang]; ok {
		if s, ok := m[key]; ok && s != "" {
			return s
		}
	}
	if m, ok := table[EN]; ok {
		if s, ok := m[key]; ok && s != "" {
			return s
		}
	}
	return string(key)
}
