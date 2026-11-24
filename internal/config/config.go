package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Point defines a keyframe on the 24-hour brightness and color temperature curve.
type Point struct {
	Time       string  `json:"time"`
	Brightness int     `json:"brightness"`
	TempK      int     `json:"temp_k,omitempty"`
	RGB        *[3]int `json:"rgb,omitempty"`
}

// Config holds all runtime settings for monlight.
type Config struct {
	// MonitorMatch filters external monitors by case-insensitive substring (e.g. "M27UP").
	// Leave empty to target all detected DDC/CI monitors.
	MonitorMatch string `json:"monitor_match"`

	// PollIntervalSec controls how often the daemon evaluates the curve.
	PollIntervalSec int `json:"poll_interval_sec"`

	// CommandDelayMs adds a settle delay between consecutive DDC/CI commands.
	CommandDelayMs int `json:"command_delay_ms"`

	// Retries is the number of retry attempts when a DDC/CI call fails transiently.
	Retries int `json:"retries"`

	// MinDelta is the minimum absolute difference required before issuing a DDC/CI write.
	MinDelta int `json:"min_delta"`

	// EnableBrightness toggles hardware brightness (VCP 0x10) control.
	EnableBrightness bool `json:"enable_brightness"`

	// EnableNightLight toggles hardware RGB warmth (VCP 0x16/0x18/0x1A) control.
	EnableNightLight bool `json:"enable_night_light"`

	// ColorPresetUser optionally writes VCP 0x14 before updating RGB gains (0 disables).
	// On some monitors User Color mode is 0x0B (11). Run `monlight probe` first to verify.
	ColorPresetUser int `json:"color_preset_user"`

	// RGBNeutral is the monitor's neutral daytime RGB gain baseline (typically [100,100,100] or [50,50,50]).
	RGBNeutral [3]int `json:"rgb_neutral"`

	// DDCTool optionally specifies the path to m1ddc / ddcctl on macOS.
	// When empty, monlight searches PATH and standard Homebrew directories.
	DDCTool string `json:"ddc_tool"`

	// Schedule contains keyframes across a 24-hour cycle.
	Schedule []Point `json:"schedule"`
}

// State tracks runtime control flags shared between CLI commands and the daemon.
type State struct {
	Paused      bool       `json:"paused"`
	PausedUntil *time.Time `json:"paused_until,omitempty"`
	Reason      string     `json:"reason,omitempty"`
}

// IsPaused reports whether automatic schedule updates are currently paused.
func (s State) IsPaused(now time.Time) bool {
	if !s.Paused {
		return false
	}
	if s.PausedUntil != nil && !now.Before(*s.PausedUntil) {
		return false
	}
	return true
}

// DefaultConfig returns a sensible starter configuration tuned for Gigabyte M27UP.
func DefaultConfig() *Config {
	return &Config{
		MonitorMatch:     "M27UP",
		PollIntervalSec:  60,
		CommandDelayMs:   60,
		Retries:          2,
		MinDelta:         1,
		EnableBrightness: true,
		EnableNightLight: true,
		ColorPresetUser:  0,
		RGBNeutral:       [3]int{100, 100, 100},
		DDCTool:          "",
		Schedule: []Point{
			{Time: "07:00", Brightness: 40, TempK: 5200},
			{Time: "09:30", Brightness: 75, TempK: 6500},
			{Time: "17:30", Brightness: 65, TempK: 6500},
			{Time: "19:30", Brightness: 45, TempK: 4800},
			{Time: "21:30", Brightness: 25, TempK: 3800},
			{Time: "23:30", Brightness: 12, TempK: 3200},
		},
	}
}

// DefaultDir returns the per-user configuration directory for monlight.
func DefaultDir() (string, error) {
	base, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("resolve user config dir: %w", err)
	}
	return filepath.Join(base, "monlight"), nil
}

// DefaultConfigPath returns the default config.json path.
func DefaultConfigPath() (string, error) {
	dir, err := DefaultDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "config.json"), nil
}

// StatePathForConfig places state.json alongside the active config file.
func StatePathForConfig(configPath string) string {
	return filepath.Join(filepath.Dir(configPath), "state.json")
}

// Load reads and validates a Config from disk.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config %s: %w", path, err)
	}

	cfg := DefaultConfig()
	cfg.Schedule = nil

	if err := json.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("parse config %s: %w", path, err)
	}

	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("invalid config %s: %w", path, err)
	}
	return cfg, nil
}

// Save writes Config as formatted JSON, creating parent directories if needed.
func Save(path string, cfg *Config) error {
	if err := cfg.Validate(); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create config dir: %w", err)
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return os.WriteFile(path, data, 0o644)
}

// LoadState reads state.json; a missing file is treated as an unpaused default state.
func LoadState(path string) (State, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return State{}, nil
		}
		return State{}, fmt.Errorf("read state %s: %w", path, err)
	}
	var st State
	if err := json.Unmarshal(data, &st); err != nil {
		return State{}, fmt.Errorf("parse state %s: %w", path, err)
	}
	return st, nil
}

// SaveState writes state.json atomically via a temporary file.
func SaveState(path string, st State) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create state dir: %w", err)
	}
	data, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// Validate checks configuration values and applies safe defaults for unset fields.
func (c *Config) Validate() error {
	if c.PollIntervalSec <= 0 {
		c.PollIntervalSec = 60
	}
	if c.CommandDelayMs < 0 {
		c.CommandDelayMs = 60
	}
	if c.Retries < 0 {
		c.Retries = 0
	}
	if c.MinDelta < 0 {
		c.MinDelta = 0
	}
	for i, ch := range c.RGBNeutral {
		if ch <= 0 || ch > 255 {
			return fmt.Errorf("rgb_neutral[%d]=%d must be in 1..255", i, ch)
		}
	}
	if len(c.Schedule) == 0 {
		return errors.New("schedule must contain at least one point")
	}

	seen := make(map[int]bool, len(c.Schedule))
	for i, p := range c.Schedule {
		mins, err := ParseHHMM(p.Time)
		if err != nil {
			return fmt.Errorf("schedule[%d].time %q: %w", i, p.Time, err)
		}
		if seen[mins] {
			return fmt.Errorf("duplicate schedule time %q", p.Time)
		}
		seen[mins] = true

		if p.Brightness < 0 || p.Brightness > 100 {
			return fmt.Errorf("schedule[%d].brightness=%d must be in 0..100", i, p.Brightness)
		}
		if p.RGB != nil {
			for chIdx, ch := range p.RGB {
				if ch < 0 || ch > 255 {
					return fmt.Errorf("schedule[%d].rgb[%d]=%d must be in 0..255", i, chIdx, ch)
				}
			}
		} else if c.EnableNightLight && (p.TempK < 1500 || p.TempK > 10000) {
			return fmt.Errorf("schedule[%d].temp_k=%d must be in 1500..10000 (or set rgb)", i, p.TempK)
		}
	}
	return nil
}

// ParseHHMM converts "HH:MM" into minutes since midnight [0, 1439].
func ParseHHMM(s string) (int, error) {
	parts := strings.Split(strings.TrimSpace(s), ":")
	if len(parts) != 2 {
		return 0, errors.New("expected HH:MM format")
	}
	h, err := strconv.Atoi(parts[0])
	if err != nil || h < 0 || h > 23 {
		return 0, errors.New("hour must be 00..23")
	}
	m, err := strconv.Atoi(parts[1])
	if err != nil || m < 0 || m > 59 {
		return 0, errors.New("minute must be 00..59")
	}
	return h*60 + m, nil
}
