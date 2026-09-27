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
	Memory   Memory
	Pressure Pressure
	Disks    []DiskIO
	RootFS   DiskSpace
	Procs    []Process

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

	cores, err := CountCores(r)
	if err != nil {
		s.Errors = append(s.Errors, err.Error())
		cores = runtime.NumCPU()
	}
	sys, err := ReadSystem(r, cores)
	if err != nil {
		s.Errors = append(s.Errors, err.Error())
	}
	s.System = sys
	if s.System.Cores == 0 {
		s.System.Cores = cores
	}

	if cpu, err := ReadCPU(r); err != nil {
		s.Errors = append(s.Errors, err.Error())
	} else {
		s.CPU = cpu
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

	return s
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
