package events

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/disgoorg/snowflake/v2"
)

// withConsole swaps the default logger for one writing to buf, and puts the old one back.
func withConsole(t *testing.T, level slog.Leveler) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: level})))
	t.Cleanup(func() { slog.SetDefault(prev) })
	return &buf
}

// kindCommand is the stored row kind, which is not the same thing as a Trigger that happens to
// spell the same word.
const (
	kindCommand = "command"
	keyName     = "name"
	soundPlay   = "play"
)

// An audit record is a row first. The columns are typed on the way in now, rather than being
// flattened to slog attrs and sniffed back out.
func TestAuditWritesARow(t *testing.T) {
	c := &capture{}
	l := New(c)
	_ = withConsole(t, slog.LevelInfo)

	g, u := snowflake.ID(11), snowflake.ID(22)
	l.Audit(context.Background(), Misc{Kind: kindCommand, Guild: &g, User: &u, Data: map[string]any{keyName: soundPlay}})
	l.Flush(context.Background())

	rows := c.rows()
	if len(rows) != 1 {
		t.Fatalf("got %d rows, want 1", len(rows))
	}
	r := rows[0]
	if r.Kind != kindCommand {
		t.Errorf("kind = %q", r.Kind)
	}
	if r.Guild == nil || *r.Guild != g {
		t.Errorf("guild = %v, want %v", r.Guild, g)
	}
	if r.User == nil || *r.User != u {
		t.Errorf("user = %v, want %v", r.User, u)
	}
	var data map[string]any
	if err := json.Unmarshal(r.Data, &data); err != nil {
		t.Fatalf("data is not json: %v", err)
	}
	if data[keyName] != soundPlay {
		t.Errorf("data = %v, want name=%s", data, soundPlay)
	}
	// An unset At means "now": every caller but /command has nothing better to offer.
	if time.Since(r.At) > time.Minute {
		t.Errorf("at = %v, want roughly now", r.At)
	}
}

// A caller that knows when the thing happened keeps that time: a queued event must be stored under
// when it happened, not when the handler got to it.
func TestAuditKeepsAGivenTime(t *testing.T) {
	c := &capture{}
	l := New(c)
	_ = withConsole(t, slog.LevelInfo)

	then := time.Now().Add(-2 * time.Hour).Truncate(time.Second)
	l.Audit(context.Background(), Misc{Kind: kindCommand, At: then})
	l.Flush(context.Background())

	if got := c.rows()[0].At; !got.Equal(then) {
		t.Errorf("at = %v, want %v", got, then)
	}
}

// The row and the console line are one call, so they cannot disagree about what happened.
func TestAuditAlsoPrintsOneAuditLine(t *testing.T) {
	c := &capture{}
	l := New(c)
	buf := withConsole(t, slog.LevelInfo)

	g := snowflake.ID(11)
	l.Audit(context.Background(), Misc{Kind: "guild_join", Guild: &g, Data: map[string]any{"memberCount": 7}})

	out := buf.String()
	if n := strings.Count(out, "\n"); n != 1 {
		t.Fatalf("got %d lines, want 1: %q", n, out)
	}
	for _, want := range []string{"level=INFO", "msg=guild_join", "guild=11", "memberCount=7"} {
		if !strings.Contains(out, want) {
			t.Errorf("line is missing %q: %q", want, out)
		}
	}
}

// Raising the console level must not stop a record being stored: it is a database row first and a
// log line second.
func TestAuditStoresThroughAQuietConsole(t *testing.T) {
	c := &capture{}
	l := New(c)
	buf := withConsole(t, slog.LevelError)

	l.Audit(context.Background(), Misc{Kind: "sound_added", Data: map[string]any{keyName: "airhorn"}})
	l.Flush(context.Background())

	if len(c.rows()) != 1 {
		t.Errorf("got %d rows, want the record stored anyway", len(c.rows()))
	}
	if buf.Len() != 0 {
		t.Errorf("console was quiet but got: %q", buf.String())
	}
}

// Map iteration is random; a log line whose attrs reshuffle every time is one nobody can grep.
func TestAuditLineIsDeterministic(t *testing.T) {
	l := New(&capture{})
	data := map[string]any{"a": 1, "b": 2, "c": 3, "d": 4, "e": 5}

	// Everything but the timestamp, which is the one attr that is supposed to differ.
	line := func() string {
		buf := withConsole(t, slog.LevelInfo)
		l.Audit(context.Background(), Misc{Kind: "settings_changed", Data: data})
		_, rest, _ := strings.Cut(buf.String(), " ")
		return rest
	}

	first := line()
	for range 20 {
		if got := line(); got != first {
			t.Fatalf("attr order is not stable:\n %q\n %q", first, got)
		}
	}
}
