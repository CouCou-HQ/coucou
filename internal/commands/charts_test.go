package commands

import (
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/be-sandaa/coucou/internal/store"
)

func plain(s string) string { return plainText(s) }

// Every chart at its widest: the most columns, the longest labels, the largest counts.
func TestChartsFitThePhone(t *testing.T) {
	big := make([]int, trendDays)
	var grid [7][dayHours]int
	for i := range big {
		big[i] = 999999 - i
	}
	for wd := range grid {
		for h := range grid[wd] {
			grid[wd][h] = (wd*dayHours + h) % 5
		}
	}
	blocks := map[string]string{
		"columns": titled("plays a day", columns(big, "Sep 30", "today")...),
		"heatmap": titled("when it strikes · America/Argentina/ComodRivadavia", heatmap(grid)...),
		"split": titled("how visits began and ended · 30 days",
			split([]part{{"loop", 999999}, {labelPlay, 999999}, {optEncore, 999999}}, splitWidth)...),
		"spark": block(" 30 days  " + spark(big)),
	}
	for name, b := range blocks {
		t.Run(name, func(t *testing.T) {
			for _, line := range strings.Split(b, "\n") {
				if n := cols(line); n > blockWidth {
					t.Errorf("%d columns, over the %d budget: %q", n, blockWidth, line)
				}
			}
		})
	}
}

func TestColumns(t *testing.T) {
	got := columns([]int{0, 1, 64, 32}, "a", "b")
	if len(got) != chartHeight+2 {
		t.Fatalf("got %d lines, want %d", len(got), chartHeight+2)
	}
	top, bottom := plain(got[0]), plain(got[chartHeight-1])
	if !strings.HasPrefix(top, "   64 ┤") {
		t.Errorf("the top of the axis is the scale: %q", top)
	}
	// Day 0 is empty, day 1 is 1/64 of the top and still shows, day 2 is full, day 3 is half.
	if want := "    0 ┤ ▁██"; bottom != want {
		t.Errorf("bottom row = %q, want %q", bottom, want)
	}
	if want := "   64 ┤  █"; top != want {
		t.Errorf("top row = %q, want %q", top, want)
	}
	if !strings.HasSuffix(got[len(got)-1], "a   b") {
		t.Errorf("axis labels = %q", got[len(got)-1])
	}
}

func TestColumnsOnNothing(t *testing.T) {
	for _, l := range columns(make([]int, 3), "a", "b")[:chartHeight] {
		if strings.ContainsAny(plain(l), string(eighths[1:])) {
			t.Errorf("an empty month drew a column: %q", l)
		}
	}
}

func TestSpark(t *testing.T) {
	if got, want := plain(spark([]int{0, 1, 8, 4})), "·▁█▄"; got != want {
		t.Errorf("spark = %q, want %q", got, want)
	}
}

func TestHeatmap(t *testing.T) {
	var grid [7][dayHours]int
	grid[time.Monday][0] = 1
	grid[time.Monday][1] = 2
	grid[time.Monday][2] = 3
	grid[time.Monday][3] = 4
	grid[time.Sunday][23] = 4
	got := heatmap(grid)
	if len(got) != 9 {
		t.Fatalf("got %d lines, want ruler, seven days and a legend", len(got))
	}
	if want := " Mo ░▒▓█" + strings.Repeat("·", 20); plain(got[1]) != want {
		t.Errorf("Monday = %q, want %q", plain(got[1]), want)
	}
	if !strings.HasPrefix(plain(got[7]), " Su ") || !strings.HasSuffix(plain(got[7]), "█") {
		t.Errorf("Sunday is the last row and its last hour is the busiest: %q", plain(got[7]))
	}
}

func TestSplit(t *testing.T) {
	got := split([]part{{"a", 90}, {"none", 0}, {"b", 1}}, 20)
	if len(got) != 3 {
		t.Fatalf("got %d lines, want the bar and a row per part with something in it: %q", len(got), got)
	}
	bar := plain(got[0])
	if n := utf8.RuneCountInString(bar); n != 21 {
		t.Errorf("bar is %d columns, want the width plus its margin: %q", n, bar)
	}
	if strings.Count(bar, "▓") != 1 {
		t.Errorf("the 1%% part should still get one cell: %q", bar)
	}
	if !strings.Contains(got[1], "98%") || !strings.Contains(got[2], "1%") {
		t.Errorf("legend = %q", got[1:])
	}
	if split([]part{{"a", 0}}, 20) != nil {
		t.Error("nothing to split drew a bar")
	}
}

func TestTrend(t *testing.T) {
	tests := []struct {
		cur, prev int
		want      string
	}{
		{0, 0, "nothing either week"},
		{5, 0, "up from nothing the week before"},
		{5, 5, "same as the week before"},
		{15, 10, "up 50% on the week before"},
		{5, 10, "down 50% on the week before"},
		{199, 200, "down a hair on the week before"},
	}
	for _, tt := range tests {
		if got := trend(tt.cur, tt.prev); got != tt.want {
			t.Errorf("trend(%d, %d) = %q, want %q", tt.cur, tt.prev, got, tt.want)
		}
	}
}

func TestCompact(t *testing.T) {
	const ceiling = "999k"
	for n, want := range map[int]string{0: "0", 9999: "9999", 12345: "12k", 999999: ceiling, 5000000: ceiling} {
		if got := compact(n); got != want {
			t.Errorf("compact(%d) = %q, want %q", n, got, want)
		}
	}
}

// A reply over Discord's limits is rejected whole, so fit gives up colour until it is under them.
func TestFit(t *testing.T) {
	coloured := strings.Repeat(sgrWarm+"█"+sgrReset, 400) // 400 columns, ~6000 characters
	em := fit(info("a", coloured), info("b", "short"))
	if ansiRE.MatchString(em[0].Description) {
		t.Error("a description over the limit kept its colour")
	}
	if em[0].Description != strings.Repeat("█", 400) {
		t.Error("fit changed more than the colour")
	}

	small := sgrWarm + "█" + sgrReset
	if em := fit(info("a", small)); em[0].Description != small {
		t.Error("a reply inside the limits lost its colour")
	}

	// Each under the description limit, together over the message one: only the bigger gives way.
	half := strings.Repeat(sgrWarm+"█"+sgrReset, 300)
	quarter := strings.Repeat(sgrWarm+"█"+sgrReset, 200)
	em = fit(info("a", half), info("b", quarter))
	if ansiRE.MatchString(em[0].Description) || !ansiRE.MatchString(em[1].Description) {
		t.Error("fit should strip the biggest description first and stop once it fits")
	}
}

func TestPlaysReportOnAQuietMonth(t *testing.T) {
	w := lastDays(time.Now(), time.UTC, trendDays)
	out := playsReport(w, nil, "UTC")
	if !strings.Contains(out, "0 plays** this week, nothing either week.") {
		t.Errorf("a quiet month still says so in words: %q", out)
	}
	if strings.Contains(out, "how visits") {
		t.Error("a month with nothing in it drew a split of nothing")
	}
	if strings.Count(out, "```") != 4 {
		t.Errorf("want two fenced blocks, got %q", out)
	}
}

func TestPlaysReportCountsOnlyTheMonth(t *testing.T) {
	now := time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC)
	w := lastDays(now, time.UTC, trendDays)
	hours := []store.PlayHour{
		{Hour: w.start.Add(-time.Hour), Plays: 50, Loops: 50}, // the day before the month
		{Hour: now.Add(-time.Hour), Plays: 3, Loops: 2, Commands: 1, FakeOuts: 1},
	}
	out := plain(playsReport(w, hours, "UTC"))
	if !strings.Contains(out, "**3 plays** this week") {
		t.Errorf("week = %q", out)
	}
	if !strings.Contains(out, "loop      66% · 2") || !strings.Contains(out, "fake-out  25% · 1") {
		t.Errorf("split counted outside the month: %q", out)
	}
}
