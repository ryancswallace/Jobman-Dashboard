package presentation

import (
	"errors"
	"math"
	"regexp"
	"strconv"
)

// Theme changes brand accents only. Status colors and reading surfaces are fixed.
type Theme struct {
	Light AccentPair `json:"light"`
	Dark  AccentPair `json:"dark"`
}
type AccentPair struct {
	Accent     string `json:"accent"`
	Foreground string `json:"foreground"`
}

var hexColor = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)

func luminance(value string) float64 {
	n, _ := strconv.ParseUint(value[1:], 16, 24)
	channel := func(shift uint) float64 {
		v := float64((n>>shift)&255) / 255
		if v <= 0.04045 {
			return v / 12.92
		}
		return math.Pow((v+0.055)/1.055, 2.4)
	}
	return 0.2126*channel(16) + 0.7152*channel(8) + 0.0722*channel(0)
}
func contrast(a, b string) float64 {
	x, y := luminance(a), luminance(b)
	return (math.Max(x, y) + 0.05) / (math.Min(x, y) + 0.05)
}
func (t *Theme) Validate() error {
	if t == nil {
		return nil
	}
	for index, pair := range []AccentPair{t.Light, t.Dark} {
		if !hexColor.MatchString(pair.Accent) || !hexColor.MatchString(pair.Foreground) || contrast(pair.Accent, pair.Foreground) < 4.5 {
			return errors.New("theme requires hex accent/foreground pairs with contrast at least 4.5")
		}
		surfaces := []string{"#ffffff", "#f7f8fc", "#fafafe", "#efebfb"}
		if index == 1 {
			surfaces = []string{"#000000", "#14141d", "#1c1c28", "#222230", "#312645"}
		}
		for _, surface := range surfaces {
			if contrast(pair.Accent, surface) < 4.5 {
				return errors.New("theme accent must remain readable on fixed reading surfaces")
			}
		}
	}
	return nil
}
