//go:build windows

package main

import (
	"easyshare/internal/desktoppet"
	"easyshare/internal/winui"
	"testing"
)

func TestPetVisualChangesOnlyInvalidateChangedPixels(t *testing.T) {
	state := petVisualState{}
	next := petVisualState{144, 96, 96, false, petPalette()}
	if !state.update(next) {
		t.Fatal("initial image not invalidated")
	}
	for i := 0; i < 100; i++ {
		if state.update(next) {
			t.Fatal("unchanged pointer sample invalidated image")
		}
	}
	changes := []func(*petVisualState){
		func(s *petVisualState) { s.pinned = true },
		func(s *petVisualState) { s.width++ },
		func(s *petVisualState) { s.height++ },
		func(s *petVisualState) { s.dpi = 144 },
		func(s *petVisualState) { s.palette.Surface++ },
		func(s *petVisualState) { s.palette.Ink++ },
	}
	for i, change := range changes {
		change(&next)
		if !state.update(next) || state.update(next) {
			t.Fatalf("change %d not invalidated exactly once", i)
		}
	}
	for _, dpi := range []int32{96, 120, 144, 192} {
		if winui.PetCornerRadius(dpi) != desktoppet.Scale(desktoppet.RadiusDIP, dpi) {
			t.Fatalf("region radius mismatch at %d DPI", dpi)
		}
	}
}
