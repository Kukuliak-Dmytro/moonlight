package schedule

import (
	"fmt"
	"math"
	"sort"
	"time"

	"moonlight/internal/config"
)

// Target represents the computed monitor settings for a specific moment in time.
type Target struct {
	Brightness int
	TempK      int
	RGB        [3]int
}

type parsedPoint struct {
	seconds    int
	brightness float64
	tempK      float64
	rgb        [3]float64
}

// Evaluate smoothly interpolates the configured 24-hour schedule at the given local time.
func Evaluate(cfg *config.Config, now time.Time) (Target, error) {
	if len(cfg.Schedule) == 0 {
		return Target{}, fmt.Errorf("empty schedule")
	}

	points := make([]parsedPoint, 0, len(cfg.Schedule))
	for _, p := range cfg.Schedule {
		mins, err := config.ParseHHMM(p.Time)
		if err != nil {
			return Target{}, err
		}
		var rgb [3]int
		if p.RGB != nil {
			rgb = *p.RGB
		} else {
			temp := p.TempK
			if temp == 0 {
				temp = 6500
			}
			rgb = KelvinToRGB(temp, cfg.RGBNeutral)
		}
		points = append(points, parsedPoint{
			seconds:    mins * 60,
			brightness: float64(p.Brightness),
			tempK:      float64(p.TempK),
			rgb:        [3]float64{float64(rgb[0]), float64(rgb[1]), float64(rgb[2])},
		})
	}

	sort.Slice(points, func(i, j int) bool {
		return points[i].seconds < points[j].seconds
	})

	if len(points) == 1 {
		p := points[0]
		return Target{
			Brightness: clampInt(int(math.Round(p.brightness)), 0, 100),
			TempK:      int(math.Round(p.tempK)),
			RGB: [3]int{
				clampInt(int(math.Round(p.rgb[0])), 0, 255),
				clampInt(int(math.Round(p.rgb[1])), 0, 255),
				clampInt(int(math.Round(p.rgb[2])), 0, 255),
			},
		}, nil
	}

	const daySec = 24 * 3600
	nowSec := now.Hour()*3600 + now.Minute()*60 + now.Second()

	var prev, next parsedPoint
	var prevSec, nextSec int

	if nowSec < points[0].seconds {
		prev = points[len(points)-1]
		next = points[0]
		prevSec = prev.seconds - daySec
		nextSec = next.seconds
	} else if nowSec >= points[len(points)-1].seconds {
		prev = points[len(points)-1]
		next = points[0]
		prevSec = prev.seconds
		nextSec = next.seconds + daySec
	} else {
		for i := 0; i < len(points)-1; i++ {
			if nowSec >= points[i].seconds && nowSec < points[i+1].seconds {
				prev = points[i]
				next = points[i+1]
				prevSec = prev.seconds
				nextSec = next.seconds
				break
			}
		}
	}

	span := nextSec - prevSec
	var ratio float64
	if span > 0 {
		ratio = float64(nowSec-prevSec) / float64(span)
	}

	b := lerp(prev.brightness, next.brightness, ratio)
	tK := lerp(prev.tempK, next.tempK, ratio)
	r := lerp(prev.rgb[0], next.rgb[0], ratio)
	g := lerp(prev.rgb[1], next.rgb[1], ratio)
	bl := lerp(prev.rgb[2], next.rgb[2], ratio)

	return Target{
		Brightness: clampInt(int(math.Round(b)), 0, 100),
		TempK:      int(math.Round(tK)),
		RGB: [3]int{
			clampInt(int(math.Round(r)), 0, 255),
			clampInt(int(math.Round(g)), 0, 255),
			clampInt(int(math.Round(bl)), 0, 255),
		},
	}, nil
}

// KelvinToRGB maps a color temperature in Kelvin to monitor RGB gain values
// scaled relative to neutral (6500K == neutral).
func KelvinToRGB(kelvin int, neutral [3]int) [3]int {
	ratios := KelvinToRatios(kelvin)
	return [3]int{
		clampInt(int(math.Round(float64(neutral[0])*ratios[0])), 0, 255),
		clampInt(int(math.Round(float64(neutral[1])*ratios[1])), 0, 255),
		clampInt(int(math.Round(float64(neutral[2])*ratios[2])), 0, 255),
	}
}

// KelvinToRatios returns normalized [R, G, B] multipliers in [0, 1] with 6500K == [1, 1, 1].
func KelvinToRatios(kelvin int) [3]float64 {
	if kelvin < 1500 {
		kelvin = 1500
	}
	if kelvin > 10000 {
		kelvin = 10000
	}
	if kelvin == 6500 {
		return [3]float64{1, 1, 1}
	}

	raw := blackbodyRaw(float64(kelvin))
	ref := blackbodyRaw(6500.0)

	return [3]float64{
		strengthen(raw[0] / ref[0]),
		strengthen(raw[1] / ref[1]),
		strengthen(raw[2] / ref[2]),
	}
}

// tintStrength scales how far each channel moves away from neutral: 1.3 makes the
// night-light tint 30% stronger than the plain blackbody curve at every temperature.
const tintStrength = 1.3

// strengthen amplifies a channel's deviation from 1.0 by tintStrength, clamped to [0, 1].
func strengthen(ratio float64) float64 {
	return clampFloat(1-tintStrength*(1-clampFloat(ratio, 0, 1)), 0, 1)
}

// blackbodyRaw implements Tanner Helland's color temperature approximation.
func blackbodyRaw(kelvin float64) [3]float64 {
	temp := kelvin / 100.0
	var r, g, b float64

	if temp <= 66 {
		r = 255
		g = 99.4708025861*math.Log(temp) - 161.1195681661
		if temp <= 19 {
			b = 0
		} else {
			b = 138.5177312231*math.Log(temp-10) - 305.0447927307
		}
	} else {
		r = 329.698727446 * math.Pow(temp-60, -0.1332047592)
		g = 288.1221695283 * math.Pow(temp-60, -0.0755148492)
		b = 255
	}

	return [3]float64{
		clampFloat(r, 0, 255),
		clampFloat(g, 0, 255),
		clampFloat(b, 0, 255),
	}
}

func lerp(a, b, t float64) float64 {
	return a + (b-a)*t
}

func clampFloat(v, min, max float64) float64 {
	if v < min {
		return min
	}
	if v > max {
		return max
	}
	return v
}

func clampInt(v, min, max int) int {
	if v < min {
		return min
	}
	if v > max {
		return max
	}
	return v
}
