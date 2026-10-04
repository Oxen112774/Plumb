package config

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Oxen112774/plumb/internal/diagnose"
)

func TestMissingFileIsNotAnError(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	// No HOME fallback either, so a read failure cannot quietly succeed by
	// finding a real file elsewhere.
	t.Setenv("HOME", dir)
	t.Setenv("USERPROFILE", dir)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load with no file: %v", err)
	}
	if cfg != Default() {
		t.Errorf("missing file did not yield defaults:\n got %+v\nwant %+v", cfg, Default())
	}
}

func TestThresholdsSectionIsRead(t *testing.T) {
	cfg := Default()
	got, err := parse(bufio.NewScanner(strings.NewReader(
		"[thresholds]\nio_full_warn = 5\nio_full_critical = 25\n")), cfg)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if got.Thresholds.IOFullWarn != 5 {
		t.Errorf("io_full_warn = %v, want 5", got.Thresholds.IOFullWarn)
	}
	if got.Thresholds.IOFullCritical != 25 {
		t.Errorf("io_full_critical = %v, want 25", got.Thresholds.IOFullCritical)
	}
	// Untouched fields keep the built-in values, not zero.
	if got.Thresholds.CPUSomeWarn != diagnose.DefaultThresholds().CPUSomeWarn {
		t.Errorf("cpu_some_warn was zeroed: %v", got.Thresholds.CPUSomeWarn)
	}
}

// TestRemovedWarnKeysAreRefused covers a key that used to exist and never did
// anything. It is refused by name rather than ignored, because a user who sets
// warn_io believes they tightened a check, and silence is what let that
// believe survive a whole release.
func TestRemovedWarnKeysAreRefused(t *testing.T) {
	for _, key := range []string{"warn_cpu", "warn_memory", "warn_io"} {
		_, err := parse(bufio.NewScanner(strings.NewReader(key+" = 50\n")), Default())
		if err == nil {
			t.Errorf("%s was accepted; it must name its replacement or fail", key)
			continue
		}
		if !strings.Contains(err.Error(), key) {
			t.Errorf("error for %s does not name the key: %v", key, err)
		}
		if !strings.Contains(err.Error(), "thresholds") {
			t.Errorf("error for %s does not point at [thresholds]: %v", key, err)
		}
	}
}

func TestUnknownKeyFails(t *testing.T) {
	_, err := parse(bufio.NewScanner(strings.NewReader("nonsense = 1\n")), Default())
	if err == nil {
		t.Fatal("unknown key accepted")
	}
	if !strings.Contains(err.Error(), "line 1") {
		t.Errorf("error does not say which line: %v", err)
	}
}

// TestSaveOnlyWritesMovedThresholds is about what the file means. Writing all
// eleven cut-offs would make the file look authoritative about numbers the
// user never chose, and a reader would have to work out which lines are theirs.
func TestSaveOnlyWritesMovedThresholds(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	t.Setenv("HOME", dir)
	t.Setenv("USERPROFILE", dir)

	cfg := Default()
	cfg.Thresholds.IOFullWarn = 7
	if err := Save(cfg); err != nil {
		t.Fatalf("Save: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(dir, "plumb", "config.toml"))
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	text := string(data)
	if !strings.Contains(text, "io_full_warn = 7") {
		t.Errorf("moved threshold not written:\n%s", text)
	}
	if strings.Contains(text, "cpu_some_warn") {
		t.Errorf("untouched threshold was written:\n%s", text)
	}
	if !strings.Contains(text, "内置 2") {
		t.Errorf("moved threshold does not record the built-in value it replaced:\n%s", text)
	}
}

func TestThresholdsRoundTrip(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	t.Setenv("HOME", dir)
	t.Setenv("USERPROFILE", dir)

	want := Default()
	want.Thresholds.MemFullCritical = 33
	if err := Save(want); err != nil {
		t.Fatalf("Save: %v", err)
	}
	got, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got.Thresholds.MemFullCritical != 33 {
		t.Errorf("mem_full_critical = %v after round trip, want 33", got.Thresholds.MemFullCritical)
	}
	if len(got.Thresholds.Changed()) != 1 {
		t.Errorf("expected exactly one moved threshold, got %v", got.Thresholds.Changed())
	}
}
