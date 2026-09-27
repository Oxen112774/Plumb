package probe

import (
	"fmt"
	"regexp"
	"strings"
)

// sectorSize is fixed at 512 bytes by the kernel block layer, regardless of
// the device's physical sector size.
const sectorSize = 512

// DiskIO is the cumulative I/O counter set for one block device, from
// /proc/diskstats.
type DiskIO struct {
	Name            string `json:"name"`
	ReadsCompleted  uint64 `json:"reads_completed"`
	SectorsRead     uint64 `json:"sectors_read"`
	WritesCompleted uint64 `json:"writes_completed"`
	SectorsWritten  uint64 `json:"sectors_written"`
	InFlight        uint64 `json:"in_flight"`
	MillisDoingIO   uint64 `json:"millis_doing_io"`
	WeightedMillis  uint64 `json:"weighted_millis"`
}

// BytesRead converts the sector counter to bytes.
func (d DiskIO) BytesRead() uint64 { return d.SectorsRead * sectorSize }

// BytesWritten converts the sector counter to bytes.
func (d DiskIO) BytesWritten() uint64 { return d.SectorsWritten * sectorSize }

// Delta subtracts an earlier sample, clamping at zero.
func (d DiskIO) Delta(prev DiskIO) DiskIO {
	sub := func(a, b uint64) uint64 {
		if a < b {
			return 0
		}
		return a - b
	}
	return DiskIO{
		Name:            d.Name,
		ReadsCompleted:  sub(d.ReadsCompleted, prev.ReadsCompleted),
		SectorsRead:     sub(d.SectorsRead, prev.SectorsRead),
		WritesCompleted: sub(d.WritesCompleted, prev.WritesCompleted),
		SectorsWritten:  sub(d.SectorsWritten, prev.SectorsWritten),
		InFlight:        d.InFlight, // a gauge, not a counter
		MillisDoingIO:   sub(d.MillisDoingIO, prev.MillisDoingIO),
		WeightedMillis:  sub(d.WeightedMillis, prev.WeightedMillis),
	}
}

// wholeDevice matches the device names that represent a real queue.
//
// Partitions (sda1, nvme0n1p1) report their own counters but share the parent
// device's queue, so counting both double-counts the same I/O. Virtual
// devices (loop, ram) are noise on a server.
var wholeDevice = regexp.MustCompile(`^(sd[a-z]+|vd[a-z]+|xvd[a-z]+|nvme\d+n\d+|mmcblk\d+|dm-\d+|md\d+)$`)

// ReadDiskStats parses /proc/diskstats, returning whole devices only.
func ReadDiskStats(r Root) ([]DiskIO, error) {
	data, err := r.ReadFile("proc/diskstats")
	if err != nil {
		return nil, fmt.Errorf("read proc/diskstats: %w", err)
	}

	var out []DiskIO
	err = lines(data, func(line string) error {
		fields := strings.Fields(line)
		// 14 fields are the documented minimum; newer kernels append
		// discard and flush counters, which we do not need.
		if len(fields) < 14 {
			return nil
		}
		name := fields[2]
		if !wholeDevice.MatchString(name) {
			return nil
		}
		var v [11]uint64
		for i := 0; i < 11; i++ {
			n, err := atou(fields[3+i])
			if err != nil {
				return fmt.Errorf("proc/diskstats %s field %d (%q): %w", name, i, fields[3+i], err)
			}
			v[i] = n
		}
		out = append(out, DiskIO{
			Name:            name,
			ReadsCompleted:  v[0],
			SectorsRead:     v[2],
			MillisDoingIO:   v[3],
			WritesCompleted: v[4],
			SectorsWritten:  v[6],
			InFlight:        v[8],
			WeightedMillis:  v[10],
		})
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// DiskSpace is free-space information for one mounted filesystem.
type DiskSpace struct {
	Path       string  `json:"path"`
	TotalBytes uint64  `json:"total_bytes"`
	FreeBytes  uint64  `json:"free_bytes"`
	UsedBytes  uint64  `json:"used_bytes"`
	UsedPct    float64 `json:"used_percent"`
}

// ReadDiskSpace reports free space for a path. The implementation is
// platform-specific; on non-Linux builds it returns ErrUnsupported.
func ReadDiskSpace(path string) (DiskSpace, error) { return readDiskSpace(path) }
