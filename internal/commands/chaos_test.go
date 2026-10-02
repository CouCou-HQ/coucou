package commands

import (
	"testing"
	"time"

	"github.com/be-sandaa/coucou/internal/schedule"
)

func TestChaosRefusesAnythingButABoost(t *testing.T) {
	cases := []struct {
		name         string
		chance, base int
		refused      bool
	}{
		{"above /chance", 30, 10, false},
		{"above a /chance of zero", 1, 0, false},
		{"equal to /chance", 10, 10, true},
		{"below /chance", 5, 10, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := chaosRefusal(c.chance, c.base) != nil; got != c.refused {
				t.Errorf("refused = %v, want %v", got, c.refused)
			}
		})
	}
}

func TestQuietOverlapWarns(t *testing.T) {
	loc, err := time.LoadLocation(tzTest)
	if err != nil {
		t.Fatalf("load zone: %v", err)
	}
	cases := []struct {
		name               string
		days               string
		from, hours        int
		quietFrom, quietTo int
		want               bool
	}{
		{"evening window, night quiet", "TH,FR", 20, 3, 23, 7, false},
		{"runs into the quiet hours", "FR,SA", 20, 4, 23, 7, true},
		{"wraps past midnight into them", "FR,SA,SU", 22, 6, 2, 6, true},
		{"starts inside them", "MO,TU", 3, 1, 23, 7, true},
		{"no quiet hours", "SU,MO,TU,WE,TH,FR,SA", 0, 24, 0, 0, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r, err := buildRule(weeklySpec(c.days, c.from), tzTest, ruleNow)
			if err != nil {
				t.Fatalf("buildRule: %v", err)
			}
			w := schedule.Window{Rule: r, Length: time.Duration(c.hours) * time.Hour}
			quiet := func(t time.Time) bool { return clockHours(c.quietFrom, c.quietTo, t.In(loc).Hour()) }
			if got := quietOverlap(w, quiet, ruleNow); got != c.want {
				t.Errorf("overlap = %v, want %v", got, c.want)
			}
		})
	}
}
