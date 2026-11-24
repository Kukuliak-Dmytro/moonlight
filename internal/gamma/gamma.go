package gamma

// Ratios represents normalized [R, G, B] color channel multipliers in [0.25, 1.0].
type Ratios [3]float64

// ClampRatios enforces safety floors so gamma can never black out the screen.
func ClampRatios(r Ratios) Ratios {
	for i := 0; i < 3; i++ {
		if r[i] < 0.25 {
			r[i] = 0.25
		}
		if r[i] > 1.0 {
			r[i] = 1.0
		}
	}
	return r
}

// Neutral returns identity [1, 1, 1] gamma multipliers.
func Neutral() Ratios {
	return Ratios{1, 1, 1}
}
