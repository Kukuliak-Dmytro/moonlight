package main

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestUpdateStepsOrder(t *testing.T) {
	mac := updateSteps("darwin", true)
	want := [][]string{
		{"git", "pull", "--ff-only"},
		{"go", "run", "./scripts/build.go"},
		{filepath.Join("dist", "moonlight"), "install"},
	}
	if !reflect.DeepEqual(mac, want) {
		t.Fatalf("darwin steps = %v, want %v", mac, want)
	}
	if got := updateSteps("darwin", false); len(got) != 2 || got[0][0] != "go" {
		t.Fatalf("-no-pull must drop the pull step, got %v", got)
	}
	win := updateSteps("windows", true)
	if last := win[len(win)-1]; last[0] != "powershell" || !strings.HasSuffix(last[len(last)-1], "install-windows-task.ps1") {
		t.Fatalf("windows must finish with the task script, got %v", last)
	}
}

func TestUpdateRunsStepsInRepo(t *testing.T) {
	repo := t.TempDir()
	if err := os.MkdirAll(filepath.Join(repo, "scripts"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "scripts", "build.go"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	var dirs, names []string
	old := runStep
	runStep = func(dir, name string, args ...string) error {
		dirs, names = append(dirs, dir), append(names, name)
		return nil
	}
	defer func() { runStep = old }()

	if err := cmdUpdate([]string{"-repo", repo, "-no-pull"}); err != nil {
		t.Fatal(err)
	}
	if len(names) != 2 || names[0] != "go" {
		t.Fatalf("steps = %v", names)
	}
	for _, d := range dirs {
		if d != repo {
			t.Fatalf("step ran in %q, want %q", d, repo)
		}
	}
}

func TestUpdateRejectsUnknownRepo(t *testing.T) {
	t.Setenv("MOONLIGHT_REPO", "")
	old := repoDir
	repoDir = ""
	defer func() { repoDir = old }()
	if err := cmdUpdate(nil); err == nil || !strings.Contains(err.Error(), "-repo") {
		t.Fatalf("want a hint about -repo, got %v", err)
	}
	if err := cmdUpdate([]string{"-repo", t.TempDir()}); err == nil {
		t.Fatal("a directory without scripts/build.go must be rejected")
	}
}
