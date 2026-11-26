package daemon

import (
	"context"
	"errors"
	"io"
	"log"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"moonlight/internal/config"
	"moonlight/internal/ddc"
	"moonlight/internal/gamma"
	"moonlight/internal/schedule"
)

type setCall struct {
	code  byte
	value uint32
}

type fakeMonitor struct {
	info  ddc.MonitorInfo
	fail  map[byte]bool
	calls []setCall
}

func (f *fakeMonitor) Info() ddc.MonitorInfo { return f.info }
func (f *fakeMonitor) Close() error          { return nil }
func (f *fakeMonitor) GetVCP(byte) (ddc.VCPValue, error) {
	return ddc.VCPValue{Current: 50, Max: 100}, nil
}
func (f *fakeMonitor) SetVCP(code byte, v uint32) error {
	if f.fail[code] {
		return errors.New("ddc write failed")
	}
	f.calls = append(f.calls, setCall{code, v})
	return nil
}

type fakeBackend struct{ mons []*fakeMonitor }

func (b *fakeBackend) Name() string { return "fake" }
func (b *fakeBackend) ListMonitors() ([]ddc.Monitor, error) {
	out := make([]ddc.Monitor, len(b.mons))
	for i, m := range b.mons {
		out[i] = m
	}
	return out, nil
}

func newMon(id string) *fakeMonitor {
	return &fakeMonitor{info: ddc.MonitorInfo{ID: id, Name: "Mon" + id, Device: "uuid-" + id}, fail: map[byte]bool{}}
}

func quiet() *log.Logger { return log.New(io.Discard, "", 0) }

// captureGamma replaces the gamma writer for one test and returns the recorded calls.
func captureGamma(t *testing.T, err error) *[]gamma.Ratios {
	t.Helper()
	var calls []gamma.Ratios
	origApply, origReset, origKeep, origEnsure := applyGamma, resetGamma, keepGamma, ensureGamma
	applyGamma = func(r gamma.Ratios) error { calls = append(calls, r); return err }
	resetGamma = func() error { return nil } // never reset the real display from a test
	keepGamma = func() error { return nil }  // nor touch a real holder
	ensureGamma = func() error { return nil }
	t.Cleanup(func() { applyGamma, resetGamma, keepGamma, ensureGamma = origApply, origReset, origKeep, origEnsure })
	return &calls
}

// TestMain points HOME at a throwaway directory. The real gamma holder is found through
// a socket under $HOME, so even a test that forgets to stub the gamma writers cannot
// reach the user's live holder and tear down their night light.
func TestMain(m *testing.M) {
	home, err := os.MkdirTemp("", "ml-daemon-test")
	if err != nil {
		panic(err)
	}
	os.Setenv("HOME", home)
	code := m.Run()
	os.RemoveAll(home)
	os.Exit(code)
}

func testCfg() *config.Config {
	cfg := config.DefaultConfig()
	cfg.MinBrightness = 5
	cfg.AllowZeroBrightness = false
	return cfg
}

func TestClampTargetBrightnessFloor(t *testing.T) {
	cfg := testCfg()
	cfg.MinBrightness = 20
	got := ClampTarget(cfg, schedule.Target{Brightness: 0, RGB: [3]int{100, 100, 100}})
	if got.Brightness != 20 {
		t.Fatalf("brightness = %d, want floor 20", got.Brightness)
	}
	got = ClampTarget(cfg, schedule.Target{Brightness: 500, RGB: [3]int{100, 100, 100}})
	if got.Brightness != 100 {
		t.Fatalf("brightness = %d, want cap 100", got.Brightness)
	}
}

func TestClampTargetZeroBrightnessNeedsOptIn(t *testing.T) {
	cfg := testCfg()
	cfg.MinBrightness = 0
	cfg.AllowZeroBrightness = false
	if got := ClampTarget(cfg, schedule.Target{Brightness: 0}).Brightness; got != defaultMinBrightness {
		t.Fatalf("brightness = %d, want default floor %d", got, defaultMinBrightness)
	}
	cfg.AllowZeroBrightness = true
	if got := ClampTarget(cfg, schedule.Target{Brightness: 0, RGB: [3]int{100, 100, 100}}).Brightness; got != 0 {
		t.Fatalf("brightness = %d, want 0 when explicitly allowed", got)
	}
}

func TestClampTargetRGBFloorAndCap(t *testing.T) {
	cfg := testCfg()
	cfg.MinRGB = 25
	got := ClampTarget(cfg, schedule.Target{Brightness: 50, RGB: [3]int{0, 24, 999}})
	if got.RGB != [3]int{25, 25, maxRGB} {
		t.Fatalf("rgb = %v, want [25 25 %d]", got.RGB, maxRGB)
	}
	cfg.MinRGB = 0 // a broken config must still not black the screen out
	got = ClampTarget(cfg, schedule.Target{Brightness: 50, RGB: [3]int{0, 0, 0}})
	if got.RGB != [3]int{minRGBFloor, minRGBFloor, minRGBFloor} {
		t.Fatalf("rgb = %v, want hard floor %d", got.RGB, minRGBFloor)
	}
}

func TestPlanMonitor(t *testing.T) {
	cfg := testCfg()
	cfg.MinDelta = 2
	tgt := schedule.Target{Brightness: 50, RGB: [3]int{100, 90, 80}}
	same := AppliedState{Brightness: 50, RGB: tgt.RGB, HasBrightness: true, HasRGB: true}

	cases := []struct {
		name     string
		prev     AppliedState
		havePrev bool
		force    bool
		want     monitorPlan
	}{
		{"unchanged", same, true, false, monitorPlan{}},
		{"first sight", AppliedState{}, false, false, monitorPlan{true, true}},
		{"force", same, true, true, monitorPlan{true, true}},
		{"below min delta", AppliedState{Brightness: 49, RGB: [3]int{99, 90, 80}, HasBrightness: true, HasRGB: true}, true, false, monitorPlan{}},
		{"brightness delta only", AppliedState{Brightness: 40, RGB: tgt.RGB, HasBrightness: true, HasRGB: true}, true, false, monitorPlan{Brightness: true}},
		{"one rgb channel", AppliedState{Brightness: 50, RGB: [3]int{100, 90, 70}, HasBrightness: true, HasRGB: true}, true, false, monitorPlan{RGB: true}},
		{"failed rgb retried", AppliedState{Brightness: 50, RGB: tgt.RGB, HasBrightness: true}, true, false, monitorPlan{RGB: true}},
	}
	for _, c := range cases {
		if got := planMonitor(cfg, tgt, c.prev, c.havePrev, c.force); got != c.want {
			t.Errorf("%s: plan = %+v, want %+v", c.name, got, c.want)
		}
	}
}

func TestApplyTargetGammaOnceForManyMonitors(t *testing.T) {
	calls := captureGamma(t, nil)
	b := &fakeBackend{mons: []*fakeMonitor{newMon("1"), newMon("2"), newMon("3")}}
	n, err := ApplyTarget(testCfg(), b, schedule.Target{Brightness: 40, RGB: [3]int{100, 80, 60}},
		ApplyOptions{Force: true}, map[string]AppliedState{}, quiet())
	if err != nil || n != 3 {
		t.Fatalf("n=%d err=%v, want 3 updated", n, err)
	}
	if len(*calls) != 1 {
		t.Fatalf("gamma written %d times, want exactly 1", len(*calls))
	}
	if (*calls)[0] != (gamma.Ratios{1, 0.8, 0.6}) {
		t.Fatalf("gamma ratios = %v", (*calls)[0])
	}
}

func TestApplyTargetBrightnessFailureDoesNotBlockGamma(t *testing.T) {
	calls := captureGamma(t, nil)
	m := newMon("1")
	m.fail[ddc.VCPBrightness] = true
	last := map[string]AppliedState{}
	n, err := ApplyTarget(testCfg(), &fakeBackend{mons: []*fakeMonitor{m}},
		schedule.Target{Brightness: 40, RGB: [3]int{100, 80, 60}}, ApplyOptions{Force: true}, last, quiet())
	if err == nil {
		t.Fatal("expected an error: the only monitor rejected its brightness write")
	}
	if len(*calls) != 1 {
		t.Fatalf("gamma writes = %d, want 1 despite DDC failure", len(*calls))
	}
	if n != 0 {
		t.Fatalf("updated = %d, want 0 (brightness failed)", n)
	}
	st := last[MonitorKey(m.info)]
	if st.HasBrightness || !st.HasRGB {
		t.Fatalf("state = %+v, want brightness retried and rgb remembered", st)
	}
}

func TestApplyTargetGammaFailureIsRetried(t *testing.T) {
	captureGamma(t, errors.New("boom"))
	m := newMon("1")
	last := map[string]AppliedState{}
	tgt := schedule.Target{Brightness: 40, RGB: [3]int{100, 80, 60}}
	if _, err := ApplyTarget(testCfg(), &fakeBackend{mons: []*fakeMonitor{m}}, tgt, ApplyOptions{Force: true}, last, quiet()); err == nil {
		t.Fatal("expected an error when gamma failed and nothing else was applied")
	}
	if last[MonitorKey(m.info)].HasRGB {
		t.Fatal("RGB must not be recorded as applied when gamma failed")
	}
	calls := captureGamma(t, nil)
	if _, err := ApplyTarget(testCfg(), &fakeBackend{mons: []*fakeMonitor{m}}, tgt, ApplyOptions{}, last, quiet()); err != nil {
		t.Fatal(err)
	}
	if len(*calls) != 1 {
		t.Fatalf("gamma retries = %d, want 1 on next tick", len(*calls))
	}
}

func TestApplyTargetSteadyStateOnlyReassertsGamma(t *testing.T) {
	calls := captureGamma(t, nil)
	m := newMon("1")
	last := map[string]AppliedState{}
	tgt := schedule.Target{Brightness: 40, RGB: [3]int{100, 80, 60}}
	b := &fakeBackend{mons: []*fakeMonitor{m}}
	for i := 0; i < 2; i++ {
		if _, err := ApplyTarget(testCfg(), b, tgt, ApplyOptions{}, last, quiet()); err != nil {
			t.Fatal(err)
		}
	}
	if len(m.calls) != 1 {
		t.Fatalf("ddc writes=%d, second tick must not rewrite DDC", len(m.calls))
	}
	if len(*calls) != 2 {
		t.Fatalf("gamma writes=%d, want 2: gamma is re-asserted every tick", len(*calls))
	}
}

func TestApplyTargetDryRunWritesNothing(t *testing.T) {
	calls := captureGamma(t, nil)
	m := newMon("1")
	n, err := ApplyTarget(testCfg(), &fakeBackend{mons: []*fakeMonitor{m}},
		schedule.Target{Brightness: 40, RGB: [3]int{100, 80, 60}}, ApplyOptions{Force: true, DryRun: true}, nil, quiet())
	if err != nil || n != 1 {
		t.Fatalf("n=%d err=%v", n, err)
	}
	if len(*calls) != 0 || len(m.calls) != 0 {
		t.Fatalf("dry run wrote gamma=%d ddc=%d", len(*calls), len(m.calls))
	}
}

func TestApplyTargetDDCModeSkipsGamma(t *testing.T) {
	calls := captureGamma(t, nil)
	cfg := testCfg()
	cfg.NightLightMode = "ddc"
	m := newMon("1")
	if _, err := ApplyTarget(cfg, &fakeBackend{mons: []*fakeMonitor{m}},
		schedule.Target{Brightness: 40, RGB: [3]int{100, 80, 60}}, ApplyOptions{Force: true}, nil, quiet()); err != nil {
		t.Fatal(err)
	}
	if len(*calls) != 0 {
		t.Fatal("gamma must not be written in ddc mode")
	}
	if len(m.calls) != 4 { // brightness + R + G + B
		t.Fatalf("ddc writes = %d, want 4", len(m.calls))
	}
}

func TestApplyTargetNoMonitorsErrors(t *testing.T) {
	calls := captureGamma(t, nil)
	_, err := ApplyTarget(testCfg(), &fakeBackend{}, schedule.Target{Brightness: 40, RGB: [3]int{100, 80, 60}},
		ApplyOptions{Force: true}, nil, quiet())
	if err == nil {
		t.Fatal("expected an error when no monitors match")
	}
	if len(*calls) != 0 {
		t.Fatal("gamma must not be written without a monitor")
	}
}

func TestApplyTargetForgetsDisconnectedMonitors(t *testing.T) {
	captureGamma(t, nil)
	gone := newMon("9")
	last := map[string]AppliedState{MonitorKey(gone.info): {HasBrightness: true, HasRGB: true}}
	if _, err := ApplyTarget(testCfg(), &fakeBackend{mons: []*fakeMonitor{newMon("1")}},
		schedule.Target{Brightness: 40, RGB: [3]int{100, 80, 60}}, ApplyOptions{Force: true}, last, quiet()); err != nil {
		t.Fatal(err)
	}
	if _, ok := last[MonitorKey(gone.info)]; ok {
		t.Fatal("state of a disconnected monitor must be dropped so it is re-applied on return")
	}
}

func TestResetAllHonorsSafeBrightnessAndFloor(t *testing.T) {
	captureGamma(t, nil)
	cases := []struct{ safe, min, want int }{
		{50, 0, 50},
		{10, 0, config.FallbackSafeBrightness}, // below the rescue floor
		{200, 0, config.FallbackSafeBrightness},
	}
	for _, c := range cases {
		cfg := testCfg()
		cfg.SafeBrightness, cfg.MinBrightness = c.safe, c.min
		m := newMon("1")
		if err := ResetAll(cfg, &fakeBackend{mons: []*fakeMonitor{m}}, quiet()); err != nil {
			t.Fatal(err)
		}
		if m.calls[0] != (setCall{ddc.VCPBrightness, uint32(c.want)}) {
			t.Errorf("safe=%d: first write %+v, want brightness %d", c.safe, m.calls[0], c.want)
		}
	}
}

func TestMonitorKeyIgnoresListIndexWhenDeviceKnown(t *testing.T) {
	a := ddc.MonitorInfo{ID: "1", Name: "M27UP", Device: "UUID-A"}
	b := ddc.MonitorInfo{ID: "2", Name: "M27UP", Device: "UUID-A"}
	if MonitorKey(a) != MonitorKey(b) {
		t.Fatal("key must survive a display reorder when a hardware id is known")
	}
	if MonitorKey(ddc.MonitorInfo{ID: "1", Name: "X"}) == MonitorKey(ddc.MonitorInfo{ID: "2", Name: "X"}) {
		t.Fatal("without a hardware id the index must still distinguish monitors")
	}
}

func TestBackupSurvivesReorderAndMigratesLegacyKey(t *testing.T) {
	path := t.TempDir() + "/backup.json"
	b := config.Backup{Monitors: map[string]config.MonitorSnapshot{
		"1|M27UP|UUID-A": {Name: "M27UP", Device: "UUID-A", HasBrightness: true, Brightness: 41},
	}}
	if err := config.SaveBackup(path, b); err != nil {
		t.Fatal(err)
	}

	// Same monitor, now listed at index 2, and already tinted (brightness 0).
	m := newMon("2")
	m.info = ddc.MonitorInfo{ID: "2", Name: "M27UP", Device: "UUID-A"}
	ensureBackupSnapshot(path, m, quiet())

	got, err := config.LoadBackup(path)
	if err != nil {
		t.Fatal(err)
	}
	snap, ok := got.Monitors[MonitorKey(m.info)]
	if !ok || snap.Brightness != 41 {
		t.Fatalf("snapshot = %+v ok=%v, want original brightness 41 kept under the new key", snap, ok)
	}
	if _, stale := got.Monitors["1|M27UP|UUID-A"]; stale {
		t.Fatal("legacy key should be migrated away")
	}
}

func TestRestoreBackupFindsLegacyKey(t *testing.T) {
	captureGamma(t, nil)
	path := t.TempDir() + "/backup.json"
	m := newMon("3")
	b := config.Backup{Monitors: map[string]config.MonitorSnapshot{
		m.info.ID + "|" + m.info.Name + "|" + m.info.Device: {Name: m.info.Name, Device: m.info.Device, HasBrightness: true, Brightness: 41},
	}}
	if err := config.SaveBackup(path, b); err != nil {
		t.Fatal(err)
	}
	if err := RestoreBackup(testCfg(), &fakeBackend{mons: []*fakeMonitor{m}}, path, quiet()); err != nil {
		t.Fatal(err)
	}
	if len(m.calls) == 0 || m.calls[0] != (setCall{ddc.VCPBrightness, 41}) {
		t.Fatalf("restore calls = %+v, want brightness 41", m.calls)
	}
}

func TestGammaLeaseOutlivesPollInterval(t *testing.T) {
	cfg := testCfg()
	cfg.PollIntervalSec = 60
	if got := gammaLease(cfg); got != gamma.DefaultLease {
		t.Fatalf("lease = %v, want default %v", got, gamma.DefaultLease)
	}
	cfg.PollIntervalSec = 600
	if got := gammaLease(cfg); got != 30*time.Minute {
		t.Fatalf("lease = %v, want 3x a 10 min poll", got)
	}
}

func TestApplyTargetPartialFailureIsNotAnError(t *testing.T) {
	captureGamma(t, nil)
	bad, good := newMon("1"), newMon("2")
	bad.fail[ddc.VCPBrightness] = true
	n, err := ApplyTarget(testCfg(), &fakeBackend{mons: []*fakeMonitor{bad, good}},
		schedule.Target{Brightness: 40, RGB: [3]int{100, 80, 60}}, ApplyOptions{Force: true}, nil, quiet())
	if err != nil || n != 1 {
		t.Fatalf("n=%d err=%v, want 1 updated and no error while one monitor still works", n, err)
	}
}

func TestPausedDaemonKeepsTheTintAlive(t *testing.T) {
	var keeps, applies atomic.Int32
	captureGamma(t, nil) // stubs every gamma entry point, including ensureGamma and resetGamma
	keepGamma = func() error { keeps.Add(1); return nil }
	applyGamma = func(gamma.Ratios) error { applies.Add(1); return nil }

	cfgPath := filepath.Join(t.TempDir(), "config.json")
	cfg := config.DefaultConfig()
	cfg.PollIntervalSec = 1
	if err := config.Save(cfgPath, cfg); err != nil {
		t.Fatal(err)
	}
	if err := config.SaveState(config.StatePathForConfig(cfgPath), config.State{Paused: true, Reason: "test"}); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 1500*time.Millisecond)
	defer cancel()
	if err := Run(ctx, cfgPath, false, quiet()); err != nil {
		t.Fatal(err)
	}
	if keeps.Load() < 2 {
		t.Fatalf("keepalive calls = %d, want one per tick while paused (>=2)", keeps.Load())
	}
	if applies.Load() != 0 {
		t.Fatalf("a paused daemon must not write a new tint, wrote %d", applies.Load())
	}
}
