package commands

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/teambition/rrule-go"
)

// A recurring opt-out is stored as an RFC 5545 rule, so both /optout schedule and /optout rrule
// end up here: the guided subcommand writes a rule body the user could have typed themselves and
// hands it to the same builder, which is what keeps the two from meaning different things.

var (
	errNoFreq    = errors.New("a rule needs a FREQ — for example `FREQ=WEEKLY;BYDAY=MO,WE;BYHOUR=20`")
	errTooOften  = errors.New("only daily, weekly, monthly and yearly rules are accepted")
	errNeverRuns = errors.New("that rule never comes round")
)

// weeklySpec is the guided subcommand's rule body. days is already a BYDAY list, because the
// choice values are the BYDAY codes.
func weeklySpec(days string, hour int) string {
	return fmt.Sprintf("FREQ=WEEKLY;BYDAY=%s;BYHOUR=%d;BYMINUTE=0;BYSECOND=0", days, hour)
}

// buildRule turns a rule body and an IANA zone into the rule that gets stored, or the reason it
// cannot be. The returned rule renders with its own DTSTART;TZID line, so the stored text carries
// the zone and nothing else has to remember it.
//
// DTSTART is midnight today in that zone: BYHOUR then decides the time of day, and the walk back
// from now that the refresher does never spans more than the age of the opt-out.
func buildRule(spec, tz string, now time.Time) (*rrule.RRule, error) {
	loc, err := time.LoadLocation(tz)
	if err != nil {
		return nil, fmt.Errorf("`%s` is not a time zone. Try Europe/Brussels", tz)
	}
	spec = strings.TrimPrefix(strings.ToUpper(strings.TrimSpace(spec)), "RRULE:")
	// A body with no FREQ parses without complaint and yields a rule that never fires, so this
	// check has to be ours rather than the parser's.
	if !strings.Contains(spec, "FREQ=") {
		return nil, errNoFreq
	}
	o, err := rrule.StrToROptionInLocation("RRULE:"+spec, loc)
	if err != nil {
		return nil, err
	}
	// Sub-daily frequencies are refused rather than supported. A window measured in hours that
	// comes round every hour is an opt-out that never ends, and it is the refresher rather than
	// the person who typed it that would pay for walking it.
	switch o.Freq {
	case rrule.YEARLY, rrule.MONTHLY, rrule.WEEKLY, rrule.DAILY:
	case rrule.HOURLY, rrule.MINUTELY, rrule.SECONDLY:
		return nil, errTooOften
	}
	o.Dtstart = time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, loc)
	r, err := rrule.NewRRule(*o)
	if err != nil {
		return nil, err
	}
	// An impossible rule — 30 February, say — parses and builds, and reports no next occurrence.
	// Storing it would leave somebody believing they had opted out of something.
	if r.After(now, true).IsZero() {
		return nil, errNeverRuns
	}
	return r, nil
}
