package desktoppet

import "easyshare/internal/winui"

// RevealEase solves x(u) before sampling y(u) for cubic-bezier(.22,1,.36,1).
func RevealEase(x float64) float64 {
	if x <= 0 {
		return 0
	}
	if x >= 1 {
		return 1
	}
	lo, hi := 0.0, 1.0
	for i := 0; i < 48; i++ {
		u := (lo + hi) / 2
		v := 1 - u
		bx := 3*v*v*u*.22 + 3*v*u*u*.36 + u*u*u
		if bx < x {
			lo = u
		} else {
			hi = u
		}
	}
	u := (lo + hi) / 2
	v := 1 - u
	return 1 - v*v*v
}

// RevealBounds keeps the controller viewport full-size and translates its contents
// inside a fixed, clipped host. Upward fallback enters from below the pet's top edge.
func RevealBounds(final winui.Rect, progress float64, upward bool) (bounds, clip winui.Rect) {
	progress = max(0, min(1, progress))
	offset := int32(float64(final.Height())*(1-progress) + .5)
	bounds = winui.Rect{Right: final.Width(), Bottom: final.Height()}
	clip = bounds
	if upward {
		bounds.Top += offset
		bounds.Bottom += offset
		clip.Top = offset
	} else {
		bounds.Top -= offset
		bounds.Bottom -= offset
		clip.Bottom -= offset
	}
	return
}
