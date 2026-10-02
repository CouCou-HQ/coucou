package commands

import (
	"slices"
	"testing"
	"time"

	"github.com/be-sandaa/coucou/internal/store"
)

func brussels(t *testing.T) *time.Location {
	t.Helper()
	loc, err := time.LoadLocation("Europe/Brussels")
	if err != nil {
		t.Skip("no zone database:", err)
	}
	return loc
}

// A UTC hour lands on the local day it belongs to, including the one either side of midnight that
// UTC would put on the wrong date.
func TestDailyIsLocal(t *testing.T) {
	loc := brussels(t)
	now := time.Date(2026, 7, 10, 12, 0, 0, 0, loc)
	w := lastDays(now, loc, 3)
	hours := []store.PlayHour{
		{Hour: time.Date(2026, 7, 7, 21, 0, 0, 0, time.UTC), Plays: 9}, // 23:00 local on the 7th, before the window
		{Hour: time.Date(2026, 7, 7, 22, 0, 0, 0, time.UTC), Plays: 1}, // 00:00 local on the 8th
		{Hour: time.Date(2026, 7, 9, 21, 0, 0, 0, time.UTC), Plays: 2}, // 23:00 local on the 9th
		{Hour: time.Date(2026, 7, 10, 9, 0, 0, 0, time.UTC), Plays: 4},
	}
	if got, want := daily(w, hours, hourAt, playsOf), []int{1, 2, 4}; !slices.Equal(got, want) {
		t.Errorf("daily = %v, want %v", got, want)
	}
	if got := w.label(0); got != "Jul 8" {
		t.Errorf("label(0) = %q", got)
	}
	if got := w.label(2); got != "today" {
		t.Errorf("label(2) = %q", got)
	}
}

// The day the clocks go back has 25 hours and is still one column; the days after it do not shift.
func TestDailyAcrossDST(t *testing.T) {
	loc := brussels(t)
	now := time.Date(2026, 10, 27, 12, 0, 0, 0, loc)
	w := lastDays(now, loc, 4) // 24th to 27th; the 25th is 25 hours long
	var hours []store.PlayHour
	for h := w.start; h.Before(time.Date(2026, 10, 28, 0, 0, 0, 0, loc)); h = h.Add(time.Hour) {
		hours = append(hours, store.PlayHour{Hour: h.UTC(), Plays: 1})
	}
	if got, want := daily(w, hours, hourAt, playsOf), []int{24, 25, 24, 24}; !slices.Equal(got, want) {
		t.Errorf("daily = %v, want %v", got, want)
	}
}

func TestWeekHoursIsLocal(t *testing.T) {
	loc := brussels(t)
	w := lastDays(time.Date(2026, 7, 10, 12, 0, 0, 0, loc), loc, 7)
	// Thursday 9th, 21:00 UTC is 23:00 in Brussels.
	grid := weekHours(w, []store.PlayHour{{Hour: time.Date(2026, 7, 9, 21, 0, 0, 0, time.UTC), Plays: 3}}, hourAt, playsOf)
	if grid[time.Thursday][23] != 3 {
		t.Errorf("the play is not at Thursday 23:00 local: %v", grid[time.Thursday])
	}
	if peakHour(grid) != 23 {
		t.Errorf("peakHour = %d, want 23", peakHour(grid))
	}
	var empty [7][dayHours]int
	if peakHour(empty) != -1 {
		t.Error("an empty grid has a peak")
	}
}

func TestStreak(t *testing.T) {
	tests := []struct {
		name string
		in   []int
		want int
	}{
		{"nothing", []int{0, 0, 0}, 0},
		{"running through today", []int{0, 1, 2, 3}, 3},
		{"today not started yet", []int{1, 1, 1, 0}, 3},
		{"broken yesterday", []int{1, 1, 0, 0}, 0},
		{"no days at all", nil, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := streak(tt.in); got != tt.want {
				t.Errorf("streak(%v) = %d, want %d", tt.in, got, tt.want)
			}
		})
	}
}

func TestWeeksAndBusiest(t *testing.T) {
	vals := make([]int, 30)
	vals[29], vals[23], vals[22], vals[10] = 1, 2, 5, 5
	if cur, prev := weeks(vals); cur != 3 || prev != 5 {
		t.Errorf("weeks = %d, %d; want 3, 5", cur, prev)
	}
	if got := busiest(vals); got != 22 {
		t.Errorf("busiest = %d, want the later of the tie", got)
	}
	if busiest(make([]int, 3)) != -1 {
		t.Error("an empty month has a busiest day")
	}
	if cur, prev := weeks([]int{4}); cur != 4 || prev != 0 {
		t.Errorf("weeks on a short series = %d, %d", cur, prev)
	}
}
