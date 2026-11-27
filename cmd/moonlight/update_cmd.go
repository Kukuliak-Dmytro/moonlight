package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
)

// repoDir is the source checkout this binary was built from. scripts/build.go stamps it
// (-ldflags "-X main.repoDir=...") on local builds so `moonlight update` knows where to pull.
var repoDir string

// runStep runs one external command in dir with its output shown; a variable so tests
// can record the steps instead of running them.
var runStep = func(dir, name string, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = os.Stdin
	return cmd.Run()
}

// updateSteps lists, in order, what `moonlight update` runs inside the repo. The last step
// installs the fresh build and relaunches the daemon: `install` on macOS, the Task Scheduler
// script on Windows (which stops the old daemon first).
func updateSteps(goos string, pull bool) [][]string {
	var steps [][]string
	if pull {
		steps = append(steps, []string{"git", "pull", "--ff-only"})
	}
	steps = append(steps, []string{"go", "run", "./scripts/build.go"})
	if goos == "windows" {
		steps = append(steps, []string{"powershell", "-NoProfile", "-ExecutionPolicy", "Bypass",
			"-File", filepath.Join("scripts", "install-windows-task.ps1")})
	} else {
		steps = append(steps, []string{filepath.Join("dist", "moonlight"), "install"})
	}
	return steps
}

// cmdUpdate pulls the source repo, rebuilds, and reinstalls + relaunches the daemon.
func cmdUpdate(args []string) error {
	fs := flag.NewFlagSet("update", flag.ContinueOnError)
	repo := fs.String("repo", "", "Source checkout to update (default: $MOONLIGHT_REPO, else where this binary was built)")
	noPull := fs.Bool("no-pull", false, "Skip git pull; just rebuild and relaunch")
	if err := fs.Parse(args); err != nil {
		return err
	}

	dir := *repo
	if dir == "" {
		dir = os.Getenv("MOONLIGHT_REPO")
	}
	if dir == "" {
		dir = repoDir
	}
	if dir == "" {
		return errors.New("don't know where the source repo is: pass -repo <dir> or set MOONLIGHT_REPO " +
			"(binaries built with scripts/build.go remember it)")
	}
	if _, err := os.Stat(filepath.Join(dir, "scripts", "build.go")); err != nil {
		return fmt.Errorf("%s is not a moonlight checkout (scripts/build.go not found); use -repo <dir>", dir)
	}

	steps := updateSteps(runtime.GOOS, !*noPull)
	for _, tool := range []string{"git", "go"} {
		if tool == "git" && *noPull {
			continue
		}
		if _, err := exec.LookPath(tool); err != nil {
			return fmt.Errorf("%s not found on PATH; update needs it", tool)
		}
	}
	for _, s := range steps {
		fmt.Printf("==> %s\n", joinCmd(s))
		if err := runStep(dir, s[0], s[1:]...); err != nil {
			return fmt.Errorf("%s failed: %w", s[0], err)
		}
	}
	fmt.Println("Update complete: the new build is installed and the daemon was relaunched.")
	return nil
}

func joinCmd(s []string) string {
	out := s[0]
	for _, a := range s[1:] {
		out += " " + a
	}
	return out
}
