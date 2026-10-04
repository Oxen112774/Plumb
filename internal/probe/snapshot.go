package probe

import (
	"fmt"
	"runtime"
	"time"
)

// Snapshot is one complete reading of the machine.
type Snapshot struct {
	At       time.Time
	System   System
	CPU      CPUTimes
	CPUCores []CPUCore
	Memory   Memory
	Pressure Pressure
	Disks    []DiskIO
	RootFS   DiskSpace
	Procs    []Process
	Net      NetTCP
	Cgroup   Cgroup

	// K8s is the pod this process was placed in, when a kubelet placed it in
	// one. It is read from the same hierarchy as Cgroup and is carried
	// alongside it because the two answer different questions: Cgroup says
	// what the limits are, K8s says whose limits they are.
	K8s K8sContext `json:"k8s,omitempty"`

	// Threads and OpenFiles are filled only by AttachThreadScan, and only on
	// the later of the two samples. Walking every thread table costs a
	// readdir plus a read per thread; more to the point, it answers "what is
	// stuck right now", which is a question about an instant, not a rate.
	Threads   ThreadScan          `json:"threads,omitempty"`
	OpenFiles map[int]OpenFileSet `json:"open_files,omitempty"`

	// Errors records probes that could not be completed. They are carried in
	// the snapshot rather than logged and dropped so the report can state
	// plainly which measurements are missing — a diagnosis that silently
	// omits the metric that mattered is worse than one that says it could
	// not look.
	Errors []string
}

// Take performs one full probe pass. /proc is read once per subsystem; no
// external process is started.
func Take(r Root, mountPoint string) Snapshot {
	s := Snapshot{At: time.Now()}

	// /proc/stat is read once and serves both the CPU counters and the core
	// count; reading it twice would copy the same kernel data twice for no
	// reason.
	agg, perCore, err := ReadCPUAll(r)
	if err != nil {
		s.Errors = append(s.Errors, err.Error())
	}
	s.CPU, s.CPUCores = agg, perCore

	cores := len(perCore)
	if cores == 0 {
		if err == nil {
			s.Errors = append(s.Errors, "proc/stat: no per-cpu lines")
		}
		cores = runtime.NumCPU()
	}

	// The cgroup envelope is read before anything that divides by core count:
	// in a container the host's cores are not this workload's cores, and load
	// per core read against the wrong denominator is a confident wrong number.
	s.Cgroup, err = ReadCgroup(r)
	if err != nil {
		s.Errors = append(s.Errors, err.Error())
	}
	if s.Cgroup.V2 {
		s.Cgroup.HostCores = cores
		cores = EffectiveCores(cores, s.Cgroup.CPUQuotaCores)
	}
	// Read after the hierarchy has been found, not before: on a host with no
	// cgroup v2 there is no kubelet-shaped path to parse either, and the
	// ordering keeps the two from disagreeing about which root was used.
	s.K8s = ReadK8sContext(r)

	sys, err := ReadSystem(r, cores)
	if err != nil {
		s.Errors = append(s.Errors, err.Error())
	}
	s.System = sys
	if s.System.Cores == 0 {
		s.System.Cores = cores
	}

	if mem, err := ReadMemory(r); err != nil {
		s.Errors = append(s.Errors, err.Error())
	} else {
		s.Memory = mem
	}

	s.Pressure = ReadPressure(r)
	if !s.Pressure.Supported {
		s.Errors = append(s.Errors, "PSI unavailable (kernel < 4.20 or psi disabled); falling back to utilisation")
	}

	if disks, err := ReadDiskStats(r); err != nil {
		s.Errors = append(s.Errors, err.Error())
	} else {
		s.Disks = disks
	}

	if space, err := ReadDiskSpace(mountPoint); err != nil {
		s.Errors = append(s.Errors, fmt.Sprintf("disk space for %s: %v", mountPoint, err))
	} else {
		s.RootFS = space
	}

	pids, err := ListPIDs(r)
	if err != nil {
		s.Errors = append(s.Errors, err.Error())
	} else {
		s.Procs = ReadProcesses(r, pids, ReadUsers(r))
	}

	if net, err := ReadNetTCP(r); err != nil {
		s.Errors = append(s.Errors, err.Error())
	} else {
		s.Net = net
	}

	return s
}

// AttachThreadScan walks the thread tables and records which processes have
// threads stuck in uninterruptible sleep, together with the files those
// processes hold open.
//
// openFileLimit caps how many paths are recorded per blocked process; pass 0
// for no cap.
//
// A failure here is reported, not swallowed. On a kernel where
// CONFIG_TASK_DELAY_ACCT is off, or where /proc/<pid>/task is unreadable, the
// tool must say that it could not look rather than print a clean bill of
// health it did not earn.
func AttachThreadScan(r Root, s *Snapshot, openFileLimit int) {
	pids := make([]int, 0, len(s.Procs))
	for _, p := range s.Procs {
		pids = append(pids, p.PID)
	}

	s.Threads = ReadThreadScan(r, pids)
	if s.Threads.Total == 0 {
		s.Errors = append(s.Errors, "blocked-thread detection: no thread tables could be read")
		return
	}

	blocked := s.Threads.BlockedProcesses()
	if len(blocked) == 0 {
		return
	}

	s.OpenFiles = make(map[int]OpenFileSet, len(blocked))
	for _, bp := range blocked {
		set := ReadOpenFiles(r, bp.PID, openFileLimit)
		if set.Distinct == 0 {
			continue
		}
		s.OpenFiles[bp.PID] = set
	}
}

// ProcessByPID indexes a snapshot's processes.
func (s Snapshot) ProcessByPID() map[int]Process {
	m := make(map[int]Process, len(s.Procs))
	for _, p := range s.Procs {
		m[p.PID] = p
	}
	return m
}

// DiskByName indexes a snapshot's block devices.
func (s Snapshot) DiskByName() map[string]DiskIO {
	m := make(map[string]DiskIO, len(s.Disks))
	for _, d := range s.Disks {
		m[d.Name] = d
	}
	return m
}

// CoreLoads computes each core's utilisation between two snapshots.
func (s Snapshot) CoreLoads(prev Snapshot) []CoreLoad {
	return CoreLoads(prev.CPUCores, s.CPUCores)
}
