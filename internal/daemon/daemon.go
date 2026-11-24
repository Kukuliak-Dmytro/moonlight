package daemon

import (
	"context"
	"fmt"
	"log"
	"os"
	"time"

	"monlight/internal/config"
	"monlight/internal/ddc"
	"monlight/internal/schedule"
)

// AppliedState remembers the last values written to a specific monitor.
type AppliedState struct {
	Brightness    int
	RGB           [3]int
	HasBrightness bool
	HasRGB        bool
	PresetSent    bool
}

// NewBackendFromConfig constructs the platform DDC/CI backend using config timing options.
func NewBackendFromConfig(cfg *config.Config) ddc.Backend {
	return ddc.NewBackend(ddc.Options{
		CommandDelay: time.Duration(cfg.CommandDelayMs) * time.Millisecond,
		Retries:      cfg.Retries,
		DDCTool:      cfg.DDCTool,
	})
}

// ApplyTarget writes brightness and/or RGB gain to all matching monitors.
// When last is non-nil and force is false, unchanged values within cfg.MinDelta are skipped.
func ApplyTarget(
	cfg *config.Config,
	backend ddc.Backend,
	target schedule.Target,
	force bool,
	last map[string]AppliedState,
	logger *log.Logger,
) (int, error) {
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
		key := info.ID + "|" + info.Name + "|" + info.Device
		seenKeys[key] = true

		var prev AppliedState
		var havePrev bool
		if last != nil {
			prev, havePrev = last[key]
		}

		needBrightness := cfg.EnableBrightness && (force || !havePrev || !prev.HasBrightness || absInt(target.Brightness-prev.Brightness) >= cfg.MinDelta)
		needRGB := cfg.EnableNightLight && (force || !havePrev || !prev.HasRGB ||
			absInt(target.RGB[0]-prev.RGB[0]) >= cfg.MinDelta ||
			absInt(target.RGB[1]-prev.RGB[1]) >= cfg.MinDelta ||
			absInt(target.RGB[2]-prev.RGB[2]) >= cfg.MinDelta)

		if !needBrightness && !needRGB {
			continue
		}

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
			if cfg.ColorPresetUser > 0 && (!prev.PresetSent || force) {
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
			// Clear cached state so the monitor gets a fresh full apply once KVM returns.
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

	// Evict disconnected monitors so reconnecting via KVM triggers an immediate re-apply.
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

// Run starts the foreground daemon loop until ctx is cancelled.
func Run(ctx context.Context, configPath string, logger *log.Logger) error {
	cfg, err := config.Load(configPath)
	if err != nil {
		return err
	}

	var configModTime time.Time
	if st, err := os.Stat(configPath); err == nil {
		configModTime = st.ModTime()
	}

	statePath := config.StatePathForConfig(configPath)
	backend := NewBackendFromConfig(cfg)
	lastApplied := make(map[string]AppliedState)

	logger.Printf("monlight daemon started (backend=%s, config=%s, poll=%ds, filter=%q)",
		backend.Name(), configPath, cfg.PollIntervalSec, cfg.MonitorMatch)

	wasPaused := false
	tick := func(force bool) {
		// Hot-reload config if edited on disk.
		if st, err := os.Stat(configPath); err == nil && !st.ModTime().Equal(configModTime) {
			if reloaded, err := config.Load(configPath); err != nil {
				logger.Printf("warn: config reload failed, keeping previous config: %v", err)
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
					logger.Printf("paused indefinitely (run `monlight resume` to continue)")
				}
				wasPaused = true
			}
			return
		}

		if wasPaused || (st.Paused && !st.IsPaused(now)) {
			// Pause expired or resumed: clear expired pause state and force apply.
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

		if _, err := ApplyTarget(cfg, backend, target, force, lastApplied, logger); err != nil {
			logger.Printf("info: waiting for monitor (%v)", err)
		}
	}

	// Immediate initial apply.
	tick(true)

	interval := time.Duration(cfg.PollIntervalSec) * time.Second
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			logger.Printf("monlight daemon stopping")
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
