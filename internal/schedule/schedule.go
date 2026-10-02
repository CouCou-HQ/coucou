// Package schedule is a recurring window held in memory: a rule's occurrences, each lasting a fixed
// length. Shared by the opt-outs and /chaos, which both answer "is it on right now" on a hot path.
package schedule

import (
	"fmt"
	"time"

	"github.com/teambition/rrule-go"
)

// Refresh is how often the recurring windows are recomputed. Occurrences are minute-granular at
// their finest and the loop that reads them only ticks every five minutes, so a minute of lag at a
// window edge is below anything anybody can observe.
const Refresh = time.Minute

// Window is one schedule.
//
// From/To is the occurrence around the last refresh, precomputed. Active could walk the rule itself —
// Before(now) and compare, which is what the library is shaped for — but that allocates and
// iterates from DTSTART on a path that runs once per human in every candidate channel on every
// tick. The cached pair is what keeps a lookup a map read under a read lock.
type Window struct {
	Rule     *rrule.RRule
	Length   time.Duration
	From, To time.Time
}

// Parse resolves stored rule text once, so that no parsing happens on the hot path.
func Parse(rule string, length time.Duration, now time.Time) (Window, error) {
	r, err := rrule.StrToRRule(rule)
	if err != nil {
		return Window{}, fmt.Errorf("parse rule: %w", err)
	}
	w := Window{Rule: r, Length: length}
	w.At(now)
	return w, nil
}

func (w Window) Active(now time.Time) bool { return !now.Before(w.From) && now.Before(w.To) }

// At recomputes the occurrence around now. A rule that has not reached its first occurrence yet
// has nothing before now, which rrule reports as the zero time and Active reads as "not in a
// window" — the same answer as a schedule that is simply not running right now.
func (w *Window) At(now time.Time) {
	from := w.Rule.Before(now, true)
	if from.IsZero() {
		w.From, w.To = time.Time{}, time.Time{}
		return
	}
	w.From, w.To = from, from.Add(w.Length)
}
