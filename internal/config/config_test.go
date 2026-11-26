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

func TestEffectiveSafeBrightness(t *testing.T) {
	cases := []struct{ safe, min, want int }{
		{50, 0, 50},
		{30, 0, 30},
		{29, 0, FallbackSafeBrightness},
		{101, 0, FallbackSafeBrightness},
		{40, 60, FallbackSafeBrightness},
	}
	for _, c := range cases {
		cfg := DefaultConfig()
		cfg.SafeBrightness, cfg.MinBrightness = c.safe, c.min
		if got := cfg.EffectiveSafeBrightness(); got != c.want {
			t.Errorf("safe=%d min=%d: got %d, want %d", c.safe, c.min, got, c.want)
		}
	}
}
