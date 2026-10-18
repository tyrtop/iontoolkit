package ui

import "testing"

func TestBannerDegradesOnSmallTerminals(t *testing.T) {
	if got := BannerHeight(80, 24); got != len(bannerRows)+1 {
		t.Errorf("BannerHeight(80,24) = %d, want %d", got, len(bannerRows)+1)
	}
	if got := BannerHeight(80, 10); got != 1 {
		t.Errorf("short terminal bannerHeight = %d, want 1", got)
	}
	if got := BannerHeight(20, 40); got != 1 {
		t.Errorf("narrow terminal bannerHeight = %d, want 1", got)
	}
	for _, r := range bannerRows {
		if len(r) != bannerWidth {
			t.Errorf("banner row %q is %d wide, want %d", r, len(r), bannerWidth)
		}
	}
}
