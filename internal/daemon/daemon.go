package daemon

import (
	"context"
	"fmt"
	"log"
	"os"
	"time"

	"moonlight/internal/config"
	"moonlight/internal/ddc"
	"moonlight/internal/gamma"
	"moonlight/internal/schedule"
)

// AppliedState remembers the last values written to a specific monitor.
type AppliedState struct {
	Brightness    int
	RGB           [3]int
	HasBrightness bool
	HasRGB        bool
	PresetSent    bool
}

// ApplyOptions controls how ApplyTarget writes settings to monitors.
type ApplyOptions struct {
	Force      bool
	DryRun     bool
	BackupPath string
}

// NewBackendFromConfig constructs the platform DDC/CI backend using config timing options.
func NewBackendFromConfig(cfg *config.Config) ddc.Backend {
	return ddc.NewBackend(ddc.Options{
		CommandDelay: time.Duration(cfg.CommandDelayMs) * time.Millisecond,
		Retries:      cfg.Retries,
		DDCTool:      cfg.DDCTool,
	})
}

const (
	// defaultMinBrightness is the floor used when min_brightness is 0 and zero is not explicitly allowed.
	defaultMinBrightness = 5
	// minRGBFloor is the lowest per-channel RGB value ClampTarget will ever write.
	minRGBFloor = 10
	// maxRGB is the highest per-channel value config validation accepts.
	maxRGB = 255
	// presetSettleDelay lets the monitor finish switching color presets before RGB writes.
	presetSettleDelay = 400 * time.Millisecond
)

// applyGamma and resetGamma are the OS gamma writers; variables so tests can observe
// calls and, crucially, never touch the developer's real display or gamma holder.
var (
	applyGamma = gamma.Apply
	resetGamma = gamma.Reset
	keepGamma  = gamma.Keepalive
	// ensureGamma retires a stale gamma holder from an older build before the first apply.
	ensureGamma = gamma.EnsureCurrent
)

// ClampTarget enforces hard safety floors right before any DDC/CI write so a bad
// config, math bug, or typo can never black out the monitor.
func ClampTarget(cfg *config.Config, target schedule.Target) schedule.Target {
	out := target

	minB := cfg.MinBrightness
	if minB < 1 && !cfg.AllowZeroBrightness {
		minB = defaultMinBrightness
	}
	if out.Brightness < minB {
		out.Brightness = minB
	}
	if out.Brightness > 100 {
		out.Brightness = 100
	}

	minRGB := cfg.MinRGB
	if minRGB < minRGBFloor {
		minRGB = minRGBFloor
	}
	for i := 0; i < 3; i++ {
		if out.RGB[i] < minRGB {
			out.RGB[i] = minRGB
		}
		if out.RGB[i] > maxRGB {
			out.RGB[i] = maxRGB
		}
	}
	return out
}

// MonitorKey builds a stable identifier for a physical monitor. When the backend
// reports a hardware identifier (macOS display UUID, Windows device path) the list
// index is left out: it changes when displays are replugged or reordered, which
// would make a saved backup unfindable and trigger a fresh snapshot of values
// moonlight itself had already changed.
func MonitorKey(info ddc.MonitorInfo) string {
	if info.Device != "" {
		return info.Name + "|" + info.Device
	}
	return info.ID + "|" + info.Name
}

// lookupSnapshot finds a monitor's backup under its current key. Backups written before
// the key dropped the list index are matched by the Name and Device stored inside the
// snapshot, so they are found even if the display order has since changed. oldKey is
// non-empty when the entry was found under such a legacy key.
func lookupSnapshot(b config.Backup, info ddc.MonitorInfo) (snap config.MonitorSnapshot, oldKey string, ok bool) {
	if snap, ok := b.Monitors[MonitorKey(info)]; ok {
		return snap, "", true
	}
	if info.Device == "" {
		return config.MonitorSnapshot{}, "", false
	}
	for k, s := range b.Monitors {
		if s.Device == info.Device && s.Name == info.Name {
			return s, k, true
		}
	}
	return config.MonitorSnapshot{}, "", false
}

// CaptureSnapshot reads the current hardware brightness and RGB values from a monitor.
func CaptureSnapshot(mon ddc.Monitor) config.MonitorSnapshot {
	info := mon.Info()
	snap := config.MonitorSnapshot{
		Name:       info.Name,
		Device:     info.Device,
		CapturedAt: time.Now(),
	}
	if v, err := mon.GetVCP(ddc.VCPBrightness); err == nil {
		snap.HasBrightness = true
		snap.Brightness = int(v.Current)
	}
	r, errR := mon.GetVCP(ddc.VCPRedGain)
	g, errG := mon.GetVCP(ddc.VCPGreenGain)
	b, errB := mon.GetVCP(ddc.VCPBlueGain)
	if errR == nil && errG == nil && errB == nil {
		snap.HasRGB = true
		snap.RGB = [3]int{int(r.Current), int(g.Current), int(b.Current)}
	}
	return snap
}

func ensureBackupSnapshot(backupPath string, mon ddc.Monitor, logger *log.Logger) {
	if backupPath == "" {
		return
	}
	b, err := config.LoadBackup(backupPath)
	if err != nil {
		logger.Printf("warn: load backup failed: %v", err)
		return
	}
	info := mon.Info()
	key := MonitorKey(info)
	if old, oldKey, exists := lookupSnapshot(b, info); exists {
		if oldKey != "" {
			// Migrate in place; never re-capture, the hardware may already be tinted.
			delete(b.Monitors, oldKey)
			b.Monitors[key] = old
			if err := config.SaveBackup(backupPath, b); err != nil {
				logger.Printf("warn: migrate backup key failed: %v", err)
			}
		}
		return
	}
	snap := CaptureSnapshot(mon)
	if !snap.HasBrightness && !snap.HasRGB {
		return
	}
	b.Monitors[key] = snap
	if err := config.SaveBackup(backupPath, b); err != nil {
		logger.Printf("warn: save backup snapshot failed: %v", err)
		return
	}
	logger.Printf("saved initial hardware backup for [%s]: brightness=%d%% rgb=%v -> %s",
		snap.Name, snap.Brightness, snap.RGB, backupPath)
}

// gammaLease is how long a gamma write is held without a refresh: comfortably longer
// than the poll interval, so one slow or missed tick never drops the tint.
func gammaLease(cfg *config.Config) time.Duration {
	lease := 3 * time.Duration(cfg.PollIntervalSec) * time.Second
	if lease < gamma.DefaultLease {
		lease = gamma.DefaultLease
	}
	return lease
}

func useGamma(cfg *config.Config) bool {
	return cfg.EnableNightLight && (cfg.NightLightMode == "gamma" || cfg.NightLightMode == "both")
}

func useDDCRGB(cfg *config.Config) bool {
	return cfg.EnableNightLight && (cfg.NightLightMode == "ddc" || cfg.NightLightMode == "both")
}

// monitorPlan says which settings a monitor needs written this tick.
type monitorPlan struct {
	Brightness bool
	RGB        bool
}

func (p monitorPlan) any() bool { return p.Brightness || p.RGB }

// planMonitor decides what to write by diffing target against what was last applied.
func planMonitor(cfg *config.Config, target schedule.Target, prev AppliedState, havePrev, force bool) monitorPlan {
	var p monitorPlan
	p.Brightness = cfg.EnableBrightness &&
		(force || !havePrev || !prev.HasBrightness || absInt(target.Brightness-prev.Brightness) >= cfg.MinDelta)
	p.RGB = cfg.EnableNightLight && (force || !havePrev || !prev.HasRGB ||
		absInt(target.RGB[0]-prev.RGB[0]) >= cfg.MinDelta ||
		absInt(target.RGB[1]-prev.RGB[1]) >= cfg.MinDelta ||
		absInt(target.RGB[2]-prev.RGB[2]) >= cfg.MinDelta)
	return p
}

// gammaRatios converts an RGB target into OS gamma multipliers relative to the neutral baseline.
func gammaRatios(cfg *config.Config, target schedule.Target) gamma.Ratios {
	return gamma.Ratios{
		float64(target.RGB[0]) / float64(cfg.RGBNeutral[0]),
		float64(target.RGB[1]) / float64(cfg.RGBNeutral[1]),
		float64(target.RGB[2]) / float64(cfg.RGBNeutral[2]),
	}
}

// writeDDCPreset switches the monitor to its user color preset once, if configured.
func writeDDCPreset(cfg *config.Config, mon ddc.Monitor, prev AppliedState, force bool, logger *log.Logger, name string) bool {
	if cfg.ColorPresetUser <= 0 || (prev.PresetSent && !force) {
		return prev.PresetSent
	}
	cur, err := mon.GetVCP(ddc.VCPColorPreset)
	if err == nil && int(cur.Current) == cfg.ColorPresetUser {
		return true
	}
	if err := mon.SetVCP(ddc.VCPColorPreset, uint32(cfg.ColorPresetUser)); err != nil {
		logger.Printf("warn: [%s] set color preset=%d failed: %v", name, cfg.ColorPresetUser, err)
		return prev.PresetSent
	}
	time.Sleep(presetSettleDelay)
	return true
}

// writeDDCRGB writes the three RGB gain channels; it stops at the first failure.
func writeDDCRGB(mon ddc.Monitor, target schedule.Target, logger *log.Logger, name string) bool {
	channels := [3]struct {
		code byte
		val  int
	}{
		{ddc.VCPRedGain, target.RGB[0]},
		{ddc.VCPGreenGain, target.RGB[1]},
		{ddc.VCPBlueGain, target.RGB[2]},
	}
	for _, ch := range channels {
		if err := mon.SetVCP(ch.code, uint32(ch.val)); err != nil {
			logger.Printf("warn: [%s] set %s=%d failed: %v", name, ddc.VCPName(ch.code), ch.val, err)
			return false
		}
	}
	return true
}

// applyToMonitor performs the planned writes on one monitor and returns its new state.
// Brightness and RGB fail independently: a failed write only clears its own "has" flag,
// so the next tick retries just that part. gammaOK reports whether the one global gamma
// write for this tick succeeded.
func applyToMonitor(cfg *config.Config, mon ddc.Monitor, target schedule.Target, plan monitorPlan,
	prev AppliedState, force, gammaOK bool, logger *log.Logger) (AppliedState, bool) {

	name := mon.Info().Name
	next := prev
	ok := true

	if plan.Brightness {
		if err := mon.SetVCP(ddc.VCPBrightness, uint32(target.Brightness)); err != nil {
			logger.Printf("warn: [%s] set brightness=%d failed (KVM switched away or HDR active?): %v", name, target.Brightness, err)
			next.HasBrightness = false
			ok = false
		} else {
			next.Brightness = target.Brightness
			next.HasBrightness = true
		}
	}

	if plan.RGB {
		rgbOK := true
		if useGamma(cfg) {
			rgbOK = gammaOK
		}
		if rgbOK && useDDCRGB(cfg) {
			next.PresetSent = writeDDCPreset(cfg, mon, prev, force, logger, name)
			rgbOK = writeDDCRGB(mon, target, logger, name)
		}
		if rgbOK {
			next.RGB = target.RGB
			next.HasRGB = true
		} else {
			next.HasRGB = false
			ok = false
		}
	}
	return next, ok
}

// ApplyTarget writes brightness and/or night light to all matching monitors.
// Safety floors are enforced via ClampTarget before any write. OS gamma is global to
// the display session, so it is written at most once per call, before the per-monitor
// DDC/CI writes, and a DDC/CI failure never prevents it.
func ApplyTarget(
	cfg *config.Config,
	backend ddc.Backend,
	rawTarget schedule.Target,
	opts ApplyOptions,
	last map[string]AppliedState,
	logger *log.Logger,
) (int, error) {
	target := ClampTarget(cfg, rawTarget)

	allMonitors, err := backend.ListMonitors()
	if err != nil {
		return 0, err
	}
	monitors := ddc.FilterMonitors(allMonitors, cfg.MonitorMatch)
	defer ddc.CloseAll(monitors)

	plans := make([]monitorPlan, len(monitors))
	prevs := make([]AppliedState, len(monitors))
	keys := make([]string, len(monitors))
	seenKeys := make(map[string]bool, len(monitors))

	for i, mon := range monitors {
		keys[i] = MonitorKey(mon.Info())
		seenKeys[keys[i]] = true
		var havePrev bool
		if last != nil {
			prevs[i], havePrev = last[keys[i]]
		}
		plans[i] = planMonitor(cfg, target, prevs[i], havePrev, opts.Force)
	}

	if last != nil {
		for k := range last {
			if !seenKeys[k] {
				delete(last, k)
			}
		}
	}
	if len(monitors) == 0 {
		return 0, fmt.Errorf("no matching DDC/CI monitors found (filter=%q)", cfg.MonitorMatch)
	}

	// Gamma is re-asserted on every call, not only when the target changed: the OS can
	// drop it (sleep/wake, display reconfiguration) and, on macOS, it is a lease that
	// lapses unless refreshed.
	gammaOK := true
	if useGamma(cfg) && !opts.DryRun {
		if err := applyGamma(gammaRatios(cfg, target)); err != nil {
			logger.Printf("warn: set gamma ratios=%v failed: %v", gammaRatios(cfg, target), err)
			gammaOK = false
		}
	}

	updated, failed := 0, 0
	for i, mon := range monitors {
		if !plans[i].any() {
			continue
		}
		info := mon.Info()
		if opts.DryRun {
			logger.Printf("[dry-run] would apply [%s]: brightness=%d%% rgb=%v (~%dK, mode=%s)",
				info.Name, target.Brightness, target.RGB, target.TempK, cfg.NightLightMode)
			updated++
			continue
		}

		ensureBackupSnapshot(opts.BackupPath, mon, logger)

		next, ok := applyToMonitor(cfg, mon, target, plans[i], prevs[i], opts.Force, gammaOK, logger)
		if last != nil {
			last[keys[i]] = next
		}
		if ok {
			updated++
			logger.Printf("applied [%s]: brightness=%d%% rgb=%v (~%dK)", info.Name, target.Brightness, target.RGB, target.TempK)
		} else {
			failed++
		}
	}
	if failed > 0 && updated == 0 {
		// Every monitor that needed a write rejected it: report that instead of success,
		// so `moonlight apply` exits non-zero and the daemon's log says why nothing changed.
		return 0, fmt.Errorf("no monitor accepted the update (%d failed; see warnings above)", failed)
	}
	return updated, nil
}

// ResetAll forces safe brightness, neutral OS gamma, and neutral DDC RGB on ALL detected monitors,
// bypassing MonitorMatch so emergency recovery always works.
func ResetAll(cfg *config.Config, backend ddc.Backend, logger *log.Logger) error {
	if err := resetGamma(); err != nil {
		logger.Printf("warn: reset OS gamma failed: %v", err)
	}
	monitors, err := backend.ListMonitors()
	if err != nil {
		return err
	}
	defer ddc.CloseAll(monitors)

	if len(monitors) == 0 {
		return fmt.Errorf("no DDC/CI monitors detected")
	}

	safeB := cfg.EffectiveSafeBrightness()
	neutral := cfg.RGBNeutral

	var lastErr error
	for _, mon := range monitors {
		info := mon.Info()
		if err := mon.SetVCP(ddc.VCPBrightness, uint32(safeB)); err != nil {
			logger.Printf("warn: reset brightness on [%s] failed: %v", info.Name, err)
			lastErr = err
		}
		if err := mon.SetVCP(ddc.VCPRedGain, uint32(neutral[0])); err != nil {
			logger.Printf("warn: reset red on [%s] failed: %v", info.Name, err)
			lastErr = err
		}
		if err := mon.SetVCP(ddc.VCPGreenGain, uint32(neutral[1])); err != nil {
			logger.Printf("warn: reset green on [%s] failed: %v", info.Name, err)
			lastErr = err
		}
		if err := mon.SetVCP(ddc.VCPBlueGain, uint32(neutral[2])); err != nil {
			logger.Printf("warn: reset blue on [%s] failed: %v", info.Name, err)
			lastErr = err
		}
		logger.Printf("reset [%s]: brightness=%d%% rgb=%v", info.Name, safeB, neutral)
	}
	return lastErr
}

// RestoreBackup writes back the original hardware values saved in backup.json and resets OS gamma.
func RestoreBackup(cfg *config.Config, backend ddc.Backend, backupPath string, logger *log.Logger) error {
	_ = resetGamma()
	b, err := config.LoadBackup(backupPath)
	if err != nil {
		return err
	}
	if len(b.Monitors) == 0 {
		return fmt.Errorf("no saved snapshots in %s (use `moonlight reset` instead)", backupPath)
	}

	monitors, err := backend.ListMonitors()
	if err != nil {
		return err
	}
	defer ddc.CloseAll(monitors)

	restored := 0
	for _, mon := range monitors {
		info := mon.Info()
		snap, _, ok := lookupSnapshot(b, info)
		if !ok {
			continue
		}
		if snap.HasBrightness {
			if err := mon.SetVCP(ddc.VCPBrightness, uint32(snap.Brightness)); err != nil {
				logger.Printf("warn: restore brightness on [%s] failed: %v", info.Name, err)
			}
		}
		if snap.HasRGB {
			_ = mon.SetVCP(ddc.VCPRedGain, uint32(snap.RGB[0]))
			_ = mon.SetVCP(ddc.VCPGreenGain, uint32(snap.RGB[1]))
			_ = mon.SetVCP(ddc.VCPBlueGain, uint32(snap.RGB[2]))
		}
		logger.Printf("restored [%s] from backup: brightness=%d%% rgb=%v", info.Name, snap.Brightness, snap.RGB)
		restored++
	}

	if restored == 0 {
		return fmt.Errorf("none of the connected monitors matched backup.json (use `moonlight reset` instead)")
	}
	return nil
}

// Run starts the foreground daemon loop until ctx is cancelled.
func Run(ctx context.Context, configPath string, dryRun bool, logger *log.Logger) error {
	cfg, err := config.Load(configPath)
	if err != nil {
		return err
	}

	var configModTime time.Time
	if st, err := os.Stat(configPath); err == nil {
		configModTime = st.ModTime()
	}

	statePath := config.StatePathForConfig(configPath)
	backupPath := config.BackupPathForConfig(configPath)
	backend := NewBackendFromConfig(cfg)
	lastApplied := make(map[string]AppliedState)
	gamma.SetLease(gammaLease(cfg))
	if useGamma(cfg) && !dryRun {
		if err := ensureGamma(); err != nil {
			logger.Printf("warn: could not retire an old gamma holder: %v", err)
		}
	}

	logger.Printf("moonlight daemon started (backend=%s, config=%s, poll=%ds, filter=%q, dry_run=%v)",
		backend.Name(), configPath, cfg.PollIntervalSec, cfg.MonitorMatch, dryRun)

	wasPaused := false
	tick := func(force bool) {
		if st, err := os.Stat(configPath); err == nil && !st.ModTime().Equal(configModTime) {
			if reloaded, err := config.Load(configPath); err != nil {
				logger.Printf("warn: config reload failed, keeping previous safe config: %v", err)
			} else {
				cfg = reloaded
				backend = NewBackendFromConfig(cfg)
				gamma.SetLease(gammaLease(cfg))
				configModTime = st.ModTime()
				force = true
				logger.Printf("reloaded config from %s", configPath)
			}
		}

		now := time.Now()
		st, err := config.LoadState(statePath)
		if err != nil {
			logger.Printf("warn: read state failed: %v", err)
		}

		if st.IsPaused(now) {
			if !wasPaused {
				if st.PausedUntil != nil {
					logger.Printf("paused until %s", st.PausedUntil.Format(time.Kitchen))
				} else {
					logger.Printf("paused indefinitely (run `moonlight resume` to continue)")
				}
				wasPaused = true
			}
			// Pausing freezes brightness; it must freeze the tint too. Keep the held gamma
			// alive (extend-only) without changing it, so a pause, or a manual `set -pause`
			// override, does not let the tint lapse back to white.
			if useGamma(cfg) && !dryRun {
				_ = keepGamma()
			}
			return
		}

		if wasPaused || (st.Paused && !st.IsPaused(now)) {
			if st.Paused {
				_ = config.SaveState(statePath, config.State{})
			}
			wasPaused = false
			force = true
			logger.Printf("resuming automatic schedule")
		}

		target, err := schedule.Evaluate(cfg, now)
		if err != nil {
			logger.Printf("warn: schedule evaluation failed: %v", err)
			return
		}

		applyOpts := ApplyOptions{
			Force:      force,
			DryRun:     dryRun,
			BackupPath: backupPath,
		}
		if _, err := ApplyTarget(cfg, backend, target, applyOpts, lastApplied, logger); err != nil {
			logger.Printf("info: waiting for monitor (%v)", err)
		}
	}

	tick(true)

	interval := time.Duration(cfg.PollIntervalSec) * time.Second
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			logger.Printf("moonlight daemon stopping")
			return nil
		case <-ticker.C:
			tick(false)
			newInterval := time.Duration(cfg.PollIntervalSec) * time.Second
			if newInterval != interval && newInterval > 0 {
				interval = newInterval
				ticker.Reset(interval)
			}
		}
	}
}

func absInt(v int) int {
	if v < 0 {
		return -v
	}
	return v
}
