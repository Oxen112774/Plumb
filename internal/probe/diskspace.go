package probe

// diskSpaceFromStatfs converts raw statfs counters into a DiskSpace.
//
// The arithmetic lives here, apart from the syscall that produces the raw
// numbers, so it can be exercised on any platform — this is where the
// off-by-one and overflow bugs actually are.
//
// Note that free space uses f_bavail (blocks available to an unprivileged
// user), not f_bfree: the reserved 5% on a root filesystem is not available
// to the applications that will be filling the disk, so counting it would
// report room that is not there.
func diskSpaceFromStatfs(path string, blocks, bavail, bsize uint64) DiskSpace {
	total := blocks * bsize
	free := bavail * bsize
	var used uint64
	if total >= free {
		used = total - free
	}
	return DiskSpace{
		Path:       path,
		TotalBytes: total,
		FreeBytes:  free,
		UsedBytes:  used,
		UsedPct:    pct(used, total),
	}
}
