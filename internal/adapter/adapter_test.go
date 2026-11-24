package adapter

import (
	"bytes"
	"strings"
	"testing"

	"moonlight/internal/config"
	"moonlight/internal/ddc"
)

func TestExtractMatchToken(t *testing.T) {
	win := ddc.MonitorInfo{
		ID:     "1",
		Name:   "Generic PnP Monitor",
		Device: `\\.\DISPLAY1 (MONITOR\GBT273E\{4d36e96e-e325-11ce-bfc1-08002be10318}\0005)`,
	}
	if got := ExtractMatchToken(win); got != "GBT273E" {
		t.Fatalf("expected GBT273E, got %q", got)
	}

	mac := ddc.MonitorInfo{
		ID:     "1",
		Name:   "M27UP",
		Device: "12345678-ABCD-1234-ABCD-1234567890AB",
	}
	if got := ExtractMatchToken(mac); got != "M27UP" {
		t.Fatalf("expected M27UP, got %q", got)
	}
}

func TestDetectProfile(t *testing.T) {
	gbt := DetectProfile(ddc.MonitorInfo{Name: "Generic PnP Monitor", Device: `MONITOR\GBT273E\0001`}, [3]int{100, 100, 100})
	if gbt.Name != "Gigabyte / AORUS" {
		t.Fatalf("expected Gigabyte / AORUS, got %q", gbt.Name)
	}

	dell := DetectProfile(ddc.MonitorInfo{Name: "DELL U2723QE", Device: `MONITOR\DEL423E\0001`}, [3]int{100, 100, 100})
	if dell.Name != "Dell / Alienware" {
		t.Fatalf("expected Dell / Alienware, got %q", dell.Name)
	}

	generic := DetectProfile(ddc.MonitorInfo{Name: "Unknown Monitor", Device: "DISPLAY1"}, [3]int{100, 100, 100})
	if generic.Name != "Generic DDC/CI" {
		t.Fatalf("expected Generic DDC/CI, got %q", generic.Name)
	}
}

func TestSelectInteractiveAndApplySelection(t *testing.T) {
	monitors := []DiscoveredMonitor{
		{
			Index:        1,
			Info:         ddc.MonitorInfo{ID: "1", Name: "Generic PnP Monitor", Device: `MONITOR\GBT273E\0001`},
			MatchToken:   "GBT273E",
			DDCSupported: true,
			Profile:      Profile{Name: "Gigabyte / AORUS", CommandDelayMs: 60, NightLightMode: "gamma", RGBNeutral: [3]int{100, 100, 100}},
		},
		{
			Index:        2,
			Info:         ddc.MonitorInfo{ID: "2", Name: "DELL U2723QE", Device: `MONITOR\DEL423E\0002`},
			MatchToken:   "DEL423E",
			DDCSupported: true,
			Profile:      Profile{Name: "Dell / Alienware", CommandDelayMs: 50, NightLightMode: "gamma", RGBNeutral: [3]int{100, 100, 100}},
		},
	}

	// Pick monitor 2
	var out bytes.Buffer
	sel, err := SelectInteractive(monitors, strings.NewReader("2\n"), &out)
	if err != nil {
		t.Fatalf("SelectInteractive(2): %v", err)
	}
	if len(sel) != 1 || sel[0].MatchToken != "DEL423E" {
		t.Fatalf("expected DEL423E, got %+v", sel)
	}

	cfg := config.DefaultConfig()
	ApplySelection(cfg, sel)
	if cfg.MonitorMatch != "DEL423E" || cfg.Adapter != "Dell / Alienware" {
		t.Fatalf("unexpected applied config: match=%q adapter=%q", cfg.MonitorMatch, cfg.Adapter)
	}

	// Pick 0 -> all monitors
	selAll, err := SelectInteractive(monitors, strings.NewReader("0\n"), &out)
	if err != nil {
		t.Fatalf("SelectInteractive(0): %v", err)
	}
	ApplySelection(cfg, selAll)
	if cfg.MonitorMatch != "" {
		t.Fatalf("expected empty MonitorMatch for all monitors, got %q", cfg.MonitorMatch)
	}
}
