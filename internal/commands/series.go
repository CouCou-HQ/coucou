package commands

import (
	"time"
)

// A series arrives as UTC hours with something in them; a report is drawn in a server's own zone,
// with the empty days left in. This is where one becomes the other.

// calendar is n local calendar days ending today: start is the first one's midnight in loc. Calendar
// days rather than 24-hour spans, so a day that loses an hour to DST is still one column.
type calendar struct {
	loc   *time.Location
	start time.Time
	n     int
}

func lastDays(now time.Time, loc *time.Location, n int) calendar {
	y, m, d := now.In(loc).Date()
	return calendar{loc: loc, start: time.Date(y, m, d-(n-1), 0, 0, 0, 0, loc), n: n}
}

// day is t's index in the calendar, or -1 outside it. Days are counted on the calendar, not in hours:
// noon UTC on both dates is always a whole number of days apart, whatever DST did in between.
func (w calendar) day(t time.Time) int {
	y, m, d := t.In(w.loc).Date()
	sy, sm, sd := w.start.Date()
	i := int(time.Date(y, m, d, 12, 0, 0, 0, time.UTC).Sub(time.Date(sy, sm, sd, 12, 0, 0, 0, time.UTC)).Hours() / 24)
	if i < 0 || i >= w.n {
		return -1
	}
	return i
}

// label names day i the way the axis prints it.
func (w calendar) label(i int) string {
	if i == w.n-1 {
		return labelToday
	}
	y, m, d := w.start.Date()
	return time.Date(y, m, d+i, 0, 0, 0, 0, w.loc).Format("Jan 2")
}

// daily sums v over each local day of w.
func daily[T any](w calendar, rows []T, at func(T) time.Time, v func(T) int) []int {
	out := make([]int, w.n)
	for _, r := range rows {
		if i := w.day(at(r)); i >= 0 {
			out[i] += v(r)
		}
	}
	return out
}

// weekHours sums v by local weekday and hour over the rows inside w.
func weekHours[T any](w calendar, rows []T, at func(T) time.Time, v func(T) int) [7][dayHours]int {
	var out [7][dayHours]int
	for _, r := range rows {
		t := at(r)
		if w.day(t) < 0 {
			continue
		}
		l := t.In(w.loc)
		out[l.Weekday()][l.Hour()] += v(r)
	}
	return out
}

// sum adds vals[from:to], clipped to the slice.
func sum(vals []int, from, to int) int {
	n := 0
	for i := max(0, from); i < min(len(vals), to); i++ {
		n += vals[i]
	}
	return n
}

// weeks is the last seven days of vals and the seven before them.
func weeks(vals []int) (cur, prev int) {
	n := len(vals)
	return sum(vals, n-7, n), sum(vals, n-14, n-7)
}

// streak is how many days in a row, ending today or yesterday, have something in them. Yesterday
// counts as still running: the day is not over, and a streak that dies every midnight until the
// first play is a streak nobody ever sees.
func streak(vals []int) int {
	i := len(vals) - 1
	if i >= 0 && vals[i] == 0 {
		i--
	}
	n := 0
	for ; i >= 0 && vals[i] > 0; i-- {
		n++
	}
	return n
}

// busiest is the index of the largest value, the latest on a tie, and -1 when there is none.
func busiest(vals []int) int {
	best, top := -1, 0
	for i, v := range vals {
		if v > 0 && v >= top {
			best, top = i, v
		}
	}
	return best
}

// peakHour is the local hour with the most in it across every weekday, -1 when there is none.
func peakHour(grid [7][dayHours]int) int {
	best, top := -1, 0
	for h := range dayHours {
		n := 0
		for wd := range grid {
			n += grid[wd][h]
		}
		if n > top {
			best, top = h, n
		}
	}
	return best
}
