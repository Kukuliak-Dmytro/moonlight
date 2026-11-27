// Package install implements `moonlight install` / `moonlight uninstall`: it copies the
// running binary to a bin directory on PATH and, on macOS, registers a LaunchAgent that
// runs the daemon from that copy.
//
// Running the daemon from a copy rather than from inside a source checkout matters on
// macOS: a background process launched from ~/Desktop or ~/Documents triggers a
// "would like to access files in your Desktop folder" prompt.
package install

import (
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// Label is the launchd job label of the moonlight daemon.
const Label = "com.moonlight.daemon"

// bootstrapRetryDelay is the pause between launchctl bootstrap attempts; tests shorten it.
var bootstrapRetryDelay = time.Second

// runner executes an external command; a variable so tests can record calls.
var runner = func(name string, args ...string) ([]byte, error) {
	return exec.Command(name, args...).CombinedOutput()
}

// Options controls Install.
type Options struct {
	BinDir  string // install directory; empty = auto-pick
	NoAgent bool   // copy the binary only, skip the LaunchAgent
	Out     io.Writer
}

// Paths groups the locations Install and Uninstall touch.
type Paths struct {
	Home     string
	Plist    string
	LogDir   string
	UID      int
	DomainID string // gui/<uid>
}

// NewPaths resolves the per-user locations from home.
func NewPaths(home string, uid int) Paths {
	return Paths{
		Home:     home,
		Plist:    filepath.Join(home, "Library", "LaunchAgents", Label+".plist"),
		LogDir:   filepath.Join(home, "Library", "Logs", "moonlight"),
		UID:      uid,
		DomainID: fmt.Sprintf("gui/%d", uid),
	}
}

// ChooseBinDir returns the first writable of /opt/homebrew/bin and /usr/local/bin, else
// ~/.local/bin (created if needed). onPath is false for the fallback, which many shells
// do not have on PATH.
func ChooseBinDir(home string) (dir string, onPath bool, err error) {
	for _, d := range []string{"/opt/homebrew/bin", "/usr/local/bin"} {
		if writable(d) {
			return d, true, nil
		}
	}
	d := filepath.Join(home, ".local", "bin")
	return d, false, os.MkdirAll(d, 0o755)
}

func writable(dir string) bool {
	fi, err := os.Stat(dir)
	if err != nil || !fi.IsDir() {
		return false
	}
	f, err := os.CreateTemp(dir, ".moonlight-write-test-*")
	if err != nil {
		return false
	}
	name := f.Name()
	f.Close()
	return os.Remove(name) == nil
}

// RenderPlist builds the LaunchAgent definition. Paths are XML-escaped.
func RenderPlist(binary, logDir string) (string, error) {
	esc := func(s string) (string, error) {
		var b bytes.Buffer
		if err := xml.EscapeText(&b, []byte(s)); err != nil {
			return "", err
		}
		return b.String(), nil
	}
	bin, err := esc(binary)
	if err != nil {
		return "", err
	}
	logs, err := esc(logDir)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key>
  <string>%s</string>
  <key>ProgramArguments</key>
  <array>
    <string>%s</string>
    <string>daemon</string>
  </array>
  <key>EnvironmentVariables</key>
  <dict>
    <key>PATH</key>
    <string>/opt/homebrew/bin:/usr/local/bin:/usr/bin:/bin</string>
  </dict>
  <key>RunAtLoad</key>
  <true/>
  <key>KeepAlive</key>
  <true/>
  <key>StandardOutPath</key>
  <string>%s/stdout.log</string>
  <key>StandardErrorPath</key>
  <string>%s/stderr.log</string>
</dict>
</plist>
`, Label, bin, logs, logs), nil
}

// copyBinary copies src to dir/moonlight as a real file via an atomic rename. An existing
// symlink at the destination is removed first. It is a no-op when src already is dest.
func copyBinary(src, dir string) (dest string, copied bool, err error) {
	dest = filepath.Join(dir, "moonlight")

	if fi, err := os.Lstat(dest); err == nil && fi.Mode()&os.ModeSymlink != 0 {
		if err := os.Remove(dest); err != nil {
			return "", false, err
		}
	} else if err == nil {
		if same, _ := sameFile(src, dest); same {
			return dest, false, nil
		}
	}

	in, err := os.Open(src)
	if err != nil {
		return "", false, err
	}
	defer in.Close()
	tmp, err := os.CreateTemp(dir, ".moonlight-install-*")
	if err != nil {
		return "", false, err
	}
	defer os.Remove(tmp.Name()) // no-op once renamed
	if _, err := io.Copy(tmp, in); err != nil {
		tmp.Close()
		return "", false, err
	}
	if err := tmp.Close(); err != nil {
		return "", false, err
	}
	if err := os.Chmod(tmp.Name(), 0o755); err != nil {
		return "", false, err
	}
	if err := os.Rename(tmp.Name(), dest); err != nil {
		return "", false, err
	}
	return dest, true, nil
}

func sameFile(a, b string) (bool, error) {
	fa, err := os.Stat(a)
	if err != nil {
		return false, err
	}
	fb, err := os.Stat(b)
	if err != nil {
		return false, err
	}
	return os.SameFile(fa, fb), nil
}

// Install copies src (normally the running binary) into the bin directory and, on macOS,
// (re)registers and starts the LaunchAgent. Running it again upgrades in place.
func Install(src string, p Paths, opt Options) error {
	if runtime.GOOS == "windows" {
		return errors.New("on Windows use scripts/install-windows-task.ps1")
	}
	out := opt.Out
	if out == nil {
		out = io.Discard
	}

	dir, onPath := opt.BinDir, true
	if dir == "" {
		var err error
		if dir, onPath, err = ChooseBinDir(p.Home); err != nil {
			return err
		}
	} else if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}

	dest, copied, err := copyBinary(src, dir)
	if err != nil {
		return fmt.Errorf("copy binary: %w", err)
	}
	if copied {
		fmt.Fprintf(out, "Installed %s\n", dest)
	} else {
		fmt.Fprintf(out, "Already installed at %s\n", dest)
	}
	if !onPath {
		fmt.Fprintf(out, "Note: %s may not be on your PATH; add it to your shell profile.\n", dir)
	}

	if runtime.GOOS != "darwin" || opt.NoAgent {
		return nil
	}

	plist, err := RenderPlist(dest, p.LogDir)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p.Plist), 0o755); err != nil {
		return err
	}
	if err := os.MkdirAll(p.LogDir, 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(p.Plist, []byte(plist), 0o644); err != nil {
		return err
	}

	// bootout first so a changed plist or binary is picked up; it fails harmlessly if not loaded.
	_, _ = runner("launchctl", "bootout", p.DomainID+"/"+Label)
	// bootout returns before the old job is fully gone, so the first bootstrap can fail
	// with "Input/output error"; retry briefly before giving up.
	var outp []byte
	var berr error
	for attempt := 0; attempt < 4; attempt++ {
		if outp, berr = runner("launchctl", "bootstrap", p.DomainID, p.Plist); berr == nil {
			break
		}
		time.Sleep(bootstrapRetryDelay)
	}
	if berr != nil {
		return fmt.Errorf("launchctl bootstrap: %w (%s)", berr, strings.TrimSpace(string(outp)))
	}
	fmt.Fprintf(out, "LaunchAgent started (%s). Logs: %s\n", p.Plist, p.LogDir)
	return nil
}

// Uninstall stops and removes the LaunchAgent and, unless keepBinary, the installed copy.
// The config directory is left alone.
func Uninstall(p Paths, binDir string, keepBinary bool, out io.Writer) error {
	if out == nil {
		out = io.Discard
	}
	if runtime.GOOS == "darwin" {
		_, _ = runner("launchctl", "bootout", p.DomainID+"/"+Label)
		if err := os.Remove(p.Plist); err == nil {
			fmt.Fprintf(out, "Removed LaunchAgent %s\n", p.Plist)
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	if keepBinary {
		return nil
	}
	dirs := []string{binDir}
	if binDir == "" {
		dirs = []string{"/opt/homebrew/bin", "/usr/local/bin", filepath.Join(p.Home, ".local", "bin")}
	}
	for _, d := range dirs {
		dest := filepath.Join(d, "moonlight")
		if err := os.Remove(dest); err == nil {
			fmt.Fprintf(out, "Removed %s\n", dest)
		} else if !errors.Is(err, os.ErrNotExist) {
			fmt.Fprintf(out, "Could not remove %s: %v\n", dest, err)
		}
	}
	return nil
}
