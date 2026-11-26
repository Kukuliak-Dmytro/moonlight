//go:build darwin

package gamma

import (
	"bufio"
	"fmt"
	"math"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// The holder re-executes the current binary as `gamma-holder`; under `go test` that
// binary is the test binary, so route that argument to RunHolder.
func TestMain(m *testing.M) {
	if len(os.Args) > 1 && os.Args[1] == "gamma-holder" {
		if err := RunHolder(); err != nil {
			os.Exit(1)
		}
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func readBlueMax(t *testing.T) float64 {
	t.Helper()
	script := `ObjC.import("CoreGraphics");var a=Ref(),b=Ref(),c=Ref(),d=Ref(),e=Ref(),f=Ref(),g=Ref(),h=Ref(),i=Ref();` +
		`$.CGGetDisplayTransferByFormula($.CGMainDisplayID(),a,b,c,d,e,f,g,h,i);h[0]`
	out, err := exec.Command("/usr/bin/osascript", "-l", "JavaScript", "-e", script).CombinedOutput()
	if err != nil {
		t.Fatalf("read gamma: %v (%s)", err, out)
	}
	v, err := strconv.ParseFloat(strings.TrimSpace(string(out)), 64)
	if err != nil {
		t.Fatalf("parse gamma %q: %v", out, err)
	}
	return v
}

// Live test against the real display: it briefly dims the blue channel to 0.9 (about
// 10 seconds) and proves the tint outlives the process that set it, then lapses.
// Opt-in because it touches the screen and fights any running moonlight daemon's tint:
// stop the daemon first. MOONLIGHT_LIVE_GAMMA=1 go test ./internal/gamma
func TestLiveHolderKeepsGammaThenRestores(t *testing.T) {
	if os.Getenv("MOONLIGHT_LIVE_GAMMA") == "" {
		t.Skip("set MOONLIGHT_LIVE_GAMMA=1 to run against the real display")
	}
	home, err := os.MkdirTemp("/tmp", "ml") // short path: unix sockets are limited to ~104 bytes
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(home)
	t.Setenv("HOME", home)
	SetLease(3 * time.Second)
	defer SetLease(DefaultLease)

	if err := Apply(Ratios{1, 1, 0.9}); err != nil {
		t.Fatal(err)
	}
	time.Sleep(time.Second)
	if got := readBlueMax(t); math.Abs(got-0.9) > 0.01 {
		t.Fatalf("gamma while held = %v, want ~0.9 (visible to another process)", got)
	}

	time.Sleep(2 * time.Second) // 3s after the first Apply: it would have lapsed without a refresh
	if err := Apply(Ratios{1, 1, 0.9}); err != nil {
		t.Fatalf("refresh: %v", err)
	}
	time.Sleep(2 * time.Second)
	if got := readBlueMax(t); math.Abs(got-0.9) > 0.01 {
		t.Fatalf("gamma after refresh = %v, want still ~0.9", got)
	}

	time.Sleep(5 * time.Second) // lease lapses, helper exits, macOS restores
	if got := readBlueMax(t); math.Abs(got-1.0) > 0.001 {
		t.Fatalf("gamma after lease lapse = %v, want restored to 1.0", got)
	}
}

// ---- hermetic holder tests: the real holder logic with a fake child, an isolated HOME and
// fast clocks. They never touch the display or the user's running holder. ----

type fakeHolder struct {
	home    string
	childIn string // file the fake child appends every line it receives to
	done    chan error
}

func startFakeHolder(t *testing.T, grace time.Duration) *fakeHolder {
	t.Helper()
	home, err := os.MkdirTemp("/tmp", "mlh") // short path: unix sockets are limited to ~104 bytes
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	childIn := filepath.Join(home, "child.log")

	origChild, origRefresh, origGrace := childCommand, refreshInterval, initialGrace
	childCommand = func() *exec.Cmd {
		return exec.Command("/bin/sh", "-c", fmt.Sprintf(`while read l; do echo "$l" >> %q; echo ok; done`, childIn))
	}
	refreshInterval, initialGrace = 40*time.Millisecond, grace
	SetLease(DefaultLease)

	h := &fakeHolder{home: home, childIn: childIn, done: make(chan error, 1)}
	go func() { h.done <- RunHolder() }()
	t.Cleanup(func() {
		// Make sure the holder is gone before the globals it reads are restored.
		if conn, err := net.DialTimeout("unix", h.sock(), 200*time.Millisecond); err == nil {
			_ = roundTrip(conn, "RESET")
			conn.Close()
		}
		select {
		case <-h.done:
		case <-time.After(3 * time.Second):
		}
		childCommand, refreshInterval, initialGrace = origChild, origRefresh, origGrace
		SetLease(DefaultLease)
		os.RemoveAll(home)
	})

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if c, err := net.DialTimeout("unix", h.sock(), 100*time.Millisecond); err == nil {
			c.Close()
			return h
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("fake holder did not start listening")
	return nil
}

func (h *fakeHolder) sock() string {
	return filepath.Join(h.home, "Library", "Application Support", "moonlight", "gamma.sock")
}

func (h *fakeHolder) childLines() []string {
	data, _ := os.ReadFile(h.childIn)
	return strings.Fields(strings.ReplaceAll(strings.TrimSpace(string(data)), "\n", ";"))
}

func (h *fakeHolder) exited(within time.Duration) bool {
	select {
	case <-h.done:
		return true
	case <-time.After(within):
		return false
	}
}

func TestHolderReassertsTintWithoutHelp(t *testing.T) {
	h := startFakeHolder(t, 5*time.Second)
	SetLease(30 * time.Second)
	if err := Apply(Ratios{1, 0.8, 0.6}); err != nil {
		t.Fatal(err)
	}
	time.Sleep(400 * time.Millisecond)
	data, _ := os.ReadFile(h.childIn)
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) < 5 {
		t.Fatalf("child received %d writes in 400ms, want the tint re-asserted every refresh (>=5): %v", len(lines), lines)
	}
	for _, l := range lines {
		if l != "1.00000 0.80000 0.60000" {
			t.Fatalf("unexpected write to child %q", l)
		}
	}
}

func TestHolderReleasesWhenLeaseLapses(t *testing.T) {
	h := startFakeHolder(t, 5*time.Second)
	SetLease(time.Second)
	if err := Apply(Ratios{1, 0.8, 0.6}); err != nil {
		t.Fatal(err)
	}
	if !h.exited(3 * time.Second) {
		t.Fatal("holder still running long after its lease lapsed")
	}
	if _, err := os.Stat(h.sock()); !os.IsNotExist(err) {
		t.Fatalf("socket should be removed on exit, stat err=%v", err)
	}
}

func TestHolderThatNeverGetsARequestStillExits(t *testing.T) {
	h := startFakeHolder(t, 300*time.Millisecond)
	if !h.exited(2 * time.Second) {
		t.Fatal("an idle holder must give up after the initial grace, not linger forever")
	}
}

func TestKeepaliveExtendsButNeverShortensTheLease(t *testing.T) {
	h := startFakeHolder(t, 5*time.Second)
	SetLease(time.Second)
	if err := Apply(Ratios{1, 0.8, 0.6}); err != nil {
		t.Fatal(err)
	}
	SetLease(3 * time.Second)
	if err := Keepalive(); err != nil {
		t.Fatalf("keepalive: %v", err)
	}
	if h.exited(1500 * time.Millisecond) {
		t.Fatal("holder exited at 1.5s although the lease was extended to 3s")
	}
	SetLease(time.Second) // a shorter touch must not pull the deadline in
	if err := Keepalive(); err != nil {
		t.Fatalf("keepalive: %v", err)
	}
	if h.exited(1100 * time.Millisecond) {
		t.Fatal("a shorter keepalive shortened the lease")
	}
	if !h.exited(3 * time.Second) {
		t.Fatal("holder never released after the extended lease lapsed")
	}
}

func TestKeepaliveWithoutAHolderIsANoOp(t *testing.T) {
	home, err := os.MkdirTemp("/tmp", "mlh")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(home)
	t.Setenv("HOME", home)
	if err := Keepalive(); err != nil {
		t.Fatalf("keepalive with nothing to keep should be silent, got %v", err)
	}
	if _, err := os.Stat(filepath.Join(home, "Library", "Application Support", "moonlight", "gamma.sock")); err == nil {
		t.Fatal("keepalive must not start a holder")
	}
}

func TestOnlyOneHolderRuns(t *testing.T) {
	h := startFakeHolder(t, 5*time.Second)
	second := make(chan error, 1)
	go func() { second <- RunHolder() }()
	select {
	case err := <-second:
		if err != nil {
			t.Fatalf("second holder should exit quietly, got %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("a second holder kept running next to the first")
	}
	SetLease(30 * time.Second)
	if err := Apply(Ratios{1, 0.9, 0.9}); err != nil {
		t.Fatalf("the first holder must keep serving: %v", err)
	}
	_ = h
}

func TestResetRequestReleasesAndExits(t *testing.T) {
	h := startFakeHolder(t, 5*time.Second)
	SetLease(30 * time.Second)
	if err := Apply(Ratios{1, 0.8, 0.6}); err != nil {
		t.Fatal(err)
	}
	conn, err := net.DialTimeout("unix", h.sock(), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if err := roundTrip(conn, "RESET"); err != nil {
		t.Fatal(err)
	}
	if !h.exited(2 * time.Second) {
		t.Fatal("holder did not exit after RESET")
	}
}

func TestEnsureCurrentLeavesACurrentHolderAlone(t *testing.T) {
	h := startFakeHolder(t, 5*time.Second)
	SetLease(30 * time.Second)
	if err := Apply(Ratios{1, 0.8, 0.6}); err != nil {
		t.Fatal(err)
	}
	if err := EnsureCurrent(); err != nil {
		t.Fatal(err)
	}
	if h.exited(300 * time.Millisecond) {
		t.Fatal("EnsureCurrent must not stop a holder that speaks the current protocol")
	}
	if err := Apply(Ratios{1, 0.8, 0.6}); err != nil {
		t.Fatalf("holder should still serve: %v", err)
	}
}

func TestEnsureCurrentWithoutAHolderIsANoOp(t *testing.T) {
	home, err := os.MkdirTemp("/tmp", "mlh")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(home)
	t.Setenv("HOME", home)
	if err := EnsureCurrent(); err != nil {
		t.Fatal(err)
	}
}

// A holder from before the PING protocol answers "unknown command" to it but still obeys
// RESET; EnsureCurrent must recognise that and get rid of it.
func TestEnsureCurrentRetiresAnOldHolder(t *testing.T) {
	home, err := os.MkdirTemp("/tmp", "mlh")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(home)
	t.Setenv("HOME", home)
	path, err := socketPath()
	if err != nil {
		t.Fatal(err)
	}
	ln, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	reset := make(chan struct{})
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			line, _ := bufio.NewReader(c).ReadString('\n')
			if strings.HasPrefix(line, "RESET") {
				fmt.Fprintln(c, "OK")
				c.Close()
				ln.Close()
				os.Remove(path)
				close(reset)
				return
			}
			fmt.Fprintln(c, "ERR unknown command \"PING\"")
			c.Close()
		}
	}()
	if err := EnsureCurrent(); err != nil {
		t.Fatalf("EnsureCurrent: %v", err)
	}
	select {
	case <-reset:
	default:
		t.Fatal("the old holder was never asked to quit")
	}
}
