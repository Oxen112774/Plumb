package probe

import "testing"

// Both network files are a header line followed by a value line with the same
// prefix and one value per name. The prefix is what selects the table, so an
// Ip: pair in the same file must not be read as TCP counters.
const snmpSample = `Ip: Forwarding DefaultTTL InReceives InHdrErrors InAddrErrors
Ip: 2 64 12345678 0 3
Icmp: InMsgs InErrors
Icmp: 1200 0
Tcp: RtoAlgorithm RtoMin RtoMax MaxConn ActiveOpens PassiveOpens AttemptFails EstabResets CurrEstab InSegs OutSegs RetransSegs InErrs OutRsts InCsumErrors
Tcp: 1 200 120000 -1 12345 54321 12 34 56 9000000 6000000 120000 3 40 0
Udp: InDatagrams NoPorts
Udp: 8000 5
`

// TcpExt carries the counters /proc/net/snmp does not: the connections that
// were refused at the listen queue, and the SYN cookie accounting.
const netstatSample = `TcpExt: SyncookiesSent SyncookiesRecv SyncookiesFailed EmbryonicRsts PruneCalled ListenDrops ListenOverflows TCPTimeouts TCPSpuriousRTOs
TcpExt: 0 0 12 5 1000 37 30 900 2
IpExt: InOctets OutOctets
IpExt: 900000000 800000000
`

func netFiles() map[string]string {
	return map[string]string{
		"proc/net/snmp":    snmpSample,
		"proc/net/netstat": netstatSample,
	}
}

func TestReadNetTCPReadsBothTables(t *testing.T) {
	n, err := ReadNetTCP(fixture(t, netFiles()))
	if err != nil {
		t.Fatalf("ReadNetTCP: %v", err)
	}
	if n.OutSegs != 6000000 {
		t.Errorf("OutSegs = %d, want 6000000", n.OutSegs)
	}
	if n.RetransSegs != 120000 {
		t.Errorf("RetransSegs = %d, want 120000", n.RetransSegs)
	}
	if n.InErrs != 3 || n.OutRsts != 40 {
		t.Errorf("InErrs = %d, OutRsts = %d, want 3 and 40", n.InErrs, n.OutRsts)
	}
	if n.ListenDrops != 37 {
		t.Errorf("ListenDrops = %d, want 37", n.ListenDrops)
	}
	if n.ListenOverflows != 30 {
		t.Errorf("ListenOverflows = %d, want 30", n.ListenOverflows)
	}
	if n.SyncookiesFailed != 12 {
		t.Errorf("SyncookiesFailed = %d, want 12", n.SyncookiesFailed)
	}
	if n.TCPTimeouts != 900 {
		t.Errorf("TCPTimeouts = %d, want 900", n.TCPTimeouts)
	}
	// A stack that has been up has sent segments; zero would mean the Tcp:
	// pair never appeared, which is not a reading.
	if !n.Known {
		t.Error("Known = false with 6000000 segments sent")
	}
}

// A newer kernel adding a column must not shift the parser. Every field is
// looked up by name, and a name that is not there simply stays at zero — the
// kernel omits counters it does not track, and absence is information.
func TestReadNetTCPIgnoresUnknownColumns(t *testing.T) {
	files := netFiles()
	files["proc/net/snmp"] = `Tcp: RtoAlgorithm Extra NewColumn InSegs OutSegs RetransSegs AlsoNew
Tcp: 1 7 8 9000000 6000000 120000 9
`
	n, err := ReadNetTCP(fixture(t, files))
	if err != nil {
		t.Fatalf("ReadNetTCP: %v", err)
	}
	if n.OutSegs != 6000000 || n.RetransSegs != 120000 {
		t.Errorf("OutSegs = %d, RetransSegs = %d, want 6000000 and 120000 — an added column shifted the parser",
			n.OutSegs, n.RetransSegs)
	}
	if n.InErrs != 0 {
		t.Errorf("InErrs = %d, want 0 when the column is absent", n.InErrs)
	}
}

// A file without the Tcp: pair is not a clean network, and must not be
// reported as one: 0% retransmission on a machine whose counters were never
// read is exactly the kind of number that ends an investigation early.
func TestReadNetTCPKnownIsFalseWithoutTheTcpPair(t *testing.T) {
	files := netFiles()
	files["proc/net/snmp"] = "Ip: Forwarding DefaultTTL\nIp: 2 64\n"

	n, err := ReadNetTCP(fixture(t, files))
	if err != nil {
		t.Fatalf("ReadNetTCP: %v", err)
	}
	if n.Known {
		t.Error("Known = true with no Tcp: pair in the file")
	}
}

// Half a file is not a reading either: /proc/net/netstat is where the
// listen-queue counters live, and an absent file must be an error the report
// can carry rather than a silent zero.
func TestReadNetTCPErrorsWhenNetstatIsMissing(t *testing.T) {
	files := netFiles()
	delete(files, "proc/net/netstat")

	if _, err := ReadNetTCP(fixture(t, files)); err == nil {
		t.Fatal("want an error when /proc/net/netstat is absent, got none")
	}
}

func TestReadNetTCPErrorsOnNonNumericCounter(t *testing.T) {
	files := netFiles()
	files["proc/net/snmp"] = `Tcp: InSegs OutSegs RetransSegs
Tcp: 9000000 abc 120000
`
	if _, err := ReadNetTCP(fixture(t, files)); err == nil {
		t.Fatal("want an error for a non-numeric OutSegs, got a silent zero")
	}
}

// A header line is told from a value line by whether its first field is
// numeric. -1 (MaxConn) sits in the middle of a value line and must not make
// it look like a header.
func TestParseKVLinesSkipsGroupsWithOtherPrefixes(t *testing.T) {
	got := parseKVLines([]byte(snmpSample), "Tcp:")
	if got["OutSegs"] != "6000000" {
		t.Errorf("OutSegs = %q, want %q", got["OutSegs"], "6000000")
	}
	if _, ok := got["InReceives"]; ok {
		t.Error("an Ip: field leaked into the Tcp: table")
	}
}
