package chaos

import (
	"testing"
	"time"

	"github.com/disgoorg/snowflake/v2"

	"github.com/be-sandaa/coucou/internal/schedule"
)

const guild = snowflake.ID(1)

// Fridays 20:00-23:00 in Brussels.
const friday20 = "DTSTART;TZID=Europe/Brussels:20260901T000000\nRRULE:FREQ=WEEKLY;BYDAY=FR;BYHOUR=20;BYMINUTE=0;BYSECOND=0"

func at(t *testing.T, day, hour int) time.Time {
	t.Helper()
	loc, err := time.LoadLocation("Europe/Brussels")
	if err != nil {
		t.Fatalf("load zone: %v", err)
	}
	return time.Date(2026, time.September, day, hour, 30, 0, 0, loc)
}

func TestChanceIsBoostedOnlyInsideTheWindow(t *testing.T) {
	cases := []struct {
		name  string
		now   time.Time
		base  int
		boost int
		want  int
	}{
		{"inside", at(t, 25, 21), 10, 50, 50},
		{"before it opens", at(t, 25, 19), 10, 50, 10},
		{"after it closes", at(t, 25, 23), 10, 50, 10},
		{"another day", at(t, 24, 21), 10, 50, 10},
		{"a /chance raised past it", at(t, 25, 21), 70, 50, 70},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			w, err := schedule.Parse(friday20, 3*time.Hour, c.now)
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			s := &Store{m: map[snowflake.ID]Window{guild: {Window: w, Chance: c.boost}}}
			if got := s.Chance(guild, c.base, c.now); got != c.want {
				t.Errorf("Chance = %d, want %d", got, c.want)
			}
		})
	}
}

func TestChanceWithoutAWindowIsTheBase(t *testing.T) {
	if got := New(nil).Chance(guild, 10, at(t, 25, 21)); got != 10 {
		t.Errorf("Chance = %d, want 10", got)
	}
}
