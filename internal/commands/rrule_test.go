package commands

import (
	"strings"
	"testing"
	"time"
)

const tzTest = "Europe/Brussels"

// now is fixed so a rule's first occurrence does not depend on when the suite runs.
var ruleNow = time.Date(2026, 9, 22, 12, 0, 0, 0, time.UTC) // a Tuesday

func TestBuildRuleCarriesTheZoneInTheStoredText(t *testing.T) {
	r, err := buildRule(weeklySpec("MO,TU,WE,TH,FR", 9), tzTest, ruleNow)
	if err != nil {
		t.Fatalf("buildRule: %v", err)
	}
	// The zone lives in the rule text rather than a column of its own, so it has to be rendered.
	got := r.String()
	if !strings.Contains(got, "DTSTART;TZID="+tzTest) {
		t.Errorf("stored rule carries no zone:\n%s", got)
	}
	// DTSTART is midnight and BYHOUR decides the time of day; if that ever stops holding, every
	// stored schedule shifts by nine hours.
	next := r.After(ruleNow, true)
	if next.Hour() != 9 {
		t.Errorf("next occurrence at %v, want 09:00", next)
	}
	if next.Weekday() == time.Saturday || next.Weekday() == time.Sunday {
		t.Errorf("next occurrence on %s, want a weekday", next.Weekday())
	}
}

// The picker exists so nobody has to type BYDAY, not so it can mean something else. A rule built
// from the choices and the same rule typed by hand must be the same rule.
func TestTheGuidedSpecIsJustARule(t *testing.T) {
	guided, err := buildRule(weeklySpec("MO,WE", 20), tzTest, ruleNow)
	if err != nil {
		t.Fatalf("guided: %v", err)
	}
	raw, err := buildRule("FREQ=WEEKLY;BYDAY=MO,WE;BYHOUR=20;BYMINUTE=0;BYSECOND=0", tzTest, ruleNow)
	if err != nil {
		t.Fatalf("raw: %v", err)
	}
	if guided.String() != raw.String() {
		t.Errorf("guided and typed rules differ:\n%s\n---\n%s", guided.String(), raw.String())
	}
}

func TestBuildRuleRejects(t *testing.T) {
	cases := []struct {
		name, spec, tz string
	}{
		// Parses without complaint and yields a rule that never fires, so the check is ours.
		{"a body with no FREQ", "BYDAY=MO;BYHOUR=9", tzTest},
		// A window of hours coming round every hour is an opt-out with no end, and the refresher
		// pays for walking it.
		{"hourly", "FREQ=HOURLY", tzTest},
		{"minutely", "FREQ=MINUTELY", tzTest},
		{"secondly", "FREQ=SECONDLY", tzTest},
		{"an unknown frequency", "FREQ=FORTNIGHTLY", tzTest},
		// Builds fine and reports no next occurrence; storing it leaves somebody believing they
		// opted out of something.
		{"a date that never comes", "FREQ=YEARLY;BYMONTH=2;BYMONTHDAY=30", tzTest},
		{"an unknown time zone", "FREQ=WEEKLY;BYDAY=MO", "Mars/Olympus_Mons"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r, err := buildRule(c.spec, c.tz, ruleNow)
			if err == nil {
				t.Fatalf("accepted %q: %s", c.spec, r.String())
			}
		})
	}
}

// Case and a leading RRULE: are how people paste a rule out of a calendar app.
func TestBuildRuleAcceptsWhatPeoplePaste(t *testing.T) {
	for _, spec := range []string{
		"RRULE:FREQ=WEEKLY;BYDAY=MO",
		"freq=weekly;byday=mo",
		"  FREQ=WEEKLY;BYDAY=MO  ",
	} {
		if _, err := buildRule(spec, tzTest, ruleNow); err != nil {
			t.Errorf("buildRule(%q): %v", spec, err)
		}
	}
}
