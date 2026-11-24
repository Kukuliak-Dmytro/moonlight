package schedule

import (
	"testing"
	"time"

	"monlight/internal/config"
)

func at(hour, min int) time.Time {
	return time.Date(2026, 11, 24, hour, min, 0, 0, time.Local)
}

func TestKelvinToRGBNeutral(t *testing.T) {
	rgb := KelvinToRGB(6500, [3]int{100, 100, 100})
	if rgb != [3]int{100, 100, 100} {
		t.Fatalf("expected [100 100 100] at 6500K, got %v", rgb)
	}

	rgb50 := KelvinToRGB(6500, [3]int{50, 50, 50})
	if rgb50 != [3]int{50, 50, 50} {
		t.Fatalf("expected [50 50 50] at 6500K, got %v", rgb50)
	}
}

func TestKelvinToRGBWarmMonotonic(t *testing.T) {
	c6500 := KelvinToRGB(6500, [3]int{100, 100, 100})
	c4500 := KelvinToRGB(4500, [3]int{100, 100, 100})
	c3200 := KelvinToRGB(3200, [3]int{100, 100, 100})

	if !(c6500[2] > c4500[2] && c4500[2] > c3200[2]) {
		t.Fatalf("expected blue channel to decrease as temperature warms: 6500=%v 4500=%v 3200=%v", c6500, c4500, c3200)
	}
	if !(c6500[1] > c4500[1] && c4500[1] > c3200[1]) {
		t.Fatalf("expected green channel to decrease as temperature warms: 6500=%v 4500=%v 3200=%v", c6500, c4500, c3200)
	}
	if c3200[0] != 100 {
		t.Fatalf("expected red channel to stay 100 at warm temps, got %v", c3200)
	}
}

func TestEvaluateInterpolationAndWrapAround(t *testing.T) {
	cfg := &config.Config{
		EnableBrightness: true,
		EnableNightLight: true,
		RGBNeutral:       [3]int{100, 100, 100},
		Schedule: []config.Point{
			{Time: "08:00", Brightness: 40, TempK: 5000},
			{Time: "12:00", Brightness: 80, TempK: 6500},
			{Time: "22:00", Brightness: 20, TempK: 3400},
		},
	}

	// Exact keyframe
	noon, err := Evaluate(cfg, at(12, 0))
	if err != nil {
		t.Fatalf("Evaluate noon: %v", err)
	}
	if noon.Brightness != 80 || noon.RGB != [3]int{100, 100, 100} {
		t.Fatalf("unexpected noon target: %+v", noon)
	}

	// Midpoint between 08:00 (40) and 12:00 (80) -> 60
	midMorning, err := Evaluate(cfg, at(10, 0))
	if err != nil {
		t.Fatalf("Evaluate 10:00: %v", err)
	}
	if midMorning.Brightness != 60 {
		t.Fatalf("expected brightness 60 at 10:00, got %d", midMorning.Brightness)
	}

	// Wrap-around across midnight: 22:00 (20) -> 08:00 (40) is 10 hours;
	// 03:00 is exact midpoint -> brightness 30.
	midnightWrap, err := Evaluate(cfg, at(3, 0))
	if err != nil {
		t.Fatalf("Evaluate 03:00: %v", err)
	}
	if midnightWrap.Brightness != 30 {
		t.Fatalf("expected brightness 30 at 03:00 (wrap midpoint), got %d", midnightWrap.Brightness)
	}
}
