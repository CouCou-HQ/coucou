package commands

import (
	"slices"
	"testing"
	"time"

	"github.com/disgoorg/snowflake/v2"
)

// Discord sends one autocomplete interaction per keystroke. Answering every one is a REST call per
// letter, so only the last of a burst is answered — that is the whole point of the debounce, and
// this is what fails if a rewrite starts answering them all.
func TestDebouncerAnswersOnlyTheLastOfABurst(t *testing.T) {
	var d debouncer
	const key = snowflake.ID(1)
	got := make(chan string, 16)
	const last = "marta"
	for _, q := range []string{"m", "ma", "mar", "mart", last} {
		d.do(key, 30*time.Millisecond, func() { got <- q })
	}

	select {
	case q := <-got:
		if q != last {
			t.Errorf("answered %q, want the last keystroke of the burst", q)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the burst was never answered")
	}
	select {
	case q := <-got:
		t.Errorf("superseded keystroke %q was answered as well", q)
	case <-time.After(100 * time.Millisecond):
	}

	// The map must not keep an entry per user who has ever typed.
	d.mu.Lock()
	left := len(d.pending)
	d.mu.Unlock()
	if left != 0 {
		t.Errorf("%d pending entries left behind", left)
	}
}

// Two people typing at once must not cancel each other, which is what a single shared timer would do.
func TestDebouncerKeysAreIndependent(t *testing.T) {
	var d debouncer
	got := make(chan snowflake.ID, 4)
	d.do(snowflake.ID(1), 20*time.Millisecond, func() { got <- 1 })
	d.do(snowflake.ID(2), 20*time.Millisecond, func() { got <- 2 })

	var seen []snowflake.ID
	for range 2 {
		select {
		case id := <-got:
			seen = append(seen, id)
		case <-time.After(2 * time.Second):
			t.Fatalf("only %d of 2 keys were answered", len(seen))
		}
	}
	slices.Sort(seen)
	if !slices.Equal(seen, []snowflake.ID{1, 2}) {
		t.Errorf("answered %v, want both keys", seen)
	}
}
