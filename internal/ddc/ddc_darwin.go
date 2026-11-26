//go:build darwin

package ddc

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const cmdTimeout = 5 * time.Second

type darwinBackend struct {
	opts     Options
	toolPath string
	toolKind string // "m1ddc" or "ddcctl"
}

// NewBackend creates the macOS DDC/CI backend using m1ddc (Apple Silicon) or ddcctl (Intel).
func NewBackend(opts Options) Backend {
	path, kind := resolveDarwinTool(opts.DDCTool)
	return &darwinBackend{
		opts:     opts,
		toolPath: path,
		toolKind: kind,
	}
}

func (b *darwinBackend) Name() string {
	if b.toolKind == "" {
		return "darwin-none"
	}
	return "darwin-" + b.toolKind
}

func resolveDarwinTool(preferred string) (string, string) {
	if preferred != "" {
		kind := "m1ddc"
		if strings.Contains(strings.ToLower(preferred), "ddcctl") {
			kind = "ddcctl"
		}
		return preferred, kind
	}

	candidates := []struct {
		name  string
		kind  string
		paths []string
	}{
		{
			name:  "m1ddc",
			kind:  "m1ddc",
			paths: []string{"/opt/homebrew/bin/m1ddc", "/usr/local/bin/m1ddc"},
		},
		{
			name:  "ddcctl",
			kind:  "ddcctl",
			paths: []string{"/opt/homebrew/bin/ddcctl", "/usr/local/bin/ddcctl"},
		},
	}

	for _, c := range candidates {
		if p, err := exec.LookPath(c.name); err == nil {
			return p, c.kind
		}
		for _, p := range c.paths {
			if st, err := os.Stat(p); err == nil && !st.IsDir() {
				return p, c.kind
			}
		}
	}
	return "", ""
}

// Matches lines like: "[1] M27UP (12345678-1234-...)" or "[1] (null) (UUID)"
var m1ddcDisplayRe = regexp.MustCompile(`^\s*\[(\d+)\]\s+(.+?)\s*(?:\(([^)]+)\))?\s*$`)

func (b *darwinBackend) ListMonitors() ([]Monitor, error) {
	if b.toolPath == "" {
		return nil, errors.New("no macOS DDC tool found: install m1ddc via `brew install m1ddc` (Apple Silicon) or `brew install ddcctl` (Intel)")
	}

	if b.toolKind == "ddcctl" {
		return []Monitor{
			&ddcctlMonitor{
				toolPath: b.toolPath,
				info:     MonitorInfo{ID: "1", Name: "External Display 1", Device: "ddcctl"},
				opts:     b.opts,
			},
		}, nil
	}

	out, err := runCmd(b.toolPath, "display", "list")
	if err != nil {
		return nil, fmt.Errorf("m1ddc display list failed: %w", err)
	}

	var monitors []Monitor
	for _, info := range parseM1ddcList(out) {
		monitors = append(monitors, &m1ddcMonitor{toolPath: b.toolPath, info: info, opts: b.opts})
	}
	return monitors, nil
}

// parseM1ddcList turns `m1ddc display list` output into MonitorInfo entries.
// Lines that do not match the expected format are skipped.
func parseM1ddcList(out string) []MonitorInfo {
	var infos []MonitorInfo
	for _, line := range strings.Split(out, "\n") {
		m := m1ddcDisplayRe.FindStringSubmatch(strings.TrimSpace(line))
		if m == nil {
			continue
		}
		id := m[1]
		name := strings.TrimSpace(m[2])
		if name == "(null)" || name == "" {
			name = "External Display " + id
		}
		infos = append(infos, MonitorInfo{ID: id, Name: name, Device: strings.TrimSpace(m[3])})
	}
	return infos
}

type m1ddcMonitor struct {
	toolPath string
	info     MonitorInfo
	opts     Options
}

func (m *m1ddcMonitor) Info() MonitorInfo { return m.info }

// selector addresses the display by UUID when known: the list index can change when
// displays are replugged or reordered, which would send writes to the wrong screen.
func (m *m1ddcMonitor) selector() string {
	if m.info.Device != "" {
		return m.info.Device
	}
	return m.info.ID
}
func (m *m1ddcMonitor) Close() error { return nil }

func m1ddcFeatureName(code byte) (string, error) {
	switch code {
	case VCPBrightness:
		return "luminance", nil
	case VCPRedGain:
		return "red", nil
	case VCPGreenGain:
		return "green", nil
	case VCPBlueGain:
		return "blue", nil
	default:
		return "", fmt.Errorf("VCP 0x%02X is not mapped for m1ddc", code)
	}
}

func (m *m1ddcMonitor) GetVCP(code byte) (VCPValue, error) {
	attr, err := m1ddcFeatureName(code)
	if err != nil {
		return VCPValue{}, err
	}

	var out VCPValue
	err = WithRetry(m.opts.Retries, m.opts.CommandDelay, func() error {
		curStr, err := runCmd(m.toolPath, "display", m.selector(), "get", attr)
		if err != nil {
			return err
		}
		cur, err := parseM1ddcValue(curStr)
		if err != nil {
			return fmt.Errorf("parse m1ddc get %s output %q: %w", attr, curStr, err)
		}
		if m.opts.CommandDelay > 0 {
			time.Sleep(m.opts.CommandDelay)
		}

		maxVal := uint32(100)
		if maxStr, err := runCmd(m.toolPath, "display", m.selector(), "max", attr); err == nil {
			if parsed, err := parseM1ddcValue(maxStr); err == nil && parsed > 0 {
				maxVal = uint32(parsed)
			}
		}
		if m.opts.CommandDelay > 0 {
			time.Sleep(m.opts.CommandDelay)
		}

		out = VCPValue{Current: uint32(cur), Max: maxVal}
		return nil
	})
	return out, err
}

func (m *m1ddcMonitor) SetVCP(code byte, value uint32) error {
	attr, err := m1ddcFeatureName(code)
	if err != nil {
		return err
	}

	return WithRetry(m.opts.Retries, m.opts.CommandDelay, func() error {
		_, err := runCmd(m.toolPath, "display", m.selector(), "set", attr, strconv.FormatUint(uint64(value), 10))
		if err != nil {
			return err
		}
		if m.opts.CommandDelay > 0 {
			time.Sleep(m.opts.CommandDelay)
		}
		return nil
	})
}

type ddcctlMonitor struct {
	toolPath string
	info     MonitorInfo
	opts     Options
}

func (m *ddcctlMonitor) Info() MonitorInfo { return m.info }
func (m *ddcctlMonitor) Close() error      { return nil }

func ddcctlFlag(code byte) (string, error) {
	switch code {
	case VCPBrightness:
		return "-b", nil
	case VCPRedGain:
		return "-rg", nil
	case VCPGreenGain:
		return "-gg", nil
	case VCPBlueGain:
		return "-bg", nil
	default:
		return "", fmt.Errorf("VCP 0x%02X is not mapped for ddcctl", code)
	}
}

func (m *ddcctlMonitor) GetVCP(code byte) (VCPValue, error) {
	flag, err := ddcctlFlag(code)
	if err != nil {
		return VCPValue{}, err
	}
	var out VCPValue
	err = WithRetry(m.opts.Retries, m.opts.CommandDelay, func() error {
		res, err := runCmd(m.toolPath, "-d", m.info.ID, flag, "?")
		if err != nil {
			return err
		}
		cur, max, err := parseDDCCtlValue(res)
		if err != nil {
			return err
		}
		out = VCPValue{Current: uint32(cur), Max: uint32(max)}
		return nil
	})
	return out, err
}

func (m *ddcctlMonitor) SetVCP(code byte, value uint32) error {
	flag, err := ddcctlFlag(code)
	if err != nil {
		return err
	}
	return WithRetry(m.opts.Retries, m.opts.CommandDelay, func() error {
		_, err := runCmd(m.toolPath, "-d", m.info.ID, flag, strconv.FormatUint(uint64(value), 10))
		if err != nil {
			return err
		}
		if m.opts.CommandDelay > 0 {
			time.Sleep(m.opts.CommandDelay)
		}
		return nil
	})
}

func runCmd(name string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), cmdTimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, name, args...)
	out, err := cmd.CombinedOutput()
	trimmed := strings.TrimSpace(string(out))
	if err != nil {
		return trimmed, fmt.Errorf("%s %s: %w (%s)", name, strings.Join(args, " "), err, trimmed)
	}
	return trimmed, nil
}

var (
	plainIntRe  = regexp.MustCompile(`^\d+$`)
	ddcctlCurRe = regexp.MustCompile(`current:\s*(\d+)(?:\s*,\s*max:\s*(\d+))?`)
)

// parseM1ddcValue parses `m1ddc ... get|max` output, which is a single integer.
// Anything else (an error message, extra text) is rejected rather than guessed at.
func parseM1ddcValue(s string) (int, error) {
	t := strings.TrimSpace(s)
	if !plainIntRe.MatchString(t) {
		return 0, fmt.Errorf("expected a single integer from m1ddc, got %q", s)
	}
	return strconv.Atoi(t)
}

// parseDDCCtlValue parses ddcctl query output of the form "... current: 50, max: 100".
// The current value is taken explicitly: the last integer would be the maximum.
func parseDDCCtlValue(s string) (cur, max int, err error) {
	m := ddcctlCurRe.FindStringSubmatch(s)
	if m == nil {
		return 0, 0, fmt.Errorf("no \"current: N\" value in ddcctl output %q", s)
	}
	cur, _ = strconv.Atoi(m[1])
	max = 100
	if m[2] != "" {
		max, _ = strconv.Atoi(m[2])
	}
	return cur, max, nil
}
