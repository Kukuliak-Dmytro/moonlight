package daemon

import (
	"context"
	"fmt"
	"log"
	"os"
	"time"

	"moonlight/internal/config"
	"moonlight/internal/ddc"
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

// ClampTarget enforces hard safety floors right before any DDC/CI write so a bad
// config, math bug, or typo can never black out the monitor.
func ClampTarget(cfg *config.Config, target schedule.Target) schedule.Target {
	out := target

	minB := cfg.MinBrightness
	if minB < 1 && !cfg.AllowZeroBrightness {
		minB = 5
	}
	if out.Brightness < minB {
		out.Brightness = minB
	}
	if out.Brightness > 100 {
		out.Brightness = 100
	}

	minRGB := cfg.MinRGB
	if minRGB < 10 {
		minRGB = 10
	}
	for i := 0; i < 3; i++ {
		if out.RGB[i] < minRGB {
			out.RGB[i] = minRGB
		}
		if out.RGB[i] > 255 {
			out.RGB[i] = 255
		}
	}
	return out
}

// MonitorKey builds a stable identifier for a physical monitor.
func MonitorKey(info ddc.MonitorInfo) string {
	return info.ID + "|" + info.Name + "|" + info.Device
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
	key := MonitorKey(mon.Info())
	if _, exists := b.Monitors[key]; exists {
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

// ApplyTarget writes brightness and/or RGB gain to all matching monitors.
// Safety floors are enforced via ClampTarget before any DDC/CI write.
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

	seenKeys := make(map[string]bool, len(monitors))
	updatedCount := 0

	for _, mon := range monitors {
		info := mon.Info()
		key := MonitorKey(info)
		seenKeys[key] = true

		var prev AppliedState
		var havePrev bool
		if last != nil {
			prev, havePrev = last[key]
		}

		needBrightness := cfg.EnableBrightness && (opts.Force || !havePrev || !prev.HasBrightness || absInt(target.Brightness-prev.Brightness) >= cfg.MinDelta)
		needRGB := cfg.EnableNightLight && (opts.Force || !havePrev || !prev.HasRGB ||
			absInt(target.RGB[0]-prev.RGB[0]) >= cfg.MinDelta ||
			absInt(target.RGB[1]-prev.RGB[1]) >= cfg.MinDelta ||
			absInt(target.RGB[2]-prev.RGB[2]) >= cfg.MinDelta)

		if !needBrightness && !needRGB {
			continue
		}

		if opts.DryRun {
			logger.Printf("[dry-run] would apply [%s]: brightness=%d%% rgb=%v (~%dK)",
				info.Name, target.Brightness, target.RGB, target.TempK)
			updatedCount++
			continue
		}

		ensureBackupSnapshot(opts.BackupPath, mon, logger)

		nextState := prev
		monFailed := false

		if needBrightness {
			if err := mon.SetVCP(ddc.VCPBrightness, uint32(target.Brightness)); err != nil {
				logger.Printf("warn: [%s] set brightness=%d failed (KVM switched away or HDR active?): %v", info.Name, target.Brightness, err)
				monFailed = true
			} else {
				nextState.Brightness = target.Brightness
				nextState.HasBrightness = true
			}
		}

		if needRGB && !monFailed {
			if cfg.ColorPresetUser > 0 && (!prev.PresetSent || opts.Force) {
				if err := mon.SetVCP(ddc.VCPColorPreset, uint32(cfg.ColorPresetUser)); err != nil {
					logger.Printf("warn: [%s] set color preset=%d failed: %v", info.Name, cfg.ColorPresetUser, err)
				} else {
					nextState.PresetSent = true
				}
			}

			channels := [3]struct {
				code byte
				val  int
			}{
				{ddc.VCPRedGain, target.RGB[0]},
				{ddc.VCPGreenGain, target.RGB[1]},
				{ddc.VCPBlueGain, target.RGB[2]},
			}

			rgbOK := true
			for _, ch := range channels {
				if err := mon.SetVCP(ch.code, uint32(ch.val)); err != nil {
					logger.Printf("warn: [%s] set %s=%d failed: %v", info.Name, ddc.VCPName(ch.code), ch.val, err)
					rgbOK = false
					monFailed = true
					break
				}
			}
			if rgbOK {
				nextState.RGB = target.RGB
				nextState.HasRGB = true
			}
		}

		if monFailed {
			if last != nil {
				delete(last, key)
			}
			continue
		}

		if last != nil {
			last[key] = nextState
		}
		updatedCount++
		logger.Printf("applied [%s]: brightness=%d%% rgb=%v (~%dK)", info.Name, target.Brightness, target.RGB, target.TempK)
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

	return updatedCount, nil
}

// ResetAll forces safe brightness and neutral RGB on ALL detected DDC/CI monitors,
// bypassing MonitorMatch so emergency recovery always works.
func ResetAll(cfg *config.Config, backend ddc.Backend, logger *log.Logger) error {
	monitors, err := backend.ListMonitors()
	if err != nil {
		return err
	}
	defer ddc.CloseAll(monitors)

	if len(monitors) == 0 {
		return fmt.Errorf("no DDC/CI monitors detected")
	}

	safeB := cfg.SafeBrightness
	if safeB < 30 || safeB > 100 {
		safeB = 70
	}
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

// RestoreBackup writes back the original hardware values saved in backup.json.
func RestoreBackup(cfg *config.Config, backend ddc.Backend, backupPath string, logger *log.Logger) error {
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
		snap, ok := b.Monitors[MonitorKey(info)]
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
