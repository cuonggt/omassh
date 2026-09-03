package ui

import "testing"

// tile must cover the whole area exactly: any gap or overlap shows up as a
// torn layout with panes drawn over each other.
func TestTileCoversTheAreaExactly(t *testing.T) {
	const W, H = 120, 40

	for n := 1; n <= 9; n++ {
		rects := tile(n, W, H)
		if len(rects) != n {
			t.Fatalf("tile(%d) returned %d rects", n, len(rects))
		}

		area := 0
		for _, r := range rects {
			if r.w <= 0 || r.h <= 0 {
				t.Errorf("n=%d: non-positive rect %+v", n, r)
			}
			if r.x < 0 || r.y < 0 || r.x+r.w > W || r.y+r.h > H {
				t.Errorf("n=%d: rect %+v escapes the %dx%d area", n, r, W, H)
			}
			area += r.w * r.h
		}
		if area != W*H {
			t.Errorf("n=%d: rects cover %d cells, want %d (gap or overlap)", n, area, W*H)
		}

		// No two panes may overlap.
		for i := range rects {
			for j := i + 1; j < len(rects); j++ {
				if overlap(rects[i], rects[j]) {
					t.Errorf("n=%d: %+v overlaps %+v", n, rects[i], rects[j])
				}
			}
		}
	}
}

func TestTileShapes(t *testing.T) {
	// Two panes side by side, not stacked.
	two := tile(2, 100, 40)
	if two[0].y != two[1].y || two[0].x == two[1].x {
		t.Errorf("two panes should be side by side, got %+v", two)
	}
	// Four panes in a 2x2.
	four := tile(4, 100, 40)
	if four[0].y != four[1].y || four[2].y != four[3].y || four[0].y == four[2].y {
		t.Errorf("four panes should be 2x2, got %+v", four)
	}
	if got := tile(0, 100, 40); got != nil {
		t.Errorf("tile(0) = %+v, want nil", got)
	}
}

// Odd sizes must still tile exactly rather than losing a column to rounding.
func TestTileHandlesOddSizes(t *testing.T) {
	for _, wh := range [][2]int{{101, 41}, {83, 27}, {37, 13}} {
		for n := 1; n <= 6; n++ {
			area := 0
			for _, r := range tile(n, wh[0], wh[1]) {
				area += r.w * r.h
			}
			if area != wh[0]*wh[1] {
				t.Errorf("tile(%d, %d, %d) covered %d, want %d", n, wh[0], wh[1], area, wh[0]*wh[1])
			}
		}
	}
}

func overlap(a, b rect) bool {
	return a.x < b.x+b.w && b.x < a.x+a.w && a.y < b.y+b.h && b.y < a.y+a.h
}
