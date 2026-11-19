package desktoppet

import (
	"easyshare/internal/winui"
	"math"
	"testing"
)

func TestRevealFullHeightFixedHost(t *testing.T) {
	final := winui.Rect{Left: 100, Top: 96, Right: 500, Bottom: 456}
	for _, up := range []bool{false, true} {
		for _, p := range []float64{0, .1, .5, 1} {
			b, c := RevealBounds(final, p, up)
			if b.Width() != 400 || b.Height() != 360 || c.Top < 0 || c.Bottom > 360 {
				t.Fatalf("bounds=%+v clip=%+v", b, c)
			}
			if up && b.Top != 360-c.Height() {
				t.Fatal("upward offset")
			}
			if !up && b.Top != c.Height()-360 {
				t.Fatal("downward offset")
			}
		}
	}
}
func TestRevealBezierSolvesX(t *testing.T) {
	// u=.5 gives x=.3425, y=.875; simply treating x as u is incorrect.
	if math.Abs(RevealEase(.3425)-.875) > 1e-10 {
		t.Fatal(RevealEase(.3425))
	}
}
func TestPanelCompressesBeforeFlipping(t *testing.T) {
	work := winui.Rect{Right: 1920, Bottom: 1000}
	pet := winui.Rect{Left: 500, Top: 604, Right: 644, Bottom: 700}
	got := PanelRect(pet, work, 400, 360, 0)
	if got.Top != 700 || got.Height() != 300 {
		t.Fatal(got)
	}
	pet.Top = 704
	pet.Bottom = 800
	got = PanelRect(pet, work, 400, 360, 0)
	if got.Bottom != pet.Top || got.Height() != 360 {
		t.Fatal(got)
	}
}
