package main

import (
	"flag"
	"fmt"
	"os"

	"moonlight/internal/gamma"
	"moonlight/internal/install"
)

func installPaths() (install.Paths, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return install.Paths{}, err
	}
	return install.NewPaths(home, os.Getuid()), nil
}

// cmdInstall copies this binary onto PATH and (macOS) registers the background LaunchAgent.
func cmdInstall(args []string) error {
	fs := flag.NewFlagSet("install", flag.ContinueOnError)
	binDir := fs.String("bin-dir", "", "Install directory (default: /opt/homebrew/bin, /usr/local/bin or ~/.local/bin)")
	noAgent := fs.Bool("no-agent", false, "Only copy the binary; do not register the background LaunchAgent")
	if err := fs.Parse(args); err != nil {
		return err
	}
	src, err := os.Executable()
	if err != nil {
		return fmt.Errorf("locate running binary: %w", err)
	}
	p, err := installPaths()
	if err != nil {
		return err
	}
	return install.Install(src, p, install.Options{BinDir: *binDir, NoAgent: *noAgent, Out: os.Stdout})
}

// cmdUninstall removes the LaunchAgent and the installed copy; config is kept.
func cmdUninstall(args []string) error {
	fs := flag.NewFlagSet("uninstall", flag.ContinueOnError)
	binDir := fs.String("bin-dir", "", "Directory the binary was installed to (default: search the usual ones)")
	keepBinary := fs.Bool("keep-binary", false, "Remove only the LaunchAgent")
	if err := fs.Parse(args); err != nil {
		return err
	}
	p, err := installPaths()
	if err != nil {
		return err
	}
	// Release the held night-light tint now instead of letting its lease run out.
	_ = gamma.Reset()
	return install.Uninstall(p, *binDir, *keepBinary, os.Stdout)
}
