package commands

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/be-sandaa/coucou/internal/store"
)

const testSpan = "last 7 days"

func testRows() []store.Row {
	return []store.Row{
		{Key: "a", N: 12},
		{Key: "b", N: 7},
		{Key: "c", N: 3},
		{Key: strings.Repeat("x", 100), N: 1},
	}
}

// A board is a description list, never fields: a rank label is as wide as whoever owns it, and the
// span belongs in the footer where a wrap is harmless.
func TestBoardShape(t *testing.T) {
	em := boardEmbed(titleSounds, testSpan, testRows(), func(r store.Row) string { return r.Key })

	if em.Title != titleSounds {
		t.Errorf("title = %q, want the board title alone", em.Title)
	}
	if em.Color != colBoard {
		t.Errorf("colour = %#x, want colBoard", em.Color)
	}
	if em.Footer == nil || em.Footer.Text != testSpan {
		t.Error("the span belongs in the footer, not the title")
	}
	if len(em.Fields) != 0 {
		t.Errorf("got %d fields, want a description list", len(em.Fields))
	}
}

// Medals for the top three, numbers after, and a name too wide to be safe is clipped rather than
// risking the whole embed being rejected.
func TestBoardRows(t *testing.T) {
	rows := testRows()
	em := boardEmbed(titleSounds, testSpan, rows, func(r store.Row) string { return r.Key })

	lines := strings.Split(strings.TrimRight(em.Description, "\n"), "\n")
	if len(lines) != len(rows) {
		t.Fatalf("got %d lines, want %d", len(lines), len(rows))
	}
	// Bar and count live inside one inline code span so they align; the label stays outside it,
	// because a mention does not resolve inside a span.
	if lines[0] != medals[0]+" `"+strings.Repeat("█", boardBarWidth)+" 12` a" {
		t.Errorf("first row = %q", lines[0])
	}
	if !strings.HasPrefix(lines[3], "4. ") {
		t.Errorf("fourth row = %q, want a numbered rank", lines[3])
	}
	if !strings.HasSuffix(lines[3], strings.Repeat("x", 60)+"…") {
		t.Errorf("long name was not clipped: %q", lines[3])
	}
	// The last row is one twelfth of the top one and still has to show something: an empty bar
	// beside a non-zero count reads as a rendering fault.
	if !strings.Contains(lines[3], "`█░") {
		t.Errorf("smallest row drew an empty bar: %q", lines[3])
	}
}

// A board label is clipped by rune count, so the cut never lands inside a character. The offset
// emoji case is the one that mattered: a 60-byte cut of "x" plus emoji splits the fifteenth one and
// hands Discord invalid UTF-8.
func TestTruncate(t *testing.T) {
	unchanged := []struct {
		name string
		in   string
		n    int
	}{
		{"shorter than the limit", "abc", 10},
		{"exactly at the limit", "abcde", 5},
		{"three runes against a limit of three, not six bytes", "ααα", 3},
	}
	for _, tt := range unchanged {
		t.Run(tt.name, func(t *testing.T) {
			if got := truncate(tt.in, tt.n); got != tt.in {
				t.Errorf("truncate(%q, %d) = %q, want it returned untouched", tt.in, tt.n, got)
			}
		})
	}

	clipped := []struct {
		name string
		in   string
		n    int
		want string
	}{
		{"ascii is cut at the limit", "abcdef", 3, "abc…"},
		{"multi-byte is cut on a rune boundary", "ααααα", 3, "ααα…"},
	}
	for _, tt := range clipped {
		t.Run(tt.name, func(t *testing.T) {
			if got := truncate(tt.in, tt.n); got != tt.want {
				t.Errorf("truncate(%q, %d) = %q, want %q", tt.in, tt.n, got, tt.want)
			}
		})
	}

	// The real call site clips at 60, and a guild name of emoji offset by one ASCII character is
	// exactly where a byte cut lands mid-character.
	got := truncate("x"+strings.Repeat("🎉", 70), 60)
	if !utf8.ValidString(got) {
		t.Errorf("truncate produced invalid UTF-8: %q", got)
	}
	if n := utf8.RuneCountInString(strings.TrimSuffix(got, "…")); n != 60 {
		t.Errorf("kept %d runes, want 60", n)
	}
}

// Missing numbers say so in words: an em dash is announced as "dash" by a screen reader, or skipped.
func TestMissingValuesReadAsWords(t *testing.T) {
	if got := pct(nil); got != noneYet {
		t.Errorf("pct(nil) = %q", got)
	}
	if got := orNone(nil); got != noneYet {
		t.Errorf("orNone(nil) = %q", got)
	}
	if got := plural(1, "channel", "channels"); got != "1 channel" {
		t.Errorf("plural(1) = %q", got)
	}
	if got := plural(0, "channel", "channels"); got != "0 channels" {
		t.Errorf("plural(0) = %q", got)
	}
}
