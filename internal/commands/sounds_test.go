package commands

import (
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"
)

func soundNames(n int, name func(i int) string) []string {
	out := make([]string, n)
	for i := range out {
		out[i] = name(i)
	}
	return out
}

func short(i int) string { return fmt.Sprintf("s%03d", i) }

const firstShort = "`S000`"

func noMarks(string) string { return "" }

func allMarks(string) string { return bothMarks }

// The markers sit inside the span after the name, and a clipped name keeps them.
func TestClippedKeepsTheMarkers(t *testing.T) {
	tests := []struct {
		name, want string
	}{
		{"ufufu", "`Ufufu" + bothMarks + "`"},
		{strings.Repeat("w", 80), "`W" + strings.Repeat("w", soundsNameMax-1) + "…" + bothMarks + "`"},
	}
	for _, tt := range tests {
		if got := clipped(tt.name, allMarks); got != tt.want {
			t.Errorf("clipped(%q) = %q, want %q", tt.name, got, tt.want)
		}
	}
}

func TestSoundsPage(t *testing.T) {
	tests := []struct {
		name   string
		n      int
		page   int
		colour int
		lines  int
		first  string
		footer string
	}{
		{"one page", 3, 1, colBrand, 3, firstShort, "3 sounds"},
		{"one sound", 1, 1, colBrand, 1, firstShort, "1 sound"},
		{"exact multiple, last page", 2 * soundsPerPage, 2, colBrand, soundsPerPage, "`S050`", "100 sounds · page 2 of 2"},
		{"first page of three", 2*soundsPerPage + 1, 1, colBrand, soundsPerPage, firstShort, "101 sounds · page 1 of 3"},
		{"last page holds the remainder", 2*soundsPerPage + 1, 3, colBrand, 1, "`S100`", "101 sounds · page 3 of 3"},
		{"past the end", 2 * soundsPerPage, 3, colBad, 0, "", ""},
		{"nothing playable", 0, 1, colMuted, 0, "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			em := soundsPage(soundNames(tt.n, short), noMarks, tt.page)
			if em.Color != tt.colour {
				t.Errorf("colour = %#x, want %#x", em.Color, tt.colour)
			}
			if tt.lines == 0 {
				if em.Description == "" {
					t.Error("answered with an empty description; it has to say why in words")
				}
				return
			}
			lines := strings.Split(strings.TrimRight(em.Description, "\n"), "\n")
			if len(lines) != tt.lines || lines[0] != tt.first {
				t.Errorf("got %d lines starting %q, want %d starting %q", len(lines), lines[0], tt.lines, tt.first)
			}
			if em.Footer == nil || em.Footer.Text != tt.footer {
				t.Errorf("footer = %+v, want %q", em.Footer, tt.footer)
			}
		})
	}
}

// File names can run to 255 bytes; a full page of them still has to fit one embed.
func TestSoundsPageFitsDiscord(t *testing.T) {
	long := func(i int) string { return fmt.Sprintf("%03d_%s", i, strings.Repeat("ü", 120)) }
	em := soundsPage(soundNames(soundsPerPage, long), allMarks, 1)
	if n := utf8.RuneCountInString(em.Description); n > embedDescriptionLimit {
		t.Errorf("description is %d runes, over Discord's %d", n, embedDescriptionLimit)
	}
	if n := utf8.RuneCountInString(em.Title + em.Description + em.Footer.Text); n > 6000 {
		t.Errorf("embed is %d runes, over Discord's 6000", n)
	}
}
