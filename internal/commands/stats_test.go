package commands

import (
	"testing"

	"github.com/be-sandaa/coucou/internal/store"
)

func TestStatsVisibility(t *testing.T) {
	tests := []struct {
		scope string
		want  bool
	}{
		{scopeUser, true},
		{scopeGuild, false},
		{scopeBot, false},
	}
	for _, tt := range tests {
		t.Run(tt.scope, func(t *testing.T) {
			if got := statsEphemeral(tt.scope); got != tt.want {
				t.Errorf("statsEphemeral(%q) = %v, want %v", tt.scope, got, tt.want)
			}
		})
	}
}

func TestRankText(t *testing.T) {
	const onlyC = "C beats **12%** of people.\n"
	a := standing{short: "a", long: "A beats **%d%%** of %s.", pct: 40}
	b := standing{short: "b", long: "B beats **%d%%** of %s.", pct: 71}
	c := standing{short: "c", long: "C beats **%d%%** of %s.", pct: 12}
	with := func(s standing, pct int) standing { s.pct = pct; return s }
	tests := []struct {
		name string
		in   []standing
		want string
	}{
		{"nothing to rank", nil, ""},
		{"best is the headline", []standing{a, b, c}, "B beats **71%** of people.\na 40% · c 12%\n"},
		{"a tie goes to the first", []standing{a, with(b, 40)}, "A beats **40%** of people.\nb 40%\n"},
		{"a lone rank has no tail", []standing{c}, onlyC},
		{"zeros are left out", []standing{with(a, 0), c, with(b, 0)}, onlyC},
		{"all zeros is no rank", []standing{with(a, 0), with(b, 0)}, ""},
		{"stale cut-points cap at 99", []standing{with(a, 100)}, "A beats **99%** of people.\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := rankText(tt.in, "people"); got != tt.want {
				t.Errorf("rankText = %q, want %q", got, tt.want)
			}
		})
	}
}

// fixed ranks every metric at pct, or reports nothing to compare against.
func fixed(pct int, ok bool) rankFunc {
	return func(string, float64) (int, bool) { return pct, ok }
}

func TestRankHidden(t *testing.T) {
	caught := store.UserCounts{Heard: 2, Triggered: 1}
	tests := []struct {
		name string
		got  []standing
		want int
	}{
		{"user never caught in the window", userStandings(store.UserCounts{Triggered: 3, Fled: 1}, fixed(50, true)), 0},
		{"user without cut-points", userStandings(caught, fixed(0, false)), 0},
		{"user ranked", userStandings(caught, fixed(50, true)), 3},
		{"guild without a play in the window", guildStandings(store.GuildRecent{}, fixed(50, true)), 0},
		{"guild without cut-points", guildStandings(store.GuildRecent{Plays: 3, AvgListeners: 2}, fixed(0, false)), 0},
		{"guild ranked", guildStandings(store.GuildRecent{Plays: 3, AvgListeners: 2}, fixed(50, true)), 2},
		{"not in the guild's population", userStandings(store.UserCounts{}, exactRank(store.UserRank{})), 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if len(tt.got) != tt.want {
				t.Errorf("got %d standings, want %d: %+v", len(tt.got), tt.want, tt.got)
			}
		})
	}
}

func TestExactRank(t *testing.T) {
	r := store.UserRank{UserCounts: store.UserCounts{Heard: 3}, HeardBelow: 2, TriggeredBelow: 1, Of: 3}
	rank := exactRank(r)
	tests := []struct {
		metric string
		want   int
	}{
		{store.MetricHeard, 66},
		{store.MetricTriggered, 33},
		{store.MetricFled, 0},
	}
	for _, tt := range tests {
		t.Run(tt.metric, func(t *testing.T) {
			if got, ok := rank(tt.metric, 0); !ok || got != tt.want {
				t.Errorf("rank(%s) = %d, %v; want %d, true", tt.metric, got, ok, tt.want)
			}
		})
	}
}
