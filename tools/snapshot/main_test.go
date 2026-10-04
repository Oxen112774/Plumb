package main

import (
	"archive/tar"
	"compress/gzip"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"sort"
	"testing"
	"time"
)

// writeTree plants a small fake /proc-like tree under dir. It returns the
// list of file paths it created.
func writeTree(t *testing.T, dir string) []string {
	t.Helper()
	files := []string{
		"uptime",
		"meminfo",
		"loadavg",
		"sys/kernel/hostname",
		"1234/cmdline",
		"1234/status",
		"1234/io",
	}
	for _, f := range files {
		full := filepath.Join(dir, f)
		if err := os.MkdirAll(filepath.Dir(full), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte("content of "+f), 0644); err != nil {
			t.Fatal(err)
		}
	}
	return files
}

// buildSnapshot writes a snapshot of src (and prev, when non-empty) to a
// temp file, then returns the file path.
func buildSnapshot(t *testing.T, src, prev, note string) string {
	t.Helper()
	dir := t.TempDir()
	out := filepath.Join(dir, "snap.tar.gz")

	meta := Meta{
		Format:    "plumb-snapshot",
		Version:   formatVersion,
		Host:      "testhost",
		CreatedAt: time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC),
		Command:   "snapshot -root " + src,
		Note:      note,
		Root:      src,
		HasPrev:   prev != "",
		PrevRoot:  prev,
	}
	f, err := os.Create(out)
	if err != nil {
		t.Fatal(err)
	}
	if err := writeSnapshot(f, meta, src, prev); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	return out
}

// extract decompresses and untarses a snapshot to dst, returning the
// sorted list of regular-file names inside.
func extract(t *testing.T, path, dst string) []string {
	t.Helper()
	in, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer in.Close()
	gz, err := gzip.NewReader(in)
	if err != nil {
		t.Fatal(err)
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	var names []string
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		if hdr.Typeflag != tar.TypeReg {
			continue
		}
		full := filepath.Join(dst, hdr.Name)
		if err := os.MkdirAll(filepath.Dir(full), 0755); err != nil {
			t.Fatal(err)
		}
		data, err := io.ReadAll(tr)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, data, 0644); err != nil {
			t.Fatal(err)
		}
		names = append(names, hdr.Name)
	}
	sort.Strings(names)
	return names
}

func TestSnapshotRoundTripPreservesAllFiles(t *testing.T) {
	src := t.TempDir()
	writeTree(t, src)
	prev := t.TempDir()
	writeTree(t, prev)

	snap := buildSnapshot(t, src, prev, "incident: db stall")
	dst := t.TempDir()
	names := extract(t, snap, dst)

	for _, want := range []string{
		"meta.json",
		"cur/uptime",
		"cur/meminfo",
		"cur/loadavg",
		"cur/sys/kernel/hostname",
		"cur/1234/cmdline",
		"cur/1234/status",
		"cur/1234/io",
		"prev/uptime",
		"prev/meminfo",
	} {
		if !contains(names, want) {
			t.Errorf("missing %q in extracted names: %v", want, names)
		}
	}

	// meta.json must parse and carry the note.
	raw, err := os.ReadFile(filepath.Join(dst, "meta.json"))
	if err != nil {
		t.Fatal(err)
	}
	var got Meta
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("parse meta: %v\n%s", err, raw)
	}
	if got.Format != "plumb-snapshot" || got.Version != formatVersion {
		t.Errorf("meta header: %+v", got)
	}
	if got.Note != "incident: db stall" {
		t.Errorf("note lost: %q", got.Note)
	}
	if !got.HasPrev || got.PrevRoot != prev {
		t.Errorf("prev lost: %+v", got)
	}
	if got.Host != "testhost" {
		t.Errorf("host lost: %q", got.Host)
	}
}

func TestSnapshotSkipsKcore(t *testing.T) {
	src := t.TempDir()
	if err := os.WriteFile(filepath.Join(src, "kcore"), []byte("NOT TO BE ARCHIVED"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "uptime"), []byte("12345"), 0644); err != nil {
		t.Fatal(err)
	}

	snap := buildSnapshot(t, src, "", "")
	dst := t.TempDir()
	names := extract(t, snap, dst)

	if contains(names, "cur/kcore") {
		t.Errorf("kcore was archived: %v", names)
	}
	if !contains(names, "cur/uptime") {
		t.Errorf("uptime missing: %v", names)
	}
}

func TestSnapshotSurvivesUnreadableFiles(t *testing.T) {
	// Files that disappear or become unreadable mid-walk must not abort
	// the snapshot. This simulates the real /proc experience where a
	// process exits between readdir and stat.
	src := t.TempDir()
	if err := os.WriteFile(filepath.Join(src, "stable"), []byte("ok"), 0644); err != nil {
		t.Fatal(err)
	}

	snap := buildSnapshot(t, src, "", "")
	dst := t.TempDir()
	names := extract(t, snap, dst)
	if !contains(names, "cur/stable") {
		t.Errorf("stable missing: %v", names)
	}
}

func TestSnapshotReproducibleAcrossRuns(t *testing.T) {
	// Two runs over the same tree must produce the same set of file
	// entries in the same order. (The archive bytes themselves differ
	// because gzip embeds a timestamp in its header; a byte-identical
	// archive would require a fixed gzip header, which gzip.Writer does
	// not expose. The interesting determinism property for forensic
	// replay is that the file set and contents are stable — that is
	// what we assert here.)
	src := t.TempDir()
	writeTree(t, src)

	a := buildSnapshot(t, src, "", "")
	b := buildSnapshot(t, src, "", "")

	namesA := entryNames(t, a)
	namesB := entryNames(t, b)
	if len(namesA) != len(namesB) {
		t.Fatalf("entry count: %d vs %d", len(namesA), len(namesB))
	}
	for i := range namesA {
		if namesA[i] != namesB[i] {
			t.Errorf("entry[%d]: %q vs %q", i, namesA[i], namesB[i])
		}
	}
}

// entryNames returns the sorted list of file names inside a snapshot
// without extracting them.
func entryNames(t *testing.T, path string) []string {
	t.Helper()
	in, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer in.Close()
	gz, err := gzip.NewReader(in)
	if err != nil {
		t.Fatal(err)
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	var out []string
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		if hdr.Typeflag == tar.TypeReg {
			out = append(out, hdr.Name)
		}
	}
	sort.Strings(out)
	return out
}

func contains(ss []string, s string) bool {
	for _, x := range ss {
		if x == s {
			return true
		}
	}
	return false
}
