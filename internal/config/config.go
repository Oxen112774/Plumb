// Package config loads and saves plumb's user configuration.
//
// The config file lives at $XDG_CONFIG_HOME/plumb/config.toml (falling back to
// ~/.config/plumb/config.toml). It is optional: every field has a zero value
// that means "use the built-in default", and a missing file is indistinguishable
// from an empty one. Flags supplied on the command line always override the
// file — the file sets defaults, the command line sets this run.
//
// The parser is a deliberately small subset of TOML: [section] headers,
// key = "string", key = 123, key = true. It exists so the project keeps its
// zero-external-dependency property rather than pulling in a TOML library for
// twelve keys.
package config

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/Oxen112774/plumb/internal/diagnose"
)

// Config is the full user configuration. Every field is a pointer or a
// sentinel-zero so "not set" can be told apart from "set to the default".
type Config struct {
	// Lang is the output language: "zh", "en", or "auto" (detect from locale).
	Lang string

	// Color overrides colour output: "on", "off", or "auto" (follow the terminal).
	Color string

	// Style is the report layout: "compact" (the default), "raw", or "paste".
	// See StyleCompact and friends.
	Style string

	// Terminal is the glyph set: "unicode", "ascii", or "auto" (detect).
	Terminal string

	// Interval is the default sampling gap.
	Interval time.Duration

	// Top is the default number of culprit processes per finding.
	Top int

	// Thresholds are the severity cut-offs the checks judge by. They are the
	// only knobs here that change a verdict rather than the look of one.
	Thresholds diagnose.Thresholds

	// AI holds the optional model connection settings.
	AI AIConfig
}

// AIConfig is the [ai] section.
type AIConfig struct {
	URL   string
	Key   string
	Model string
}

// Style is the report layout: "compact" (the default, with sparklines and a
// thread table), "raw" (plain text, no colour or glyphs), or "paste" (wrapped
// for posting into a chat or forum).
//
// It names an output mode the flags also reach — -raw and -paste — and exists
// so the choice can be a standing one instead of something to remember. An
// explicit flag still wins: a config sets the default, the command line sets
// this run.
const (
	StyleCompact = "compact"
	StyleRaw     = "raw"
	StylePaste   = "paste"
)

// Default returns the built-in defaults.
func Default() Config {
	return Config{
		Lang:       "auto",
		Color:      "auto",
		Style:      StyleCompact,
		Terminal:   "auto",
		Interval:   time.Second,
		Top:        3,
		Thresholds: diagnose.DefaultThresholds(),
	}
}

// Path returns the config file path, or "" when neither XDG_CONFIG_HOME nor
// HOME is set.
func Path() string {
	if xdg := os.Getenv("XDG_CONFIG_HOME"); xdg != "" {
		return filepath.Join(xdg, "plumb", "config.toml")
	}
	if home, err := os.UserHomeDir(); err == nil {
		return filepath.Join(home, ".config", "plumb", "config.toml")
	}
	return ""
}

// Load reads the config file. A missing file returns Default() with no error.
func Load() (Config, error) {
	cfg := Default()
	p := Path()
	if p == "" {
		return cfg, nil
	}
	f, err := os.Open(p)
	if err != nil {
		if os.IsNotExist(err) {
			return cfg, nil
		}
		return cfg, err
	}
	defer f.Close()
	return parse(bufio.NewScanner(f), cfg)
}

// Save writes the config to its path, creating directories as needed.
func Save(cfg Config) error {
	p := Path()
	if p == "" {
		return fmt.Errorf("cannot determine config path (no XDG_CONFIG_HOME or HOME)")
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	var b strings.Builder
	b.WriteString("# plumb configuration\n")
	b.WriteString("# 每一项都可以删掉，删掉就用内置默认值。\n\n")
	b.WriteString("lang = \"" + cfg.Lang + "\"\n")
	b.WriteString("color = \"" + cfg.Color + "\"\n")
	b.WriteString("style = \"" + cfg.Style + "\"\n")
	b.WriteString("terminal = \"" + cfg.Terminal + "\"\n")
	b.WriteString("interval = \"" + cfg.Interval.String() + "\"\n")
	b.WriteString("top = " + strconv.Itoa(cfg.Top) + "\n")
	// Only thresholds the user moved are written. Writing all eleven would
	// make the file look authoritative about numbers plumb chose, and a
	// reader would then have to work out which lines are theirs.
	if changed := cfg.Thresholds.Changed(); len(changed) > 0 {
		b.WriteString("\n[thresholds]\n")
		b.WriteString("# 判定分界线。删掉任意一行就回到 plumb 的内置值。\n")
		for _, d := range changed {
			b.WriteString(d.Name + " = " + strconv.FormatFloat(d.Value, 'f', -1, 64) +
				"   # 内置 " + strconv.FormatFloat(d.Built, 'f', -1, 64) + "\n")
		}
	}
	if cfg.AI.URL != "" || cfg.AI.Key != "" || cfg.AI.Model != "" {
		b.WriteString("\n[ai]\n")
		b.WriteString("url = \"" + cfg.AI.URL + "\"\n")
		b.WriteString("key = \"" + cfg.AI.Key + "\"\n")
		b.WriteString("model = \"" + cfg.AI.Model + "\"\n")
	}
	return os.WriteFile(p, []byte(b.String()), 0o644)
}

// parse reads a minimal TOML subset into cfg.
func parse(s *bufio.Scanner, cfg Config) (Config, error) {
	section := ""
	lineNo := 0
	for s.Scan() {
		lineNo++
		line := strings.TrimSpace(s.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			section = strings.TrimSpace(line[1 : len(line)-1])
			continue
		}
		key, val, ok := strings.Cut(line, "=")
		if !ok {
			return cfg, fmt.Errorf("config line %d: missing '='", lineNo)
		}
		key = strings.TrimSpace(key)
		val = strings.TrimSpace(stripComment(val))
		if err := cfg.set(section, key, val); err != nil {
			return cfg, fmt.Errorf("config line %d: %w", lineNo, err)
		}
	}
	return cfg, s.Err()
}

func (c *Config) set(section, key, val string) error {
	if section == "ai" {
		switch key {
		case "url":
			c.AI.URL = unquote(val)
		case "key":
			c.AI.Key = unquote(val)
		case "model":
			c.AI.Model = unquote(val)
		default:
			return fmt.Errorf("unknown [ai] key %q", key)
		}
		return nil
	}
	if section == "thresholds" {
		f, err := strconv.ParseFloat(val, 64)
		if err != nil {
			return fmt.Errorf("%s: %w", key, err)
		}
		if !c.Thresholds.Set(key, f) {
			return fmt.Errorf("unknown [thresholds] key %q", key)
		}
		return nil
	}
	switch key {
	case "lang":
		c.Lang = unquote(val)
	case "color":
		c.Color = unquote(val)
	case "style":
		c.Style = unquote(val)
	case "terminal":
		c.Terminal = unquote(val)
	case "interval":
		d, err := time.ParseDuration(unquote(val))
		if err != nil {
			return fmt.Errorf("interval: %w", err)
		}
		c.Interval = d
	case "top":
		n, err := strconv.Atoi(val)
		if err != nil {
			return fmt.Errorf("top: %w", err)
		}
		c.Top = n
	case "warn_cpu", "warn_memory", "warn_io":
		// These three existed before thresholds were wired up, and they never
		// did anything: no rule ever read them. They are refused by name
		// rather than silently ignored, because a key that looks like it
		// tightens a check and does not is worse than a key that is absent.
		return fmt.Errorf("%s 已移除（它从来没有生效过）。改用 [thresholds] 段，键名见 plumb config", key)
	default:
		return fmt.Errorf("unknown key %q", key)
	}
	return nil
}

// stripComment removes a trailing `# …` from a value.
//
// Save writes the built-in value beside every threshold it writes, and a
// parser that cannot read a comment makes plumb unable to read the file it
// wrote — which is not a corrupt-config situation, it is the normal one.
// Quotes are tracked so a `#` inside a value survives.
func stripComment(s string) string {
	var out strings.Builder
	inQuote := false
	for _, r := range s {
		switch {
		case r == '"':
			inQuote = !inQuote
			out.WriteRune(r)
		case r == '#' && !inQuote:
			return strings.TrimRight(out.String(), " ")
		default:
			out.WriteRune(r)
		}
	}
	return s
}

// unquote strips surrounding double quotes, leaving the raw value otherwise.
func unquote(s string) string {
	if len(s) >= 2 && s[0] == '"' && s[len(s)-1] == '"' {
		return s[1 : len(s)-1]
	}
	return s
}
