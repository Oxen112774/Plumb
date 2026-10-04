package report

import (
	"strings"
	"testing"
	"time"
	"unicode"

	"github.com/Oxen112774/plumb/internal/diagnose"
	"github.com/Oxen112774/plumb/internal/i18n"
	"github.com/Oxen112774/plumb/internal/probe"
	"github.com/Oxen112774/plumb/internal/term"
)

func render(t *testing.T, rep diagnose.Report, lang i18n.Lang) string {
	t.Helper()
	return renderAt(t, rep, lang, 100)
}

// renderAt renders at a given width. Assertions about a whole sentence need a
// width that will not fold it: the verdict line breaks wherever the terminal
// ends, and a test that matches across the fold passes or fails by accident.
func renderAt(t *testing.T, rep diagnose.Report, lang i18n.Lang, width int) string {
	t.Helper()
	var b strings.Builder
	err := Hardcore(&b, rep, RenderOptions{
		Lang:    lang,
		Glyphs:  term.ASCII(),
		Color:   false,
		Verbose: true,
		Width:   width,
	})
	if err != nil {
		t.Fatalf("Hardcore: %v", err)
	}
	return b.String()
}

// A report collected from a pod has to say which pod. Twenty snapshots come
// back from twenty nodes and none of them can otherwise be told apart, and
// the hostname inside a pod is the pod name only until someone sets
// `hostname:` in the spec.
func testReport() diagnose.Report {
	return diagnose.Report{
		StartedAt:  time.Date(2026, 10, 3, 19, 47, 31, 0, time.UTC),
		System:     probe.System{Hostname: "web-7d9f", Cores: 8, Kernel: "5.15.0"},
		Thresholds: diagnose.DefaultThresholds(),
		Cgroup: probe.Cgroup{
			V2:            true,
			HostCores:     64,
			CPUQuotaCores: 1.5,
			MemoryMaxKB:   8388608,
			Throttle: probe.CPUThrottle{
				Periods: 1000, Throttled: 300, ThrottledUsec: 12500000, Known: true,
			},
		},
	}
}

func TestHeaderShowsPodIdentity(t *testing.T) {
	rep := testReport()
	rep.K8s = probe.K8sContext{
		InPod:    true,
		PodUID:   "8f2c1a3e-9d4b-4c6e-a1b2-c3d4e5f60718",
		QOSClass: probe.QoSBurstable,
		Runtime:  "containerd",
	}

	out := render(t, rep, i18n.ZH)
	if !strings.Contains(out, "pod 8f2c1a3e… · Burstable · containerd") {
		t.Errorf("header has no pod identity:\n%s", out)
	}
	// The English run carries the same line: identity is not prose, and
	// leaving it out of one language loses it for every reader of that one.
	if en := render(t, rep, i18n.EN); !strings.Contains(en, "pod 8f2c1a3e…") {
		t.Errorf("English header has no pod identity:\n%s", en)
	}
}

// Outside Kubernetes there is no pod to name, and a blank or a placeholder
// would be worse than nothing: it would look like information.
func TestHeaderOmitsPodIdentityOutsideKubernetes(t *testing.T) {
	out := render(t, testReport(), i18n.ZH)
	if strings.Contains(out, "pod ") {
		t.Errorf("header names a pod outside Kubernetes:\n%s", out)
	}
}

// Throttling appears in the overview even at low severity, because it is the
// one symptom every other line in this report actively hides.
func TestOverviewShowsThrottling(t *testing.T) {
	zh := render(t, testReport(), i18n.ZH)
	if !strings.Contains(zh, "限流") || !strings.Contains(zh, "300") {
		t.Errorf("ZH overview has no throttling line:\n%s", zh)
	}
	if !strings.Contains(zh, "30.0%") {
		t.Errorf("ZH overview does not show the throttled share:\n%s", zh)
	}

	en := render(t, testReport(), i18n.EN)
	if !strings.Contains(en, "300 of 1000 periods stopped") {
		t.Errorf("EN overview has no throttling line:\n%s", en)
	}
	for _, r := range en {
		if r >= 0x4e00 && r <= 0x9fff {
			t.Errorf("untranslated Chinese in English overview:\n%s", en)
			break
		}
	}
}

// A quota that is not being enforced has nothing to report. Printing
// "0 of 0 periods" would put a line in every report on every machine, and
// would read as a clean bill of health plumb never earned.
func TestOverviewOmitsThrottlingWhenAbsent(t *testing.T) {
	cases := map[string]func(*probe.Cgroup){
		"unknown":         func(c *probe.Cgroup) { c.Throttle.Known = false },
		"never throttled": func(c *probe.Cgroup) { c.Throttle.Throttled = 0 },
		"no periods":      func(c *probe.Cgroup) { c.Throttle.Periods = 0 },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			rep := testReport()
			mutate(&rep.Cgroup)
			out := render(t, rep, i18n.ZH)
			if strings.Contains(out, "限流") {
				t.Errorf("overview shows throttling for %s:\n%s", name, out)
			}
		})
	}
}

// When a container's cgroup has no PSI of its own, the summary borrowed the
// node's. It has to say so here too — otherwise the summary line and the
// finding below it disagree about whose waiting is being reported.
func TestOverviewQualifiesBorrowedPressure(t *testing.T) {
	rep := testReport()
	rep.Pressure = probe.Pressure{
		Supported: true,
		IO:        psi(17.4, 30.0, true),
	}
	// Cgroup is V2 but has no PSI, so the report fell back to the node's.

	zh := render(t, rep, i18n.ZH)
	if !strings.Contains(zh, "节点级") {
		t.Errorf("ZH overview presents node-wide PSI as the workload's:\n%s", zh)
	}
	en := render(t, rep, i18n.EN)
	if !strings.Contains(en, "node-wide") {
		t.Errorf("EN overview presents node-wide PSI as the workload's:\n%s", en)
	}
}

// The overview and the verdict must be reading the same number.
//
// The defect this locks: the overview read the cumulative counters while the
// finding read the window, so one report said 31% in its summary and 80% in
// its verdict. Two answers to one question on one screen is worse than either
// answer alone, because nothing on the screen says which to trust.
func TestOverviewAndVerdictAgreeOnThrottling(t *testing.T) {
	prevCgroup := probe.Cgroup{
		V2: true, HostCores: 64, CPUQuotaCores: 1.5, MemoryMaxKB: 8388608,
		Throttle: probe.CPUThrottle{Periods: 8421, Throttled: 2609, Known: true},
	}
	curCgroup := prevCgroup
	curCgroup.Throttle = probe.CPUThrottle{Periods: 8431, Throttled: 2617, Known: true}

	rep := diagnose.Analyze(
		probe.Snapshot{At: time.Now().Add(-time.Second), Cgroup: prevCgroup},
		probe.Snapshot{At: time.Now(), Cgroup: curCgroup},
		diagnose.Options{Interval: time.Second},
	)
	// Rendered wide, and compared with the whitespace squashed out: the
	// findings block folds at its own width regardless of the terminal, and a
	// test that matches across a fold passes or fails by accident.
	out := squash(renderAt(t, rep, i18n.ZH, 200))
	// 8 of the 10 periods in the window, not 2617 of the 8431 since boot.
	if !strings.Contains(out, "本窗口10个周期有8个被停") {
		t.Errorf("overview does not use the window:\n%s", out)
	}
	if !strings.Contains(out, "本窗口10个调度周期里有8个") {
		t.Errorf("verdict does not use the window:\n%s", out)
	}
	if strings.Count(out, "80.0%") < 2 {
		t.Errorf("overview and verdict disagree on the share:\n%s", out)
	}
}

// squash drops every whitespace character, so an assertion about a sentence
// survives the renderer folding it across lines.
func squash(s string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsSpace(r) {
			return -1
		}
		return r
	}, s)
}

// A cgroup with its own PSI needs no qualifier: the number is this workload's.
func TestOverviewDoesNotQualifyOwnPressure(t *testing.T) {
	rep := testReport()
	rep.Pressure = probe.Pressure{Supported: true, IO: psi(1.0, 5.0, true)}
	rep.Cgroup.Pressure = probe.Pressure{Supported: true, IO: psi(17.4, 30.0, true)}

	out := render(t, rep, i18n.ZH)
	if strings.Contains(out, "节点级，含其他租户）\n\n") {
		t.Errorf("qualified a reading that came from the cgroup itself:\n%s", out)
	}
}
