package adapter

import (
	"bufio"
	"fmt"
	"io"
	"regexp"
	"strconv"
	"strings"

	"moonlight/internal/config"
	"moonlight/internal/ddc"
)

// Profile holds vendor/monitor-specific tuning applied automatically by the adapter.
type Profile struct {
	Name           string
	CommandDelayMs int
	NightLightMode string
	RGBNeutral     [3]int
}

// DiscoveredMonitor represents a physical display found by the DDC/CI backend
// together with its probed capabilities and matched adapter profile.
type DiscoveredMonitor struct {
	Index             int
	Info              ddc.MonitorInfo
	MatchToken        string
	DDCSupported      bool
	BrightnessCurrent uint32
	BrightnessMax     uint32
	RGBSupported      bool
	Profile           Profile
}

// Discover scans all connected monitors via the platform DDC/CI backend,
// probes their capabilities, and assigns the best-fit adapter profile to each.
func Discover(backend ddc.Backend) ([]DiscoveredMonitor, error) {
	monitors, err := backend.ListMonitors()
	if err != nil {
		return nil, err
	}
	defer ddc.CloseAll(monitors)

	out := make([]DiscoveredMonitor, 0, len(monitors))
	for i, mon := range monitors {
		info := mon.Info()
		dm := DiscoveredMonitor{
			Index:      i + 1,
			Info:       info,
			MatchToken: ExtractMatchToken(info),
		}

		if b, err := mon.GetVCP(ddc.VCPBrightness); err == nil {
			dm.DDCSupported = true
			dm.BrightnessCurrent = b.Current
			dm.BrightnessMax = b.Max
		}

		var rgbMax [3]int
		r, errR := mon.GetVCP(ddc.VCPRedGain)
		g, errG := mon.GetVCP(ddc.VCPGreenGain)
		b, errB := mon.GetVCP(ddc.VCPBlueGain)
		if errR == nil && errG == nil && errB == nil {
			dm.RGBSupported = true
			rgbMax = [3]int{clampRGBMax(r.Max), clampRGBMax(g.Max), clampRGBMax(b.Max)}
		}

		dm.Profile = DetectProfile(info, rgbMax)
		out = append(out, dm)
	}
	return out, nil
}

// pnpIDRe extracts the 7-character Windows EDID hardware ID from MONITOR\GBT273E\{...}.
var pnpIDRe = regexp.MustCompile(`(?i)MONITOR\\([A-Z0-9]{3,16})\\`)

// ExtractMatchToken picks a stable, concise filter string for a monitor
// (Windows PnP hardware ID like "GBT273E" or macOS display name like "M27UP").
func ExtractMatchToken(info ddc.MonitorInfo) string {
	if m := pnpIDRe.FindStringSubmatch(info.Device); len(m) == 2 {
		return strings.ToUpper(m[1])
	}
	name := strings.TrimSpace(info.Name)
	lower := strings.ToLower(name)
	if name != "" && lower != "generic pnp monitor" && !strings.HasPrefix(lower, "external display") {
		return name
	}
	if dev := strings.TrimSpace(info.Device); dev != "" {
		return dev
	}
	return info.ID
}

// DetectProfile matches a monitor to a known vendor adapter profile or falls back to Generic.
func DetectProfile(info ddc.MonitorInfo, rgbMax [3]int) Profile {
	haystack := strings.ToUpper(info.Name + " " + info.Device)
	neutral := [3]int{100, 100, 100}
	if rgbMax[0] >= 50 && rgbMax[1] >= 50 && rgbMax[2] >= 50 {
		neutral = rgbMax
	}

	switch {
	case containsAny(haystack, "GBT", "GIG", "GIGABYTE", "M27", "M32", "AORUS"):
		return Profile{
			Name:           "Gigabyte / AORUS",
			CommandDelayMs: 60,
			NightLightMode: "gamma",
			RGBNeutral:     [3]int{100, 100, 100},
		}
	case containsAny(haystack, "DEL", "DELL", "ALIENWARE", "AW27", "AW32", "AW34"):
		return Profile{
			Name:           "Dell / Alienware",
			CommandDelayMs: 50,
			NightLightMode: "gamma",
			RGBNeutral:     neutral,
		}
	case containsAny(haystack, "GSM", "LG ", "ULTRAGEAR", "ULTRAFINE"):
		return Profile{
			Name:           "LG Display",
			CommandDelayMs: 50,
			NightLightMode: "gamma",
			RGBNeutral:     neutral,
		}
	case containsAny(haystack, "AUS", "ACI", "ASUS", "PROART", "ROG"):
		return Profile{
			Name:           "ASUS",
			CommandDelayMs: 60,
			NightLightMode: "gamma",
			RGBNeutral:     neutral,
		}
	case containsAny(haystack, "SAM", "SAMSUNG", "ODYSSEY"):
		return Profile{
			Name:           "Samsung",
			CommandDelayMs: 70,
			NightLightMode: "gamma",
			RGBNeutral:     neutral,
		}
	case containsAny(haystack, "BNQ", "BENQ"):
		return Profile{
			Name:           "BenQ",
			CommandDelayMs: 60,
			NightLightMode: "gamma",
			RGBNeutral:     neutral,
		}
	default:
		return Profile{
			Name:           "Generic DDC/CI",
			CommandDelayMs: 60,
			NightLightMode: "gamma",
			RGBNeutral:     neutral,
		}
	}
}

// SelectInteractive prints all discovered monitors and lets the user choose one,
// several (comma-separated, e.g. "1,2"), or all ("0" / "all").
// Returning an empty slice with nil error means "all monitors".
func SelectInteractive(monitors []DiscoveredMonitor, in io.Reader, out io.Writer) ([]DiscoveredMonitor, error) {
	if len(monitors) == 0 {
		return nil, fmt.Errorf("no DDC/CI monitors detected")
	}

	fmt.Fprintln(out, "Detected monitors:")
	for _, m := range monitors {
		status := "DDC n/a"
		if m.DDCSupported {
			status = fmt.Sprintf("Brightness %d/%d", m.BrightnessCurrent, m.BrightnessMax)
		}
		fmt.Fprintf(out, "  [%d] %s | ID: %s | %s | Adapter: %s\n",
			m.Index, m.Info.Name, m.MatchToken, status, m.Profile.Name)
	}
	fmt.Fprintln(out, "  [0] All DDC/CI monitors (no filter)")
	fmt.Fprintf(out, "\nSelect monitor number (comma-separated for multiple, Enter = [1]): ")

	reader := bufio.NewReader(in)
	line, err := reader.ReadString('\n')
	if err != nil && err != io.EOF {
		return nil, fmt.Errorf("read selection: %w", err)
	}
	choice := strings.TrimSpace(line)
	if choice == "" {
		return []DiscoveredMonitor{monitors[0]}, nil
	}
	if strings.EqualFold(choice, "all") || choice == "0" {
		return nil, nil
	}

	parts := strings.Split(choice, ",")
	seen := make(map[int]bool, len(parts))
	var selected []DiscoveredMonitor
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		idx, err := strconv.Atoi(p)
		if err != nil || idx < 1 || idx > len(monitors) {
			return nil, fmt.Errorf("invalid selection %q (expected 0..%d)", p, len(monitors))
		}
		if !seen[idx] {
			seen[idx] = true
			selected = append(selected, monitors[idx-1])
		}
	}
	if len(selected) == 0 {
		return []DiscoveredMonitor{monitors[0]}, nil
	}
	return selected, nil
}

// ApplySelection updates cfg with the selected monitor filter and adapter profile.
// If selected is empty, MonitorMatch is cleared so all detected DDC monitors are targeted.
func ApplySelection(cfg *config.Config, selected []DiscoveredMonitor) {
	if len(selected) == 0 {
		cfg.MonitorMatch = ""
		cfg.Adapter = "All DDC/CI Monitors"
		return
	}

	tokens := make([]string, 0, len(selected))
	seen := make(map[string]bool, len(selected))
	for _, m := range selected {
		tok := strings.TrimSpace(m.MatchToken)
		if tok != "" && !seen[strings.ToLower(tok)] {
			seen[strings.ToLower(tok)] = true
			tokens = append(tokens, tok)
		}
	}
	cfg.MonitorMatch = strings.Join(tokens, ",")

	// Apply tuning from the primary selected monitor's adapter profile.
	primary := selected[0].Profile
	cfg.Adapter = primary.Name
	if primary.CommandDelayMs > 0 {
		cfg.CommandDelayMs = primary.CommandDelayMs
	}
	if primary.NightLightMode != "" {
		cfg.NightLightMode = primary.NightLightMode
	}
	if primary.RGBNeutral[0] >= cfg.MinRGB && primary.RGBNeutral[1] >= cfg.MinRGB && primary.RGBNeutral[2] >= cfg.MinRGB {
		cfg.RGBNeutral = primary.RGBNeutral
	}
}

func containsAny(s string, patterns ...string) bool {
	for _, p := range patterns {
		if strings.Contains(s, p) {
			return true
		}
	}
	return false
}

func clampRGBMax(v uint32) int {
	if v == 0 || v > 255 {
		return 100
	}
	return int(v)
}
