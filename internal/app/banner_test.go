package app

import (
	"strings"
	"testing"
)

// The banner exists to answer "which build is this, and what did it wake up to?" from a log tail,
// so every part of the stamp and every count has to survive into the output.
func TestBannerCarriesTheWholeStamp(t *testing.T) {
	const (
		ver    = "v1.2.3"
		commit = "abc1234"
		date   = "2026-09-21T10:00:00Z"
	)

	var sb strings.Builder
	st := status{Name: "Honk", Shards: 4, Guilds: 12, Sounds: 1}
	if err := banner(&sb, Build{Version: ver, Commit: commit, Date: date}, st); err != nil {
		t.Fatalf("banner: %v", err)
	}

	out := sb.String()
	for _, want := range []string{ver, commit, date, "██", "4 shards", "12 guilds", "1 sound"} {
		if !strings.Contains(out, want) {
			t.Errorf("banner is missing %q:\n%s", want, out)
		}
	}
	if !strings.HasSuffix(out, "\n\n") {
		t.Error("banner should end with a blank line, so the first log line is not glued to it")
	}
}

// The font spells 26 letters. Anything else — a digit, an emoji, another script — prints as plain
// text rather than as half a word.
func TestWordmarkFallsBackOnUnspellableNames(t *testing.T) {
	if _, ok := wordmark("Honk"); !ok {
		t.Error("wordmark refused a name it can spell")
	}
	for _, name := range []string{"Bot2000", "ばか", ""} {
		if _, ok := wordmark(name); ok {
			t.Errorf("wordmark claimed to spell %q", name)
		}
	}
}
