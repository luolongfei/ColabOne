package main

import (
	"context"
	"testing"

	"github.com/egoist/mygo"
)

func TestClampLogDrawerWidth(t *testing.T) {
	cases := []struct {
		in   int
		want int
	}{
		{0, defaultLogDrawerWidth},
		{-100, defaultLogDrawerWidth},
		{180, minLogDrawerWidth},
		{320, 320},
		{420, 420},
		{640, 640},
		{999, maxLogDrawerWidth},
	}
	for _, tc := range cases {
		if got := clampColabOneLogDrawerWidth(tc.in); got != tc.want {
			t.Fatalf("clampColabOneLogDrawerWidth(%d) = %d, want %d", tc.in, got, tc.want)
		}
	}
}

func TestPlanLogWindowBoundsPlacesFlushRightWhenRoomAvailable(t *testing.T) {
	mainBounds := mygo.Rectangle{X: 120, Y: 80, Width: 1200, Height: 820}
	work := mygo.Rectangle{X: 0, Y: 0, Width: 1920, Height: 1040}

	got := planColabOneLogWindowBounds(mainBounds, work, 420)
	want := mygo.Rectangle{X: 1320, Y: 80, Width: 420, Height: 820}
	if got != want {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}

func TestPlanLogWindowBoundsFitsRemainingRightSpaceWithoutOverlapping(t *testing.T) {
	// Centered 1200px window on a 1920px display leaves 360px on the right (>= 320px min).
	mainBounds := mygo.Rectangle{X: 360, Y: 100, Width: 1200, Height: 820}
	work := mygo.Rectangle{X: 0, Y: 0, Width: 1920, Height: 1040}

	got := planColabOneLogWindowBounds(mainBounds, work, 420)
	want := mygo.Rectangle{X: 1560, Y: 100, Width: 360, Height: 820}
	if got != want {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}

func TestPlanLogWindowBoundsPlacesFlushLeftWhenMainIsAtRightEdge(t *testing.T) {
	// Main window parked on the right edge leaves 720px on the left and 0px on the right.
	mainBounds := mygo.Rectangle{X: 720, Y: 60, Width: 1200, Height: 820}
	work := mygo.Rectangle{X: 0, Y: 0, Width: 1920, Height: 1040}

	got := planColabOneLogWindowBounds(mainBounds, work, 420)
	want := mygo.Rectangle{X: 300, Y: 60, Width: 420, Height: 820}
	if got != want {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}

func TestPlanLogWindowBoundsMaximizedFallsBackToVisibleRightEdge(t *testing.T) {
	work := mygo.Rectangle{X: 0, Y: 0, Width: 1920, Height: 1040}
	mainBounds := mygo.Rectangle{X: 0, Y: 0, Width: 1920, Height: 1040}

	got := planColabOneLogWindowBounds(mainBounds, work, 420)
	want := mygo.Rectangle{X: 1500, Y: 0, Width: 420, Height: 1040}
	if got != want {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}

func TestPlanLogWindowBoundsMultiMonitorOffsetAndVerticalClamp(t *testing.T) {
	work := mygo.Rectangle{X: 1920, Y: 40, Width: 1920, Height: 1000}
	mainBounds := mygo.Rectangle{X: 2020, Y: 300, Width: 1200, Height: 820}

	got := planColabOneLogWindowBounds(mainBounds, work, 420)
	// Vertical Y clamped so bottom stays at 40+1000=1040 -> Y = 220.
	want := mygo.Rectangle{X: 3220, Y: 220, Width: 420, Height: 820}
	if got != want {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}

func TestLogDrawerRPCWithoutWindowIsSafe(t *testing.T) {
	d := ColabOneDesktop{}
	if got := d.GetLogDrawerState(); got.Open || !got.Standalone {
		t.Fatalf("unexpected initial state: %+v", got)
	}
	st := d.SetLogDrawer(context.Background(), true, 420)
	if !st.Open || st.Standalone {
		t.Fatalf("unexpected state without window: %+v", st)
	}
	st = d.SetLogDrawer(context.Background(), false, 420)
	if st.Open || st.Standalone {
		t.Fatalf("unexpected closed state without window: %+v", st)
	}
}
