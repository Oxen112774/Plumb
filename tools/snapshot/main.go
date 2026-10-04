// Command snapshot packages a /proc tree (or any filesystem tree) into a
// tarball that plumb can read back later via -root. The format is a
// gzipped tar with three sections:
//
//	meta.json  timestamp, hostname, plumb version, command line, note
//	cur/       the tree at -root, in full
//	prev/      the tree at -prev-root, if provided
//
// The point of the format is forensic replay: when something breaks on a
// machine three months from now, the snapshot is the proof of what plumb
// would have said at the time. That is what "安心" means in this project.
//
// Design constraints, all from the project's "no" list:
//
//   - The tool does not read /proc itself. It walks a directory, period.
//     Snapshotting a system that does not support plumb is still safe.
//   - It does not modify the source tree.
//   - It does not spawn subprocesses.
//   - It does not open network connections.
//
// Usage:
//
//	snapshot -root /proc [-prev-root /tmp/proc-prev] -out snap.tar.gz [-note "..."]
package main

import (
	"archive/tar"
	"compress/gzip"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const formatVersion = 1

// skipNames is the set of files under the tree that should never be copied
// into a snapshot. /proc's kcore is the size of physical memory;
// /proc/kmsg is a privileged stream that blocks forever when read;
// /proc/kallsyms is restricted on hardened kernels. All three would fail
// the snapshot in ways the user did not ask for, so they are excluded by
// name rather than by error.
var skipNames = map[string]bool{
	"kcore":    true,
	"kmsg":     true,
	"kallsyms": true,
}

// Meta is the JSON document at the root of every snapshot. Every field is
// either useful for forensic replay (timestamps, hostname, command line)
// or essential for tooling (format + version, so future readers can detect
// an unknown snapshot and refuse rather than guess).
type Meta struct {
	Format    string    `json:"format"`         // "plumb-snapshot"
	Version   int       `json:"format_version"` // bump on breaking changes
	Plumb     string    `json:"plumb_version"`  // empty when unknown
	Host      string    `json:"hostname,omitempty"`
	CreatedAt time.Time `json:"created_at"`
	Command   string    `json:"command"` // tool's own argv, for reproducibility
	Note      string    `json:"note,omitempty"`
	Root      string    `json:"root"` // path of the source tree
	HasPrev   bool      `json:"has_prev"`
	PrevRoot  string    `json:"prev_root,omitempty"`
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "snapshot:", err)
		os.Exit(1)
	}
}

func run() error {
	fs := flag.NewFlagSet("snapshot", flag.ContinueOnError)
	root := fs.String("root", "/proc", "filesystem tree to snapshot")
	prev := fs.String("prev-root", "", "optional second tree (also included)")
	out := fs.String("out", "-", "output path (- for stdout)")
	note := fs.String("note", "", "user note attached to meta.json")
	host := fs.String("hostname", "", "override hostname (default: read from root/sys/kernel/hostname)")
	plumbVer := fs.String("plumb-version", "", "plumb version recorded in meta.json")
	if err := fs.Parse(os.Args[1:]); err != nil {
		return err
	}
	if *root == "" {
		return fmt.Errorf("-root is required")
	}

	meta := Meta{
		Format:    "plumb-snapshot",
		Version:   formatVersion,
		Plumb:     *plumbVer,
		CreatedAt: time.Now(),
		Command:   "snapshot " + strings.Join(os.Args[1:], " "),
		Note:      *note,
		Root:      *root,
		HasPrev:   *prev != "",
		PrevRoot:  *prev,
	}
	if *host != "" {
		meta.Host = *host
	} else {
		meta.Host = readHostname(*root)
	}

	w, closer, err := openWriter(*out)
	if err != nil {
		return err
	}
	defer closer()

	return writeSnapshot(w, meta, *root, *prev)
}

func openWriter(path string) (io.Writer, func(), error) {
	if path == "-" {
		return os.Stdout, func() {}, nil
	}
	f, err := os.Create(path)
	if err != nil {
		return nil, nil, err
	}
	return f, func() { f.Close() }, nil
}

// readHostname pulls the hostname out of the tree being snapshotted rather
// than calling os.Hostname(): the source tree is the authority for what
// plumb would have read at capture time. If the file is missing or
// unreadable, the field is left empty rather than guessed.
func readHostname(root string) string {
	data, err := os.ReadFile(filepath.Join(root, "sys/kernel/hostname"))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

func writeSnapshot(w io.Writer, meta Meta, root, prev string) error {
	gz := gzip.NewWriter(w)
	defer gz.Close()
	tw := tar.NewWriter(gz)
	defer tw.Close()

	// meta.json goes first so even an interrupted snapshot has the header.
	metaBytes, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		return fmt.Errorf("encode meta: %w", err)
	}
	if err := writeTarFile(tw, "meta.json", metaBytes, 0644); err != nil {
		return err
	}

	if err := walkTree(tw, root, "cur"); err != nil {
		return fmt.Errorf("walk %s: %w", root, err)
	}
	if prev != "" {
		if err := walkTree(tw, prev, "prev"); err != nil {
			return fmt.Errorf("walk %s: %w", prev, err)
		}
	}
	return nil
}

// walkTree walks the source tree and writes each regular file into the tar
// under <prefix>/<relative path>. Errors on individual files (file
// disappeared between readdir and stat, file is unreadable, ...) are
// swallowed: a snapshot is forensically useful even when it is partial,
// and the user's command line is the record of which tree they chose to
// archive. Failing the whole snapshot over one file would be a betrayal
// of that purpose.
func walkTree(tw *tar.Writer, root, prefix string) error {
	return filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		name := filepath.Base(path)
		if skipNames[name] {
			if info.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if !info.Mode().IsRegular() {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		arc := filepath.ToSlash(filepath.Join(prefix, rel))
		return writeTarFile(tw, arc, data, int64(info.Mode().Perm()))
	})
}

// writeTarFile writes a single regular file. ModTime is the zero time so
// that running the tool twice on the same tree produces byte-identical
// archives (modulo the meta.json created_at).
func writeTarFile(tw *tar.Writer, name string, data []byte, mode int64) error {
	name = filepath.ToSlash(name)
	hdr := &tar.Header{
		Name:    name,
		Mode:    mode,
		Size:    int64(len(data)),
		ModTime: time.Time{},
	}
	if err := tw.WriteHeader(hdr); err != nil {
		return err
	}
	_, err := tw.Write(data)
	return err
}
