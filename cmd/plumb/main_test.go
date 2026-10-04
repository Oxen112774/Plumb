package main

import (
	"errors"
	"io"
	"os"
	"strings"
	"testing"
)

// captureStreams swaps os.Stdout and os.Stderr for pipes while fn runs and
// returns what was written to each. run() writes the report to os.Stdout and
// the model's warnings to os.Stderr directly, so this is the only way to see
// which branch it took.
func captureStreams(t *testing.T, fn func()) (stdout, stderr string) {
	t.Helper()
	outR, outW, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	errR, errW, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	origOut, origErr := os.Stdout, os.Stderr
	os.Stdout, os.Stderr = outW, errW
	defer func() { os.Stdout, os.Stderr = origOut, origErr }()

	drain := func(r *os.File) chan string {
		ch := make(chan string, 1)
		go func() {
			b, _ := io.ReadAll(r)
			ch <- string(b)
		}()
		return ch
	}
	outCh, errCh := drain(outR), drain(errR)

	fn()
	outW.Close()
	errW.Close()
	return <-outCh, <-errCh
}

// The exit-code contract only holds if a bad command line can never produce 2.
// 2 means "this machine has a problem"; a mistyped flag producing it would turn a
// typo into a 3am alert, which is the one confusion a monitoring tool must not
// ship.
func TestBadCommandLineIsNotAProblemExitCode(t *testing.T) {
	err := run([]string{"-nosuchflag"}, io.Discard)
	if err == nil {
		t.Fatal("an unknown flag was accepted")
	}
	if !errors.Is(err, errUsage) {
		t.Errorf("err = %v, want errUsage so main exits 1 rather than 2", err)
	}
}

func TestMutuallyExclusiveFlagsAreRejected(t *testing.T) {
	fixture := []string{"-root", "../../testdata/demo/cur", "-prev-root", "../../testdata/demo/prev", "-d", "0"}

	cases := map[string][]string{
		"-json with -voice": append([]string{"-json", "-voice"}, fixture...),
		"-json with -watch": append([]string{"-json", "-watch"}, fixture...),
		"-assert with -watch": append([]string{"-assert", "-watch"},
			fixture...),
		"-every without -watch":  append([]string{"-every", "5s"}, fixture...),
		"-rounds without -watch": append([]string{"-rounds", "2"}, fixture...),
	}
	for name, args := range cases {
		err := run(args, io.Discard)
		if err == nil {
			t.Errorf("%s was accepted", name)
			continue
		}
		if errors.Is(err, errUsage) {
			t.Errorf("%s produced errUsage; it is a validation error and should "+
				"print its own explanation", name)
		}
	}
}

func TestMissingProcIsExplained(t *testing.T) {
	err := run([]string{"-root", "../../testdata/does-not-exist"}, io.Discard)
	if err == nil {
		t.Fatal("a root without proc/ was accepted")
	}
}

func TestVersionAndHappyPathReturnNil(t *testing.T) {
	if err := run([]string{"-version"}, io.Discard); err != nil {
		t.Errorf("-version: %v", err)
	}
	err := run([]string{"-root", "../../testdata/demo/cur",
		"-prev-root", "../../testdata/demo/prev", "-d", "0", "-no-color"}, io.Discard)
	if err != nil {
		t.Errorf("a plain run on the fixture failed: %v", err)
	}
}

// -ai 补的话只出现在给人读的输出里，所以它必须自己把 -voice 打开；否则这个
// flag 会被收下却什么也不做。
func TestAIImpliesVoice(t *testing.T) {
	t.Setenv("PLUMB_AI_URL", "http://127.0.0.1:1/v1/chat/completions")
	t.Setenv("PLUMB_AI_MODEL", "test")

	// 这句泄漏提示只在模型那一段真的被调用时才出现。修复前 -ai 单独使用会走
	// report.Text，模型从不被调用，这里收不到任何东西。
	_, stderr := captureStreams(t, func() {
		_ = run([]string{"-ai", "-root", "../../testdata/demo/cur",
			"-prev-root", "../../testdata/demo/prev", "-d", "0", "-no-color"}, io.Discard)
	})
	if !strings.Contains(stderr, "会发送给") {
		t.Errorf("-ai 单独使用没有触达模型，等于收下 flag 却不做事；stderr = %q", stderr)
	}
}

// -ai 补的话进不了 JSON，所以两者放在一起是用法矛盾，要在采样前就拒绝。
func TestAIWithJSONIsRejected(t *testing.T) {
	err := run([]string{"-ai", "-json", "-root", "../../testdata/demo/cur",
		"-prev-root", "../../testdata/demo/prev", "-d", "0"}, io.Discard)
	if err == nil {
		t.Fatal("-ai 与 -json 一起用被接受了")
	}
}

func TestAIFailsLoudlyWhenUnconfigured(t *testing.T) {
	t.Setenv("PLUMB_AI_URL", "")
	t.Setenv("PLUMB_AI_MODEL", "")

	err := run([]string{"-ai", "-voice", "-root", "../../testdata/demo/cur",
		"-prev-root", "../../testdata/demo/prev", "-d", "0"}, io.Discard)
	if err == nil {
		t.Fatal("asking for -ai with nothing configured passed silently")
	}
}
