//go:build linux

package probe

import (
	"fmt"
	"syscall"
)

// readDiskSpace uses the statfs(2) syscall directly.
//
// The obvious alternative — parsing `df` output — costs a fork+exec and
// depends on the installed coreutils. It also blocks for as long as the
// filesystem does: a df against a hung NFS mount is exactly the failure mode
// that turns a status check into a ten-second hang. statfs on the same mount
// returns what the kernel already knows.
func readDiskSpace(path string) (DiskSpace, error) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(path, &st); err != nil {
		return DiskSpace{}, fmt.Errorf("statfs %s: %w", path, err)
	}
	return diskSpaceFromStatfs(path, uint64(st.Blocks), uint64(st.Bavail), uint64(st.Bsize)), nil
}
