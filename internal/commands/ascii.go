package commands

import (
	"fmt"
	"math"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/be-sandaa/coucou/internal/settings"
)

//#region Tokens

// ansiColour turns every coloured block back into plain monospace in one line. Discord's ```ansi
// fence is undocumented client behaviour — it appears nowhere in the developer docs — so where it
// is unsupported the escapes can surface as literal text rather than degrading to grey. Every
// block here is drawn to read with no colour at all; flip this if the wild disagrees. See DESIGN.md.
const ansiColour = true

// Discord's ansi fence understands the eight SGR foreground codes and nothing else, so each embed
// colour picks its nearest. There is no orange in eight, which is why brand and board share yellow.
const (
	sgrReset = "\x1b[0m"
	sgrWarm  = "\x1b[0;33m" // colBrand and colBoard
	sgrMuted = "\x1b[0;30m" // colMuted, and every empty track
)

// Widths are the whole layout. 40 columns is the phone budget from DESIGN.md: code blocks do not
// wrap on Discord, they scroll sideways, and a readout you have to drag is not a readout.
const (
	blockWidth    = 40
	barWidth      = 14
	labelWidth    = 8
	boardBarWidth = 12
	dayHours      = 24
)

//#endregion

//#region Primitives

// runs renders cells as contiguous coloured spans rather than one escape per character. Where ansi
// is not supported every escape shows as text, so a two-run bar wrecks a line and a 24-run one
// wrecks the embed.
func runs(cells []bool, fill, track string, lit bool) string {
	var sb strings.Builder
	for i := 0; i < len(cells); {
		j := i
		for j < len(cells) && cells[j] == cells[i] {
			j++
		}
		ch, sgr := track, sgrMuted
		if cells[i] {
			ch, sgr = fill, sgrWarm
		}
		run := strings.Repeat(ch, j-i)
		if lit && ansiColour {
			run = sgr + run + sgrReset
		}
		sb.WriteString(run)
		i = j
	}
	return sb.String()
}

// fill is how many of w cells frac claims. A non-zero fraction always claims one: an empty bar for
// a 1% chance reads as "off", which is the one thing that number does not say.
func fill(frac float64, w int) []bool {
	var n int
	switch {
	case math.IsNaN(frac) || frac <= 0:
		n = 0
	case frac >= 1:
		n = w
	default:
		if n = int(math.Round(frac * float64(w))); n == 0 {
			n = 1
		}
	}
	cells := make([]bool, w)
	for i := range n {
		cells[i] = true
	}
	return cells
}

var ansiRE = regexp.MustCompile("\x1b\\[[0-9;]*m")

// cols is the width a line actually occupies: escapes are zero-width, so anything that measures a
// drawn line has to take them off first or every coloured row looks three times too long.
func cols(s string) int { return utf8.RuneCountInString(ansiRE.ReplaceAllString(s, "")) }

// frac is a/b guarded against the empty denominator, which is the normal state of a fresh server
// rather than an edge case.
func frac(a, b int) float64 {
	if b <= 0 {
		return 0
	}
	return float64(a) / float64(b)
}

// bar is the plain bar, for an inline code span. Only a fenced ansi block interprets escapes, so a
// board row — which sits beside a live mention and cannot be fenced — uses this one.
func bar(frac float64, w int) string { return runs(fill(frac, w), "█", "░", false) }

// litBar is bar for inside a fence, where colour survives.
func litBar(frac float64, w int) string { return runs(fill(frac, w), "█", "░", true) }

// block fences lines as a code block, which is the only thing on Discord that buys monospace — and
// monospace is the only thing that makes a column a column.
func block(lines ...string) string {
	lang := ""
	if ansiColour {
		lang = "ansi"
	}
	return "```" + lang + "\n" + strings.Join(lines, "\n") + "\n```"
}

//#endregion

//#region Components

// meter is one readout line: label, bar, value. Three fixed columns, so a stack of them scans down
// the left edge instead of being read one at a time. The leading space, the label, the bar and two
// separators spend 25 of the 40 columns, which leaves the value 15 — see TestBlocksFitThePhone.
func meter(label string, frac float64, value string) string {
	return fmt.Sprintf(" %-*s %s %s", labelWidth, label, litBar(frac, barWidth), value)
}

// panel frames a stack of meters, sized to the widest of them. A fixed frame is either strangling
// the content or drawing a box of empty air beside it, and the meters are what set the width.
//
// The frame is decoration and carries nothing: it is the first thing a narrow client mangles, and
// nothing may be lost when it does.
func panel(title string, lines ...string) string {
	head := "┌ " + title + " "
	w := cols(head) + 1
	for _, l := range lines {
		w = max(w, cols(l)+1)
	}
	out := make([]string, 0, len(lines)+2)
	out = append(out, head+strings.Repeat("─", w-cols(head))+"┐")
	out = append(out, lines...)
	out = append(out, "└"+strings.Repeat("─", w-1)+"┘")
	return block(out...)
}

// hourStrip draws the whole day, one cell an hour, under a ruler. The shape of a quiet window says
// more than its numbers do: a wrap past midnight is obvious here and arithmetic in "22:00–07:00".
func hourStrip(from, to int) string {
	cells := make([]bool, dayHours)
	for h := range dayHours {
		cells[h] = settings.QuietAt(from, to, h)
	}
	return block(" "+ruler(), " "+runs(cells, "█", "░", true))
}

// boardRow is the one line that has to be monospace and live at once: the bar and count align
// inside an inline code span, and the label stays outside it because a mention, a channel link or
// a timestamp does not resolve inside a span at all.
func boardRow(i int, frac float64, n, countWidth int, label string) string {
	return fmt.Sprintf("%s `%s %*d` %s", rank(i), bar(frac, boardBarWidth), countWidth, n, label)
}

//#endregion

//#region Mascots

// The two places DESIGN.md allows decoration: a wordmark on the command that explains the bot, and
// a speaker on the one that makes the noise.

// wordmark letter-spaces the bot's name in capitals over an underline of the same width. The name
// is a nickname any server admin can set, so backticks go (one would close the fence) and the
// spaced-out letters are clipped to the budget after the two-column indent.
func wordmark(name string) string {
	letters := strings.Join(strings.Split(strings.ToUpper(strings.ReplaceAll(name, "`", "")), ""), " ")
	letters = truncate(letters, blockWidth-3) // -2 for the indent, -1 for the ellipsis truncate adds
	return "```\n" +
		"  " + letters + "\n" +
		"  " + strings.Repeat("-", cols(letters)) + "\n" +
		"```"
}

// nowPlaying strips backticks from the name — it is a filename off disk, and one containing a
// backtick would close the fence and spill the rest of the embed as markdown — then clips it to
// what is left of the 40-column budget after the speaker.
func nowPlaying(sound string) string {
	const speaker = " |   |)))  "
	name := truncate(strings.ReplaceAll(sound, "`", ""), blockWidth-len(speaker)-1) // -1 for the ellipsis truncate adds
	return "```\n" +
		"  .-.\n" +
		speaker + name + "\n" +
		"  '-'\n" +
		"```"
}

//#endregion
