package main

import (
	"errors"
	"io"
	"testing"
)

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

func TestAIFailsLoudlyWhenUnconfigured(t *testing.T) {
	t.Setenv("PLUMB_AI_URL", "")
	t.Setenv("PLUMB_AI_MODEL", "")

	err := run([]string{"-ai", "-voice", "-root", "../../testdata/demo/cur",
		"-prev-root", "../../testdata/demo/prev", "-d", "0"}, io.Discard)
	if err == nil {
		t.Fatal("asking for -ai with nothing configured passed silently")
	}
}
