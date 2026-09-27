//go:build !linux

package probe

import "errors"

// ErrUnsupported is returned by probes that need a kernel interface the
// current platform does not expose. It is a distinct error, not an empty
// result, so callers can tell "this cannot be measured here" apart from
// "this measured as zero".
var ErrUnsupported = errors.New("probe: not supported on this platform")

func readDiskSpace(path string) (DiskSpace, error) {
	return DiskSpace{}, ErrUnsupported
}
