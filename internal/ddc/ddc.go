package ddc

import (
	"fmt"
	"strings"
	"time"
)

// Standard VESA MCCS VCP feature codes supported by Gigabyte M-series monitors.
const (
	VCPBrightness  byte = 0x10
	VCPColorPreset byte = 0x14
	VCPRedGain     byte = 0x16
	VCPGreenGain   byte = 0x18
	VCPBlueGain    byte = 0x1A
)

// VCPValue holds a VCP feature's current and maximum hardware values.
type VCPValue struct {
	Current uint32
	Max     uint32
}

// MonitorInfo describes a physical display discovered by the DDC/CI backend.
type MonitorInfo struct {
	ID     string
	Name   string
	Device string
}

// Monitor represents an open handle to a DDC/CI-capable physical display.
type Monitor interface {
	Info() MonitorInfo
	GetVCP(code byte) (VCPValue, error)
	SetVCP(code byte, value uint32) error
	Close() error
}

// Options configures DDC/CI transport timing and tool paths.
type Options struct {
	CommandDelay time.Duration
	Retries      int
	DDCTool      string
}

// Backend discovers DDC/CI-capable monitors on the current operating system.
type Backend interface {
	Name() string
	ListMonitors() ([]Monitor, error)
}

// FilterMonitors keeps only monitors matching any comma-separated case-insensitive
// substring (e.g. "M27UP,GBT273E") against Name, Device, or ID.
// Unmatched monitors are closed immediately.
func FilterMonitors(monitors []Monitor, match string) []Monitor {
	raw := strings.TrimSpace(match)
	if raw == "" {
		return monitors
	}

	var needles []string
	for _, part := range strings.Split(raw, ",") {
		if p := strings.ToLower(strings.TrimSpace(part)); p != "" {
			needles = append(needles, p)
		}
	}
	if len(needles) == 0 {
		return monitors
	}

	var matched []Monitor
	for _, m := range monitors {
		info := m.Info()
		haystack := strings.ToLower(info.Name + " " + info.Device + " " + info.ID)
		found := false
		for _, n := range needles {
			if strings.Contains(haystack, n) {
				found = true
				break
			}
		}
		if found {
			matched = append(matched, m)
		} else {
			_ = m.Close()
		}
	}
	return matched
}

// CloseAll closes every monitor in the slice.
func CloseAll(monitors []Monitor) {
	for _, m := range monitors {
		if m != nil {
			_ = m.Close()
		}
	}
}

// WithRetry executes fn up to retries+1 times with a linear backoff delay.
func WithRetry(retries int, delay time.Duration, fn func() error) error {
	var err error
	attempts := retries + 1
	if attempts < 1 {
		attempts = 1
	}
	for i := 0; i < attempts; i++ {
		if i > 0 && delay > 0 {
			time.Sleep(delay * time.Duration(i))
		}
		err = fn()
		if err == nil {
			return nil
		}
	}
	return err
}

// VCPName returns a human-readable label for common VCP codes.
func VCPName(code byte) string {
	switch code {
	case VCPBrightness:
		return "Brightness (0x10)"
	case VCPColorPreset:
		return "Color Preset (0x14)"
	case VCPRedGain:
		return "Red Gain (0x16)"
	case VCPGreenGain:
		return "Green Gain (0x18)"
	case VCPBlueGain:
		return "Blue Gain (0x1A)"
	default:
		return fmt.Sprintf("VCP 0x%02X", code)
	}
}
