//go:build ignore

// Universal cross-platform build script for moonlight.
// Works identically on Windows (PowerShell/CMD), macOS, and Linux — no bash or make required.
//
// It only builds. To put the result on PATH and register the background agent, run
// the built binary's own installer: ./dist/moonlight install
//
// Usage:
//
//	go run ./scripts/build.go        # Build for the current OS/arch -> dist/moonlight(.exe)
//	go run ./scripts/build.go -all   # Cross-compile Windows + macOS (arm64 & amd64) into dist/
//	go run ./scripts/build.go -test  # Run unit tests before building
//	MOONLIGHT_VERSION=v1.2.3 go run ./scripts/build.go   # stamp a version into `moonlight version`
package main

import (
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
)

type target struct {
	goos   string
	goarch string
	output string
}

func main() {
	buildAll := flag.Bool("all", false, "Cross-compile binaries for Windows (amd64) and macOS (arm64, amd64)")
	runTests := flag.Bool("test", false, "Run unit tests before building")
	flag.Parse()

	if *runTests {
		fmt.Println("Running unit tests...")
		if err := runGo(nil, "test", "./..."); err != nil {
			fatal(err)
		}
	}

	distDir := "dist"
	if err := os.MkdirAll(distDir, 0o755); err != nil {
		fatal(err)
	}

	var targets []target
	if *buildAll {
		targets = []target{
			{goos: "windows", goarch: "amd64", output: filepath.Join(distDir, "moonlight-windows-amd64.exe")},
			{goos: "darwin", goarch: "arm64", output: filepath.Join(distDir, "moonlight-darwin-arm64")},
			{goos: "darwin", goarch: "amd64", output: filepath.Join(distDir, "moonlight-darwin-amd64")},
		}
	} else {
		name := "moonlight"
		if runtime.GOOS == "windows" {
			name += ".exe"
		}
		targets = []target{
			{goos: runtime.GOOS, goarch: runtime.GOARCH, output: filepath.Join(distDir, name)},
		}
	}

	// Stamp the release version into the binary when MOONLIGHT_VERSION is set (CI does, from the tag).
	ldflags := "-s -w"
	if v := os.Getenv("MOONLIGHT_VERSION"); v != "" {
		ldflags += " -X main.version=" + v
	}

	// Local builds remember their checkout so `moonlight update` can pull and rebuild it.
	if os.Getenv("MOONLIGHT_VERSION") == "" {
		if wd, err := os.Getwd(); err == nil {
			ldflags += " -X main.repoDir=" + wd
		}
	}

	for _, t := range targets {
		fmt.Printf("Building %s/%s -> %s\n", t.goos, t.goarch, t.output)
		env := append(os.Environ(),
			"CGO_ENABLED=0",
			"GOOS="+t.goos,
			"GOARCH="+t.goarch,
		)
		if err := runGo(env, "build", "-trimpath", "-ldflags="+ldflags, "-o", t.output, "./cmd/moonlight"); err != nil {
			fatal(err)
		}
		if fi, err := os.Stat(t.output); err == nil {
			fmt.Printf("  OK (%.2f MB)\n", float64(fi.Size())/(1024*1024))
		}
	}
}

func runGo(env []string, args ...string) error {
	cmd := exec.Command("go", args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if env != nil {
		cmd.Env = env
	}
	return cmd.Run()
}

func fatal(err error) {
	fmt.Fprintf(os.Stderr, "build error: %v\n", err)
	os.Exit(1)
}
