package probe

import (
	"fmt"
	"strconv"
	"strings"
)

// NetTCP is the TCP accounting a kernel keeps in /proc/net/snmp and
// /proc/net/netstat, both of them cumulative counters since boot.
//
// "The server is slow" is a network story as often as a disk story, and it is
// the one no /proc-based tool on the diagnosis path usually covers: retransmits
// mean every request pays for its packets twice or more, and a listen-queue
// overflow means connections were refused outright. Both are invisible in CPU,
// memory and disk numbers — a machine can retransmit 20% of its segments while
// looking perfectly idle.
type NetTCP struct {
	OutSegs     uint64 `json:"out_segs"`
	RetransSegs uint64 `json:"retrans_segs"`
	InErrs      uint64 `json:"in_errs"`
	OutRsts     uint64 `json:"out_rsts"`
	InCsumErrs  uint64 `json:"in_csum_errs"`

	// ListenDrops counts connections dropped at the listening socket: the
	// accept queue was full (ListenOverflows) or the socket was in a state
	// that could not take them. Every one is a client that got a timeout.
	ListenDrops     uint64 `json:"listen_drops"`
	ListenOverflows uint64 `json:"listen_overflows"`

	SyncookiesSent   uint64 `json:"syncookies_sent"`
	SyncookiesFailed uint64 `json:"syncookies_failed"`
	TCPTimeouts      uint64 `json:"tcp_timeouts"`

	// Known is false when neither file could be read, so a missing network
	// picture stays distinguishable from a clean one.
	Known bool `json:"known"`
}

// ReadNetTCP parses /proc/net/snmp and /proc/net/netstat.
//
// Both files come in header/value line pairs carrying far more fields than are
// used here; unknown columns are skipped by name so a newer kernel adding one
// cannot shift the parser. A column that is named but absent simply stays at
// its zero value — the kernel omits counters it does not track, and absence is
// information, not an error.
func ReadNetTCP(r Root) (NetTCP, error) {
	var n NetTCP

	data, err := r.ReadFile("proc/net/snmp")
	if err != nil {
		return NetTCP{}, fmt.Errorf("read proc/net/snmp: %w", err)
	}
	snmp := parseKVLines(data, "Tcp:")

	data, err = r.ReadFile("proc/net/netstat")
	if err != nil {
		return NetTCP{}, fmt.Errorf("read proc/net/netstat: %w", err)
	}
	ext := parseKVLines(data, "TcpExt:")

	for _, f := range []struct {
		key    string
		dst    *uint64
		table  map[string]string
		source string
	}{
		{"OutSegs", &n.OutSegs, snmp, "snmp"},
		{"RetransSegs", &n.RetransSegs, snmp, "snmp"},
		{"InErrs", &n.InErrs, snmp, "snmp"},
		{"OutRsts", &n.OutRsts, snmp, "snmp"},
		{"InCsumErrors", &n.InCsumErrs, snmp, "snmp"},
		{"ListenDrops", &n.ListenDrops, ext, "netstat"},
		{"ListenOverflows", &n.ListenOverflows, ext, "netstat"},
		{"SyncookiesSent", &n.SyncookiesSent, ext, "netstat"},
		{"SyncookiesFailed", &n.SyncookiesFailed, ext, "netstat"},
		{"TCPTimeouts", &n.TCPTimeouts, ext, "netstat"},
	} {
		raw, ok := f.table[f.key]
		if !ok {
			continue
		}
		v, err := atou(raw)
		if err != nil {
			return NetTCP{}, fmt.Errorf("proc/net/%s %s=%q: %w", f.source, f.key, raw, err)
		}
		*f.dst = v
	}

	// A TCP stack that has been up always has sent segments, so zero means the
	// Tcp: pair was absent — half a file is not a reading.
	n.Known = n.OutSegs > 0
	return n, nil
}

// parseKVLines collects the header/value pair of every line group with the
// given prefix into one map.
//
// /proc/net/snmp and /proc/net/netstat print a header line ("Tcp: RtoAlgorithm
// RtoMin ...") followed by a value line with the same prefix and one value per
// name. Later groups of the same prefix overwrite earlier ones, which matches
// the kernel's intent: the last pair is the current accounting.
func parseKVLines(data []byte, prefix string) map[string]string {
	var out map[string]string
	var header []string

	err := lines(data, func(line string) error {
		if !strings.HasPrefix(line, prefix) {
			return nil
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			return nil
		}
		if !isNumeric(fields[1]) {
			header = fields[1:]
			return nil
		}
		if len(header) == 0 || len(fields)-1 != len(header) {
			return nil
		}
		if out == nil {
			out = make(map[string]string, len(header))
		}
		for i, name := range header {
			out[name] = fields[i+1]
		}
		return nil
	})
	if err != nil {
		return nil
	}
	if out == nil {
		return map[string]string{}
	}
	return out
}

// isNumeric reports whether the whole field parses as an unsigned integer,
// which is how a header line is told from a value line: headers start with a
// name, values start with a number.
func isNumeric(s string) bool {
	_, err := strconv.ParseUint(s, 10, 64)
	return err == nil
}
