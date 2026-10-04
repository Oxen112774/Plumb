package diagnose

import (
	"strings"
	"testing"
	"time"

	"github.com/Oxen112774/plumb/internal/probe"
)

// twoNetSamples returns two readings one second apart. Both are cumulative
// counters since boot, so only the difference means anything: retrans is the
// number of retransmitted segments inside the window, drops the connections
// refused at the listen queue.
func twoNetSamples(retrans, drops uint64) (probe.Snapshot, probe.Snapshot) {
	base := probe.NetTCP{Known: true, OutSegs: 1000000, RetransSegs: 5000, ListenDrops: 100}
	prev := probe.Snapshot{At: time.Now().Add(-time.Second), Net: base}
	cur := probe.Snapshot{
		At:  time.Now(),
		Net: probe.NetTCP{Known: true, OutSegs: base.OutSegs + 6000000, RetransSegs: base.RetransSegs + retrans, ListenDrops: base.ListenDrops + drops},
	}
	return prev, cur
}

// A server retransmitting a twentieth of its segments looks idle in every
// other number: no CPU, no disk, no memory. Requests just take several round
// trips instead of one.
func TestNetworkRetransmitIsReported(t *testing.T) {
	prev, cur := twoNetSamples(300000, 0) // 300000/6000000 = 5%
	rep := Analyze(prev, cur, Options{Interval: time.Second})

	f := findArea(rep, "网络")
	if f == nil {
		t.Fatalf("no network finding at 5%% retransmission; findings = %+v", rep.Findings)
	}
	if f.Severity != SeverityWarning {
		t.Errorf("severity = %q, want %q at 5%%", f.Severity, SeverityWarning)
	}
	if !strings.Contains(f.Headline, "重传率 5.0%") {
		t.Errorf("headline = %q, want it to state the rate", f.Headline)
	}
	if len(f.Evidence) == 0 || f.Evidence[0].Source != "/proc/net/snmp" {
		t.Errorf("evidence[0].Source = %q, want /proc/net/snmp — a finding has to say which file it read",
			f.Evidence[0].Source)
	}
}

// 1% is inside what a LAN with a loaded switch does on a bad day. Calling that
// a finding is how a tool trains people to ignore it.
func TestNetworkRetransmitBelowThresholdIsNotReported(t *testing.T) {
	prev, cur := twoNetSamples(60000, 0) // 1%
	rep := Analyze(prev, cur, Options{Interval: time.Second})

	if f := findArea(rep, "网络"); f != nil {
		t.Errorf("retransmission at 1%% reported as %q — that is normal on a LAN", f.Severity)
	}
}

// Every listen drop is a client that was turned away, and the application never
// saw it — so its own logs will say nothing. There is no acceptable rate of
// that, which is why a single drop in the window is already a warning.
func TestListenDropsAloneAreAWarning(t *testing.T) {
	prev, cur := twoNetSamples(0, 3)
	rep := Analyze(prev, cur, Options{Interval: time.Second})

	f := findArea(rep, "网络")
	if f == nil {
		t.Fatalf("listen drops not reported; findings = %+v", rep.Findings)
	}
	if f.Severity != SeverityWarning {
		t.Errorf("severity = %q, want %q for 3 refused connections", f.Severity, SeverityWarning)
	}
	if !strings.Contains(f.Headline, "accept 队列丢弃了 3 个连接") {
		t.Errorf("headline = %q, want it to name the dropped connections", f.Headline)
	}
}

// Rates need two samples. Reporting a retransmission rate from one reading
// would mean dividing a counter by nothing.
func TestNetworkIsSilentWithOneSample(t *testing.T) {
	_, cur := twoNetSamples(300000, 0)
	rep := Analyze(probe.Snapshot{}, cur, Options{})

	if f := findArea(rep, "网络"); f != nil {
		t.Errorf("network reported from a single sample: %+v", f)
	}
}

// An unread network must look different from a clean one. A silent zero here
// is the kind of number that closes an investigation.
func TestNetworkIsSilentWhenCountersWereUnread(t *testing.T) {
	prev := probe.Snapshot{At: time.Now().Add(-time.Second), Net: probe.NetTCP{}}
	cur := probe.Snapshot{At: time.Now(), Net: probe.NetTCP{}}
	rep := Analyze(prev, cur, Options{Interval: time.Second})

	if f := findArea(rep, "网络"); f != nil {
		t.Errorf("network reported from unread counters: %+v", f)
	}
	if rep.Net.Measured {
		t.Error("Net.Measured = true with no network reading")
	}
}
