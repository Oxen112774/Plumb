// Package probe reads kernel-exposed system state.
//
// Every measurement in this package comes from a direct read of /proc or
// /sys. No external command is ever executed.
//
// That is a deliberate constraint, not a style preference. Shelling out to
// df/ps/ss costs a fork+exec per call (6-10 of them per round on a small
// server, more with every extra service), ties correctness to the output
// format of whichever coreutils version happens to be installed, and gets
// slowest at exactly the moment you need it most — when the machine is
// saturated and the scheduler has no room for your child processes. Reading
// /proc/stat is a copy out of kernel memory; it cannot block on a hung mount
// and it does not care about locale.
package probe

import (
	"bufio"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// errStop ends line iteration early. It is never propagated to callers.
var errStop = errors.New("stop")

// DefaultRoot is the filesystem root used at runtime.
const DefaultRoot = "/"

// Root resolves paths beneath a filesystem root.
//
// Production code uses "/". Tests and the built-in demo point it at a fixture
// tree, which is how a Linux-only tool stays testable on the Windows machine
// it is developed on.
type Root struct{ root string }

// NewRoot returns a Root; an empty string means the real filesystem root.
func NewRoot(root string) Root {
	if root == "" {
		root = DefaultRoot
	}
	return Root{root: root}
}

// Path joins a /proc-relative path onto the root.
func (r Root) Path(rel string) string {
	rel = strings.TrimPrefix(rel, "/")
	if r.root == DefaultRoot {
		return "/" + rel
	}
	return filepath.Join(r.root, rel)
}

// ReadFile reads a /proc-relative path.
func (r Root) ReadFile(rel string) ([]byte, error) { return os.ReadFile(r.Path(rel)) }

// Exists reports whether a /proc-relative path is present.
func (r Root) Exists(rel string) bool {
	_, err := os.Stat(r.Path(rel))
	return err == nil
}

// ReadDir lists a /proc-relative directory.
func (r Root) ReadDir(rel string) ([]os.DirEntry, error) { return os.ReadDir(r.Path(rel)) }

// ReadLink resolves a /proc-relative symlink.
//
// /proc/<pid>/fd/<n> is a symlink whose target names the open file, which is
// the only way to learn *which* file a process is working on — there is no
// other interface to it.
//
// A fixture tree cannot always hold real symlinks: creating one on Windows
// requires a privilege an unprivileged process does not have. The fixture
// format therefore stores a link target as the content of a regular file, and
// this falls back to reading it. The fallback cannot hide a real failure —
// under a live /proc these paths are always symlinks, so a failed readlink
// means the descriptor is gone or the read was denied, and the file read then
// fails the same way.
func (r Root) ReadLink(rel string) (string, error) {
	target, err := os.Readlink(r.Path(rel))
	if err == nil {
		return target, nil
	}
	data, ferr := os.ReadFile(r.Path(rel))
	if ferr != nil {
		return "", err // report the readlink error; it is the informative one
	}
	s := strings.TrimSpace(string(data))
	if s == "" {
		return "", err
	}
	return s, nil
}

// lines iterates over data line by line. A callback returning errStop ends
// iteration cleanly.
func lines(data []byte, fn func(string) error) error {
	sc := bufio.NewScanner(strings.NewReader(string(data)))
	sc.Buffer(make([]byte, 0, 64<<10), 1<<20)
	for sc.Scan() {
		if err := fn(sc.Text()); err != nil {
			if errors.Is(err, errStop) {
				return nil
			}
			return err
		}
	}
	return sc.Err()
}

// atou parses an unsigned decimal field.
//
// It returns an error rather than a zero: a counter that failed to parse must
// never be indistinguishable from a counter that is genuinely zero. In a
// monitoring tool a silent zero reads as "disk 0% full", which means no alert.
func atou(s string) (uint64, error) {
	return strconv.ParseUint(strings.TrimSpace(s), 10, 64)
}

// atof parses a decimal float field.
func atof(s string) (float64, error) {
	return strconv.ParseFloat(strings.TrimSpace(s), 64)
}
