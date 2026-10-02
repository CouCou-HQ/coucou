package logging

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"
)

// plain builds the pretty handler directly: New would hand back the text handler for a buffer,
// which is the point of New but not what these tests are about. Colour off so the assertions are
// about the layout rather than the escape codes.
func plain(w *bytes.Buffer) slog.Handler {
	return &pretty{w: w, level: slog.LevelDebug, mu: new(sync.Mutex)}
}

func line(t *testing.T, f func(*slog.Logger)) string {
	t.Helper()
	var buf bytes.Buffer
	f(slog.New(plain(&buf)))
	return strings.TrimSuffix(buf.String(), "\n")
}

func TestFormat(t *testing.T) {
	tests := []struct {
		name string
		log  func(*slog.Logger)
		want string
	}{
		{
			name: "attrs follow the message",
			log:  func(l *slog.Logger) { l.Info("tick", slog.Int("joins", 3)) },
			want: "INFO  tick                       joins=3",
		},
		{
			name: "a value with spaces is quoted",
			log:  func(l *slog.Logger) { l.Warn("play failed", slog.Any("err", errors.New("no such file"))) },
			want: `WARN  play failed                err="no such file"`,
		},
		{
			name: "a long message pushes past the column",
			log:  func(l *slog.Logger) { l.Debug("sounds: fsnotify unavailable, polling", slog.Int("n", 1)) },
			want: "DEBUG sounds: fsnotify unavailable, polling n=1",
		},
		{
			name: "WithAttrs come before the record's own",
			log: func(l *slog.Logger) {
				l.With(slog.Int("shard", 0)).Error("boom", slog.Int("n", 2))
			},
			want: "ERROR boom                       shard=0 n=2",
		},
		{
			name: "a group prefixes only what follows it",
			log: func(l *slog.Logger) {
				l.With(slog.Int("a", 1)).WithGroup("g").With(slog.Int("b", 2)).Info("m", slog.Int("c", 3))
			},
			want: "INFO  m                          a=1 g.b=2 g.c=3",
		},
		{
			name: "an inline group is expanded, an empty one prints nothing",
			log: func(l *slog.Logger) {
				l.Info("m", slog.Group("g", slog.Int("b", 2)), slog.Group("empty"))
			},
			want: "INFO  m                          g.b=2",
		},
		{
			name: "an offset level keeps its own name",
			log:  func(l *slog.Logger) { l.Log(context.Background(), slog.LevelInfo+2, "m") },
			want: "INFO+2 m",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// The timestamp is the one part that differs every run, so it is cut rather than asserted.
			got := line(t, tt.log)
			_, rest, ok := strings.Cut(got, " ")
			if !ok {
				t.Fatalf("no timestamp in %q", got)
			}
			if strings.TrimRight(rest, " ") != strings.TrimRight(tt.want, " ") {
				t.Errorf("\n got %q\nwant %q", rest, tt.want)
			}
		})
	}
}

// Colour is decoration: turning it on must not move anything.
func TestColorOnlyAddsEscapes(t *testing.T) {
	var bare, painted bytes.Buffer
	rec := func(w *bytes.Buffer, color bool) {
		h := &pretty{w: w, level: slog.LevelDebug, mu: new(sync.Mutex), color: color}
		r := slog.NewRecord(time.Time{}, slog.LevelInfo, "m", 0)
		r.AddAttrs(slog.String("k", "v"))
		if err := h.Handle(context.Background(), r); err != nil {
			t.Fatalf("handle: %v", err)
		}
	}
	rec(&bare, false)
	rec(&painted, true)

	stripped := painted.String()
	for _, esc := range []string{reset, dim, bold, red, green, yellow, cyan} {
		stripped = strings.ReplaceAll(stripped, esc, "")
	}
	if stripped != bare.String() {
		t.Errorf("colour changed the layout:\n %q\n %q", stripped, bare.String())
	}
}

// A buffer is not a terminal, so New must hand back the parseable handler.
func TestNewFallsBackToText(t *testing.T) {
	var buf bytes.Buffer
	if _, ok := New(&buf, slog.LevelInfo).(*pretty); ok {
		t.Fatal("got the pretty handler for a non-terminal writer")
	}
}
