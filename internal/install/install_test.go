package install

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

type call struct{ name, args string }

func fakeRunner(t *testing.T) *[]call {
	t.Helper()
	var calls []call
	orig := runner
	runner = func(name string, args ...string) ([]byte, error) {
		calls = append(calls, call{name, strings.Join(args, " ")})
		return nil, nil
	}
	t.Cleanup(func() { runner = orig })
	return &calls
}

func writeBin(t *testing.T, dir, content string) string {
	t.Helper()
	p := filepath.Join(dir, "src-moonlight")
	if err := os.WriteFile(p, []byte(content), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestRenderPlistEscapesAndPointsAtBinary(t *testing.T) {
	s, err := RenderPlist("/opt/a&b/moonlight", "/logs/<x>")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"<string>/opt/a&amp;b/moonlight</string>",
		"<string>daemon</string>",
		"<string>/logs/&lt;x&gt;/stderr.log</string>",
		"<key>KeepAlive</key>",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("plist missing %q", want)
		}
	}
}

// skipOnWindows: Install deliberately refuses on Windows (it has its own PowerShell
// installer), and creating symlinks there needs elevated rights.
func skipOnWindows(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("moonlight install is not supported on Windows")
	}
}

func TestInstallCopiesBinaryAndReplacesSymlink(t *testing.T) {
	skipOnWindows(t)
	calls := fakeRunner(t)
	root := t.TempDir()
	bin := filepath.Join(root, "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	src := writeBin(t, root, "new-build")

	// An old-style symlink pointing into a source tree, as `ln -s` setups leave behind.
	old := filepath.Join(root, "dist-moonlight")
	if err := os.WriteFile(old, []byte("old-build"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(old, filepath.Join(bin, "moonlight")); err != nil {
		t.Fatal(err)
	}

	p := NewPaths(root, 501)
	if err := Install(src, p, Options{BinDir: bin, NoAgent: true}); err != nil {
		t.Fatal(err)
	}

	dest := filepath.Join(bin, "moonlight")
	fi, err := os.Lstat(dest)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode()&os.ModeSymlink != 0 {
		t.Fatal("destination is still a symlink")
	}
	if fi.Mode().Perm()&0o111 == 0 {
		t.Fatalf("installed file is not executable: %v", fi.Mode())
	}
	if got, _ := os.ReadFile(dest); string(got) != "new-build" {
		t.Fatalf("installed content = %q", got)
	}
	if got, _ := os.ReadFile(old); string(got) != "old-build" {
		t.Fatal("the symlink's old target must be left untouched")
	}
	if len(*calls) != 0 {
		t.Fatalf("NoAgent must not call launchctl, got %v", *calls)
	}
}

func TestInstallIsIdempotentWhenAlreadyInstalled(t *testing.T) {
	skipOnWindows(t)
	fakeRunner(t)
	root := t.TempDir()
	bin := filepath.Join(root, "bin")
	_ = os.MkdirAll(bin, 0o755)
	dest := filepath.Join(bin, "moonlight")
	if err := os.WriteFile(dest, []byte("x"), 0o755); err != nil {
		t.Fatal(err)
	}
	// Installing the already-installed binary onto itself must not truncate it.
	if err := Install(dest, NewPaths(root, 501), Options{BinDir: bin, NoAgent: true}); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(dest); string(got) != "x" {
		t.Fatalf("self-install corrupted the binary: %q", got)
	}
}

func TestInstallRegistersLaunchAgentOnMac(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("LaunchAgent is macOS only")
	}
	calls := fakeRunner(t)
	root := t.TempDir()
	bin := filepath.Join(root, "bin")
	src := writeBin(t, root, "b")
	p := NewPaths(root, 501)

	if err := Install(src, p, Options{BinDir: bin}); err != nil {
		t.Fatal(err)
	}
	plist, err := os.ReadFile(p.Plist)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(plist), "<string>"+filepath.Join(bin, "moonlight")+"</string>") {
		t.Fatalf("plist does not run the installed copy:\n%s", plist)
	}
	want := []call{
		{"launchctl", "bootout gui/501/" + Label},
		{"launchctl", "bootstrap gui/501 " + p.Plist},
	}
	if len(*calls) != 2 || (*calls)[0] != want[0] || (*calls)[1] != want[1] {
		t.Fatalf("launchctl calls = %v, want %v", *calls, want)
	}
}

func TestUninstallRemovesAgentAndBinary(t *testing.T) {
	calls := fakeRunner(t)
	root := t.TempDir()
	bin := filepath.Join(root, "bin")
	_ = os.MkdirAll(bin, 0o755)
	_ = os.WriteFile(filepath.Join(bin, "moonlight"), []byte("x"), 0o755)
	p := NewPaths(root, 501)
	_ = os.MkdirAll(filepath.Dir(p.Plist), 0o755)
	_ = os.WriteFile(p.Plist, []byte("x"), 0o644)

	if err := Uninstall(p, bin, false, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(bin, "moonlight")); !os.IsNotExist(err) {
		t.Fatal("binary should be removed")
	}
	if runtime.GOOS == "darwin" {
		if _, err := os.Stat(p.Plist); !os.IsNotExist(err) {
			t.Fatal("plist should be removed")
		}
		if len(*calls) != 1 || (*calls)[0].args != "bootout gui/501/"+Label {
			t.Fatalf("launchctl calls = %v", *calls)
		}
	}
}
