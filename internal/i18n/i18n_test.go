package i18n

import "testing"

// TestIsNoLocale covers one setting with several spellings.
//
// C.UTF-8 is what Debian and Ubuntu fall back to when no locale has been
// generated, which is the default state of a fresh cloud image. Treating it as
// a language answers "English" on a machine whose operator never made that
// choice — while treating plain "C" as no locale. The bug this locks down is
// not the string comparison, it is that two spellings of the same answer went
// down two different paths.
func TestIsNoLocale(t *testing.T) {
	for _, v := range []string{"", "C", "C.UTF-8", "C.utf8", "POSIX", "POSIX.UTF-8"} {
		if !IsNoLocale(v) {
			t.Errorf("IsNoLocale(%q) = false, want true", v)
		}
	}
	for _, v := range []string{"zh_CN.UTF-8", "en_US.UTF-8", "ja_JP.UTF-8"} {
		if IsNoLocale(v) {
			t.Errorf("IsNoLocale(%q) = true, want false", v)
		}
	}
}

func TestDetect(t *testing.T) {
	cases := []struct {
		name    string
		lcAll   string
		lcMsg   string
		lang    string
		want    Lang
		comment string
	}{
		{"chinese", "zh_CN.UTF-8", "", "", ZH, "explicit zh"},
		{"english", "en_US.UTF-8", "", "", EN, "explicit en"},
		{"c-utf8 is no choice", "C.UTF-8", "", "", ZH, "falls through to the default"},
		{"LC_ALL wins", "zh_CN.UTF-8", "en_US.UTF-8", "en_US.UTF-8", ZH, "order glibc uses"},
		{"LANG used last", "", "", "zh_TW.UTF-8", ZH, "prefix match on zh"},
		{"nothing set", "", "", "", ZH, "no locale is not English"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("LC_ALL", tc.lcAll)
			t.Setenv("LC_MESSAGES", tc.lcMsg)
			t.Setenv("LANG", tc.lang)
			if got := Detect(); got != tc.want {
				t.Errorf("Detect() = %q, want %q (%s)", got, tc.want, tc.comment)
			}
		})
	}
}

func TestResolveOverridesDetection(t *testing.T) {
	t.Setenv("LANG", "zh_CN.UTF-8")
	t.Setenv("LC_ALL", "zh_CN.UTF-8")
	if got := Resolve("en"); got != EN {
		t.Errorf(`Resolve("en") = %q, want en: an explicit choice beats the locale`, got)
	}
	if got := Resolve("EN_US"); got != EN {
		t.Errorf(`Resolve("EN_US") = %q, want en`, got)
	}
	if got := Resolve("auto"); got != ZH {
		t.Errorf(`Resolve("auto") = %q, want the detected zh`, got)
	}
	// An unrecognised value is not a third language; it falls back to
	// detection rather than to a language nobody asked for.
	if got := Resolve("klingon"); got != ZH {
		t.Errorf(`Resolve("klingon") = %q, want detection`, got)
	}
}

// TestGetFallsBackRatherThanGoingEmpty is the reason a missing translation is
// visible: an empty string in a report is a bug nobody attributes to i18n.
func TestGetFallsBackRatherThanGoingEmpty(t *testing.T) {
	if got := Get(EN, Findings); got == "" {
		t.Error("English findings label is empty")
	}
	if got := Get(ZH, Findings); got == "" {
		t.Error("Chinese findings label is empty")
	}
	// A key with no entry at all returns the key itself, so the gap shows up
	// in the output instead of silently vanishing.
	missing := T("no_such_key")
	if got := Get(EN, missing); got != string(missing) {
		t.Errorf("Get of an unknown key = %q, want the key", got)
	}
}
