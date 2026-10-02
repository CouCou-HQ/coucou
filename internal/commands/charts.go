package commands

import (
	"fmt"
	"math"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/disgoorg/disgo/discord"
)

// The charts /stats draws. Same rules as ascii.go: monospace inside a fence, 40 columns, colour as
// confirmation only. Every chart here carries its magnitude in the glyph — a taller column, a
// denser shade, a different fill — so it reads with the ansi escapes gone. See DESIGN.md.

//#region Tokens

// eighths are the column heights between empty and full, one per eighth of a cell. A cell is the
// resolution a column chart has, and eight steps per cell is what makes a four-row chart show a
// difference of three plays out of sixty.
var eighths = []rune(" ▁▂▃▄▅▆▇█")

// shades are the heatmap's steps, one hue from light to dark. Zero is its own glyph rather than the
// lightest shade: "never" and "hardly ever" are different answers to "when does it strike".
var shades = []rune("░▒▓█")

const (
	labelPlay   = "/play" // how a /play request is labelled wherever one is counted
	labelLoop   = "loop"
	labelToday  = "today"
	shadeNone   = '·'
	chartHeight = 4  // rows; 32 steps, and a phone's worth of vertical space
	trendDays   = 30 // one column a day
	axisWidth   = 4  // the widest y label the column chart prints, "9999" or "9.9k"
)

// splitFills tell the segments of a split bar apart by texture, so identity never rests on colour.
var splitFills = []string{"█", "▓", "▒", "░"}

//#endregion

//#region Helpers

// compact keeps a count inside the four columns the y axis gives it. Past 9999 it is thousands to
// one decimal and past 99k whole thousands: an axis label is a scale, not a total.
func compact(n int) string {
	switch {
	case n < 10000:
		return fmt.Sprint(n)
	case n < 100000:
		return fmt.Sprintf("%.0fk", math.Floor(float64(n)/1000))
	}
	return fmt.Sprintf("%dk", min(n/1000, 999))
}

// lit colours every run of glyphs in s other than a space; a space is the chart's background and
// stays uncoloured, so a column chart costs one escape per column group rather than one per cell.
func lit(s string, sgr string) string {
	if !ansiColour {
		return s
	}
	var sb strings.Builder
	rs := []rune(s)
	for i := 0; i < len(rs); {
		j := i
		for j < len(rs) && (rs[j] == ' ') == (rs[i] == ' ') {
			j++
		}
		if rs[i] == ' ' {
			sb.WriteString(string(rs[i:j]))
		} else {
			sb.WriteString(sgr + string(rs[i:j]) + sgrReset)
		}
		i = j
	}
	return sb.String()
}

const (
	trendNothing  = "nothing either week"
	trendFromZero = "up from nothing the week before"
	trendSame     = "same as the week before"
	trendHair     = "down a hair on the week before"
)

// trend compares this period with the one before in words: an arrow is announced as "black
// up-pointing triangle", and a percentage off a zero base is not a number anyone can use.
func trend(cur, prev int) string {
	switch {
	case prev == 0 && cur == 0:
		return trendNothing
	case prev == 0:
		return trendFromZero
	case cur == prev:
		return trendSame
	}
	d := (cur - prev) * 100 / prev
	if d > 0 {
		return fmt.Sprintf("up %d%% on the week before", d)
	}
	if d == 0 {
		return trendHair
	}
	return fmt.Sprintf("down %d%% on the week before", -d)
}

//#endregion

//#region Charts

// columns is a column chart of vals, oldest first, chartHeight rows tall on a labelled axis, with
// the first and last day named underneath. The scale is the largest value, printed at the top of
// the axis; zero is the baseline. One axis, always: two scales on one chart is two charts.
func columns(vals []int, first, last string) []string {
	top := 0
	for _, v := range vals {
		top = max(top, v)
	}
	// Heights in eighths of a cell. A non-zero value is at least one eighth: a day with a play in it
	// drawn the same as a day without is the one thing this chart must not say.
	steps := chartHeight * 8
	h := make([]int, len(vals))
	for i, v := range vals {
		if v > 0 && top > 0 {
			h[i] = max(1, int(math.Round(float64(v)*float64(steps)/float64(top))))
		}
	}
	out := make([]string, 0, chartHeight+2)
	for row := chartHeight - 1; row >= 0; row-- {
		cells := make([]rune, len(vals))
		for i, hi := range h {
			cells[i] = eighths[min(8, max(0, hi-row*8))]
		}
		label := ""
		switch row {
		case chartHeight - 1:
			label = compact(top)
		case 0:
			label = "0"
		}
		out = append(out, fmt.Sprintf(" %*s ┤%s", axisWidth, label, strings.TrimRight(lit(string(cells), sgrWarm), " ")))
	}
	pad := strings.Repeat(" ", axisWidth+2)
	out = append(out, pad+"└"+strings.Repeat("─", len(vals)))
	gap := max(1, len(vals)+1-len([]rune(first))-len([]rune(last)))
	out = append(out, pad+first+strings.Repeat(" ", gap)+last)
	return out
}

// spark is columns squeezed into one row, for where a whole chart would be too much.
func spark(vals []int) string {
	top := 0
	for _, v := range vals {
		top = max(top, v)
	}
	cells := make([]rune, len(vals))
	for i, v := range vals {
		switch {
		case v == 0 || top == 0:
			cells[i] = shadeNone
		default:
			cells[i] = eighths[max(1, int(math.Round(float64(v)*8/float64(top))))]
		}
	}
	return lit(string(cells), sgrWarm)
}

// weekdays is the heatmap's row order: Monday first, as a week reads on most of the planet.
var weekdays = []time.Weekday{time.Monday, time.Tuesday, time.Wednesday, time.Thursday, time.Friday, time.Saturday, time.Sunday}

// heatmap is a week by hour grid: a row a weekday, a column an hour, a shade the count against the
// busiest cell. The shades are quartiles of that cell, so the darkest is "as bad as it gets" and not
// an absolute that a quiet server would never reach.
func heatmap(grid [7][dayHours]int) []string {
	top := 0
	for _, row := range grid {
		for _, v := range row {
			top = max(top, v)
		}
	}
	out := []string{"    " + ruler()}
	for _, wd := range weekdays {
		cells := make([]rune, dayHours)
		for h, v := range grid[wd] {
			cells[h] = shadeNone
			if v > 0 {
				cells[h] = shades[min(len(shades)-1, (v*len(shades)-1)/top)]
			}
		}
		out = append(out, " "+wd.String()[:2]+" "+shadeRuns(cells))
	}
	return append(out, "    "+shadeRuns([]rune{shadeNone})+" never  "+shadeRuns(shades)+" most")
}

// shadeRuns colours a heatmap row: the "never" glyph muted, every shade warm. Two escapes per run
// rather than per cell, for the same reason runs exists.
func shadeRuns(cells []rune) string {
	if !ansiColour {
		return string(cells)
	}
	var sb strings.Builder
	for i := 0; i < len(cells); {
		j := i
		for j < len(cells) && (cells[j] == shadeNone) == (cells[i] == shadeNone) {
			j++
		}
		sgr := sgrWarm
		if cells[i] == shadeNone {
			sgr = sgrMuted
		}
		sb.WriteString(sgr + string(cells[i:j]) + sgrReset)
		i = j
	}
	return sb.String()
}

// ruler is the hour strip's scale, shared so the heatmap and quiet hours read against one ruler.
func ruler() string {
	r := []rune(strings.Repeat(" ", dayHours))
	for _, h := range []int{0, 6, 12, 18} {
		for i, c := range fmt.Sprint(h) {
			r[h+i] = c
		}
	}
	return strings.TrimRight(string(r), " ")
}

// part is one segment of a split bar.
type part struct {
	label string
	n     int
}

// split is one bar cut into its parts, each in its own fill, with a legend row per part giving the
// share in words. Parts with nothing in them are left out of both, and every part that has
// something gets at least one cell, for the reason fill gives.
func split(parts []part, w int) []string {
	total := 0
	var shown []part
	for _, p := range parts {
		if p.n > 0 {
			total += p.n
			shown = append(shown, p)
		}
	}
	if total == 0 {
		return nil
	}
	cells := make([]int, len(shown))
	used := 0
	for i, p := range shown {
		cells[i] = max(1, int(math.Round(float64(p.n)*float64(w)/float64(total))))
		used += cells[i]
	}
	// Rounding and the one-cell floor can overshoot or undershoot; the biggest part absorbs it.
	big := 0
	for i := range shown {
		if cells[i] > cells[big] {
			big = i
		}
	}
	cells[big] = max(1, cells[big]+w-used)

	var bar strings.Builder
	legend := make([]string, 0, len(shown))
	for i, p := range shown {
		f := splitFills[i%len(splitFills)]
		bar.WriteString(strings.Repeat(f, cells[i]))
		legend = append(legend, fmt.Sprintf(" %s %-*s %3d%% · %d", f, labelWidth, p.label, p.n*100/total, p.n))
	}
	return append([]string{" " + lit(bar.String(), sgrWarm)}, legend...)
}

// titled heads a block with a caption line, the unframed cousin of panel: a chart's own axes are
// frame enough, and a box around one is air the phone pays for. It ends its own line, so titled
// blocks stack without running together. The caption is clipped to the budget: it carries a zone
// name, and a few of those are longer than the chart under them.
func titled(title string, lines ...string) string {
	return block(append([]string{" " + truncate(title, blockWidth-2)}, lines...)...) + "\n"
}

//#endregion

//#region Limits

// Discord's embed limits, in characters: a description on its own, and everything in one message
// together. Past either the whole reply is rejected, not clipped.
const (
	descLimit    = 4096
	messageLimit = 6000
	splitWidth   = blockWidth - 2
)

// fit keeps a reply inside Discord's limits by giving up colour, which is the one thing a chart
// here can lose and still say everything: the escapes are most of a busy heatmap's length. It
// strips the biggest description first and stops as soon as the reply fits.
func fit(em ...discord.Embed) []discord.Embed {
	for i := range em {
		if utf8.RuneCountInString(em[i].Description) > descLimit {
			em[i].Description = plainText(em[i].Description)
		}
	}
	for {
		total := 0
		for _, e := range em {
			total += embedSize(e)
		}
		big := mostColoured(em)
		if total <= messageLimit || big < 0 {
			return em
		}
		em[big].Description = plainText(em[big].Description)
	}
}

func plainText(s string) string { return ansiRE.ReplaceAllString(s, "") }

// embedSize is what Discord counts towards a message's total.
func embedSize(e discord.Embed) int {
	n := utf8.RuneCountInString(e.Title) + utf8.RuneCountInString(e.Description)
	for _, f := range e.Fields {
		n += utf8.RuneCountInString(f.Name) + utf8.RuneCountInString(f.Value)
	}
	if e.Footer != nil {
		n += utf8.RuneCountInString(e.Footer.Text)
	}
	return n
}

// mostColoured is the embed with the longest description that still has colour to give up, -1
// when none has.
func mostColoured(em []discord.Embed) int {
	best, size := -1, 0
	for i, e := range em {
		if n := len(e.Description); ansiRE.MatchString(e.Description) && n > size {
			best, size = i, n
		}
	}
	return best
}

//#endregion
