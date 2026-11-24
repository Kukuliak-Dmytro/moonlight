package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"monlight/internal/config"
	"monlight/internal/daemon"
	"monlight/internal/ddc"
	"monlight/internal/schedule"
)

const version = "0.1.0"

func main() {
	log.SetFlags(log.Ldate | log.Ltime)

	defaultCfgPath, err := config.DefaultConfigPath()
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	global := flag.NewFlagSet("monlight", flag.ContinueOnError)
	configPath := global.String("config", defaultCfgPath, "Path to config.json")
	global.Usage = printUsage

	if err := global.Parse(os.Args[1:]); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			os.Exit(0)
		}
		os.Exit(2)
	}

	args := global.Args()
	if len(args) == 0 {
		printUsage()
		os.Exit(1)
	}

	cmd := args[0]
	cmdArgs := args[1:]

	var runErr error
	switch cmd {
	case "init":
		runErr = cmdInit(*configPath, cmdArgs)
	case "probe":
		runErr = cmdProbe(*configPath)
	case "preview":
		runErr = cmdPreview(*configPath)
	case "status":
		runErr = cmdStatus(*configPath)
	case "apply":
		runErr = cmdApply(*configPath, cmdArgs)
	case "set":
		runErr = cmdSet(*configPath, cmdArgs)
	case "pause":
		runErr = cmdPause(*configPath, cmdArgs)
	case "resume":
		runErr = cmdResume(*configPath)
	case "daemon":
		runErr = cmdDaemon(*configPath)
	case "version":
		fmt.Printf("monlight %s\n", version)
	case "help", "-h", "--help":
		printUsage()
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n\n", cmd)
		printUsage()
		os.Exit(2)
	}

	if runErr != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", runErr)
		os.Exit(1)
	}
}

func printUsage() {
	fmt.Print(`monlight - Cross-platform DDC/CI brightness & night-light daemon (Windows & macOS)

Usage:
  monlight [-config path/to/config.json] <command> [args]

Commands:
  init [-force]           Create a default config.json tuned for Gigabyte M27UP
  probe                   List connected DDC/CI monitors and read current VCP values
  preview                 Show the interpolated 24-hour brightness/RGB curve
  status                  Show active backend, pause state, and current target
  apply                   Apply the current schedule target immediately
  set [flags]             Manually set brightness/temp/RGB (and optionally pause daemon)
  pause [duration]        Pause automatic updates (e.g. "monlight pause 2h", or forever)
  resume                  Resume automatic updates and apply schedule immediately
  daemon                  Run the background scheduling loop
  version                 Print version
`)
}

func cmdInit(configPath string, args []string) error {
	fs := flag.NewFlagSet("init", flag.ContinueOnError)
	force := fs.Bool("force", false, "Overwrite existing config file")
	if err := fs.Parse(args); err != nil {
		return err
	}

	if !*force {
		if _, err := os.Stat(configPath); err == nil {
			return fmt.Errorf("config already exists at %s (use -force to overwrite)", configPath)
		}
	}

	cfg := config.DefaultConfig()
	if err := config.Save(configPath, cfg); err != nil {
		return err
	}
	fmt.Printf("Created default config at:\n  %s\n", configPath)
	return nil
}

func loadConfigOrDefault(configPath string) (*config.Config, bool, error) {
	cfg, err := config.Load(configPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return config.DefaultConfig(), false, nil
		}
		return nil, false, err
	}
	return cfg, true, nil
}

func cmdProbe(configPath string) error {
	cfg, _, err := loadConfigOrDefault(configPath)
	if err != nil {
		return err
	}

	backend := daemon.NewBackendFromConfig(cfg)
	fmt.Printf("Backend: %s\n", backend.Name())

	monitors, err := backend.ListMonitors()
	if err != nil {
		return err
	}
	defer ddc.CloseAll(monitors)

	if len(monitors) == 0 {
		fmt.Println("No DDC/CI monitors detected.")
		return nil
	}

	codes := []byte{
		ddc.VCPBrightness,
		ddc.VCPColorPreset,
		ddc.VCPRedGain,
		ddc.VCPGreenGain,
		ddc.VCPBlueGain,
	}

	for _, m := range monitors {
		info := m.Info()
		fmt.Printf("\nMonitor [%s]: %s\n", info.ID, info.Name)
		if info.Device != "" {
			fmt.Printf("  Device: %s\n", info.Device)
		}
		for _, code := range codes {
			val, err := m.GetVCP(code)
			if err != nil {
				fmt.Printf("  %-22s: n/a (%v)\n", ddc.VCPName(code), err)
			} else {
				fmt.Printf("  %-22s: %d / %d\n", ddc.VCPName(code), val.Current, val.Max)
			}
		}
	}
	return nil
}

func cmdPreview(configPath string) error {
	cfg, loaded, err := loadConfigOrDefault(configPath)
	if err != nil {
		return err
	}
	if !loaded {
		fmt.Printf("(using built-in defaults; run `monlight init` to create %s)\n\n", configPath)
	}

	fmt.Println("Time   Brightness  TempK   RGB (R, G, B)")
	fmt.Println("-----  ----------  ------  ---------------")
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.Local)
	for h := 0; h < 24; h++ {
		t := base.Add(time.Duration(h) * time.Hour)
		target, err := schedule.Evaluate(cfg, t)
		if err != nil {
			return err
		}
		fmt.Printf("%02d:00  %9d%%  %5dK  [%3d, %3d, %3d]\n",
			h, target.Brightness, target.TempK, target.RGB[0], target.RGB[1], target.RGB[2])
	}
	return nil
}

func cmdStatus(configPath string) error {
	cfg, loaded, err := loadConfigOrDefault(configPath)
	if err != nil {
		return err
	}

	statePath := config.StatePathForConfig(configPath)
	st, err := config.LoadState(statePath)
	if err != nil {
		return err
	}

	now := time.Now()
	target, err := schedule.Evaluate(cfg, now)
	if err != nil {
		return err
	}

	backend := daemon.NewBackendFromConfig(cfg)
	fmt.Printf("Config:   %s (exists=%v)\n", configPath, loaded)
	fmt.Printf("Backend:  %s\n", backend.Name())
	fmt.Printf("Filter:   %q\n", cfg.MonitorMatch)
	if st.IsPaused(now) {
		if st.PausedUntil != nil {
			fmt.Printf("State:    PAUSED until %s (%s)\n", st.PausedUntil.Format("15:04"), st.Reason)
		} else {
			fmt.Printf("State:    PAUSED indefinitely (%s)\n", st.Reason)
		}
	} else {
		fmt.Printf("State:    ACTIVE\n")
	}
	fmt.Printf("Target:   brightness=%d%%, temp=%dK, rgb=%v (at %s)\n",
		target.Brightness, target.TempK, target.RGB, now.Format("15:04"))

	monitors, err := backend.ListMonitors()
	if err != nil {
		fmt.Printf("Monitors: error (%v)\n", err)
		return nil
	}
	matched := ddc.FilterMonitors(monitors, cfg.MonitorMatch)
	defer ddc.CloseAll(matched)

	fmt.Printf("Matched:  %d monitor(s)\n", len(matched))
	for _, m := range matched {
		info := m.Info()
		fmt.Printf("  - [%s] %s (%s)\n", info.ID, info.Name, info.Device)
	}
	return nil
}

func cmdApply(configPath string, args []string) error {
	cfg, _, err := loadConfigOrDefault(configPath)
	if err != nil {
		return err
	}

	target, err := schedule.Evaluate(cfg, time.Now())
	if err != nil {
		return err
	}

	backend := daemon.NewBackendFromConfig(cfg)
	logger := log.New(os.Stdout, "", log.Ltime)
	_, err = daemon.ApplyTarget(cfg, backend, target, true, nil, logger)
	return err
}

func cmdSet(configPath string, args []string) error {
	fs := flag.NewFlagSet("set", flag.ContinueOnError)
	brightness := fs.Int("brightness", -1, "Brightness 0..100")
	tempK := fs.Int("temp", 0, "Color temperature in Kelvin (e.g. 4200 or 6500)")
	rgbStr := fs.String("rgb", "", "Explicit RGB gains, e.g. 100,82,66")
	pauseDur := fs.String("pause", "", "Optionally pause daemon for a duration (e.g. 2h)")
	if err := fs.Parse(args); err != nil {
		return err
	}

	if *brightness < 0 && *tempK == 0 && *rgbStr == "" {
		return errors.New("specify at least one of -brightness, -temp, or -rgb")
	}

	cfg, _, err := loadConfigOrDefault(configPath)
	if err != nil {
		return err
	}

	overrideCfg := *cfg
	overrideCfg.EnableBrightness = *brightness >= 0
	overrideCfg.EnableNightLight = *tempK > 0 || *rgbStr != ""

	var target schedule.Target
	if overrideCfg.EnableBrightness {
		if *brightness > 100 {
			return errors.New("brightness must be in 0..100")
		}
		target.Brightness = *brightness
	}
	if *rgbStr != "" {
		rgb, err := parseRGB(*rgbStr)
		if err != nil {
			return err
		}
		target.RGB = rgb
	} else if *tempK > 0 {
		target.TempK = *tempK
		target.RGB = schedule.KelvinToRGB(*tempK, cfg.RGBNeutral)
	}

	backend := daemon.NewBackendFromConfig(&overrideCfg)
	logger := log.New(os.Stdout, "", log.Ltime)
	if _, err := daemon.ApplyTarget(&overrideCfg, backend, target, true, nil, logger); err != nil {
		return err
	}

	if *pauseDur != "" {
		return cmdPause(configPath, []string{*pauseDur})
	}
	return nil
}

func cmdPause(configPath string, args []string) error {
	statePath := config.StatePathForConfig(configPath)
	st := config.State{
		Paused: true,
		Reason: "manual pause",
	}

	if len(args) > 0 {
		d, err := time.ParseDuration(args[0])
		if err != nil || d <= 0 {
			return fmt.Errorf("invalid duration %q (examples: 30m, 2h)", args[0])
		}
		until := time.Now().Add(d)
		st.PausedUntil = &until
		if err := config.SaveState(statePath, st); err != nil {
			return err
		}
		fmt.Printf("Paused automatic updates for %s (until %s)\n", d, until.Format("15:04"))
		return nil
	}

	if err := config.SaveState(statePath, st); err != nil {
		return err
	}
	fmt.Println("Paused automatic updates indefinitely. Run `monlight resume` to continue.")
	return nil
}

func cmdResume(configPath string) error {
	statePath := config.StatePathForConfig(configPath)
	if err := config.SaveState(statePath, config.State{}); err != nil {
		return err
	}
	fmt.Println("Resumed automatic updates.")
	return cmdApply(configPath, nil)
}

func cmdDaemon(configPath string) error {
	// Ensure config exists on first daemon start so autostart works out of the box.
	if _, err := os.Stat(configPath); errors.Is(err, os.ErrNotExist) {
		if err := config.Save(configPath, config.DefaultConfig()); err != nil {
			return fmt.Errorf("auto-create default config: %w", err)
		}
		log.Printf("created default config at %s", configPath)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	return daemon.Run(ctx, configPath, log.Default())
}

func parseRGB(s string) ([3]int, error) {
	parts := strings.Split(s, ",")
	if len(parts) != 3 {
		return [3]int{}, errors.New("rgb must be R,G,B (e.g. 100,82,66)")
	}
	var out [3]int
	for i, p := range parts {
		v, err := strconv.Atoi(strings.TrimSpace(p))
		if err != nil || v < 0 || v > 255 {
			return [3]int{}, fmt.Errorf("invalid rgb channel %q (expected 0..255)", p)
		}
		out[i] = v
	}
	return out, nil
}
