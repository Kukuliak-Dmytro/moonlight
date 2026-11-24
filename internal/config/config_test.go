package config

import "testing"

func TestValidateBlocksZeroBrightnessByDefault(t *testing.T) {
	cfg := DefaultConfig()
	cfg.MinBrightness = 0
	cfg.AllowZeroBrightness = false

	if err := cfg.Validate(); err == nil {
		t.Fatalf("expected Validate to reject min_brightness=0 without allow_zero_brightness=true")
	}
}

func TestValidateBlocksScheduleBelowMinBrightness(t *testing.T) {
	cfg := DefaultConfig()
	cfg.MinBrightness = 10
	cfg.Schedule[0].Brightness = 0

	if err := cfg.Validate(); err == nil {
		t.Fatalf("expected Validate to reject schedule brightness below min_brightness")
	}
}

func TestValidateBlocksBlackoutRGB(t *testing.T) {
	cfg := DefaultConfig()
	zeroRGB := [3]int{0, 0, 0}
	cfg.Schedule[0].RGB = &zeroRGB

	if err := cfg.Validate(); err == nil {
		t.Fatalf("expected Validate to reject rgb=[0,0,0] below min_rgb floor")
	}
}
