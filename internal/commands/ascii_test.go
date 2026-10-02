package commands

import (
	"math"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/be-sandaa/coucou/internal/settings"
)

// The width budget is the whole layout decision: code blocks do not wrap on Discord, they scroll
// sideways, and the bot is mostly driven from a phone that is already in the call. Every block the
// bot can emit is checked at its widest, not at a convenient value.
func TestBlocksFitThePhone(t *testing.T) {
	longest := strings.Repeat("w", 80) // a sound name far past the budget
	blocks := map[string]string{
		"status panel": panel("status",
			meter("chance", 1, "100% /5min"),
			meter("suspense", 1, "≤20s"),
			meter("earshot", 1, "500/500 rooms"), // Discord caps a guild at 500 channels
		),
		"hour strip":   hourStrip(22, 7),
		"help mascot":  mascotHelp,
		"now playing":  nowPlaying(longest),
		"single meter": block(meter("failures", 1, "100%")),
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

// A fence closed early spills the rest of the embed as raw markdown, and sound names come off the
// filesystem rather than from anything that validated them.
func TestNowPlayingCannotCloseTheFence(t *testing.T) {
	out := nowPlaying("evil```name")
	if strings.Count(out, "```") != 2 {
		t.Errorf("a backticked name broke the fence: %q", out)
	}
}

func TestBarFill(t *testing.T) {
	tests := []struct {
		name string
		frac float64
		want int
	}{
		{"empty", 0, 0},
		{"negative is empty", -1, 0},
		{"NaN is empty", math.NaN(), 0},
		// The one that matters: a 1% chance drawing no cell reads as "off", which is the single
		// thing the number beside it does not say.
		{"a sliver still shows", 0.001, 1},
		{"half", 0.5, 7},
		{"full", 1, 14},
		{"over one is clamped", 2, 14},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var got int
			for _, c := range fill(tt.frac, barWidth) {
				if c {
					got++
				}
			}
			if got != tt.want {
				t.Errorf("fill(%v) = %d cells, want %d", tt.frac, got, tt.want)
			}
		})
	}
}

// The strip draws the same rule the bot schedules by, so a window that wraps past midnight has to
// come out as two runs at the ends rather than one in the middle.
func TestHourStripWrapsMidnight(t *testing.T) {
	cells := ansiRE.ReplaceAllString(strings.Split(hourStrip(22, 7), "\n")[2], "")
	cells = strings.TrimSpace(cells)
	if n := utf8.RuneCountInString(cells); n != dayHours {
		t.Fatalf("strip is %d cells, want %d", n, dayHours)
	}
	for h, c := range []rune(cells) {
		if want := settings.QuietAt(22, 7, h); (c == '█') != want {
			t.Errorf("hour %d drawn %q, QuietAt says %v", h, c, want)
		}
	}
}

// Bars are what a board is read down; they only work if every one of them starts and ends in the
// same column whatever the counts are.
func TestBoardRowsAlign(t *testing.T) {
	for _, n := range []int{1234, 7, 0} {
		row := boardRow(0, frac(n, 1234), n, 4, "label")
		span := strings.Split(row, "`")
		if len(span) != 3 {
			t.Fatalf("row has no single code span: %q", row)
		}
		if got := utf8.RuneCountInString(span[1]); got != boardBarWidth+1+4 {
			t.Errorf("span for n=%d is %d wide, want %d", n, got, boardBarWidth+5)
		}
	}
}
