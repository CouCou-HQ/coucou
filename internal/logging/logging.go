// Package logging builds the console handler. It is the only place that knows what a log line looks
// like, which is why the level names and the colours live here and nowhere else.
package logging

import (
	"context"
	"io"
	"log/slog"
	"os"
	"slices"
	"strconv"
	"strings"
	"sync"
)

// msgColumn is where the attrs start, so a screenful of lines has one ragged column instead of
// none. A longer message pushes past it rather than being truncated — the message is the part
// worth reading.
const msgColumn = 26

// New returns the handler for w: the pretty one when w is a terminal, slog's text handler
// otherwise. A container, a pipe and `go test` all take the second path, so nothing that is going
// to be parsed ever gets the escape codes.
func New(w io.Writer, level slog.Leveler) slog.Handler {
	if !terminal(w) {
		return slog.NewTextHandler(w, &slog.HandlerOptions{Level: level})
	}
	return &pretty{w: w, level: level, mu: new(sync.Mutex), color: colorOK()}
}

// terminal asks the writer whether it is a character device. os.File satisfies the interface, a
// bytes.Buffer and an io.Pipe do not, which is the whole test.
func terminal(w io.Writer) bool {
	f, ok := w.(interface{ Stat() (os.FileInfo, error) })
	if !ok {
		return false
	}
	fi, err := f.Stat()
	return err == nil && fi.Mode()&os.ModeCharDevice != 0
}

// NO_COLOR is honoured for any value including the empty string, which is what the spec at
// no-color.org asks for; TERM=dumb is the older form of the same request.
func colorOK() bool {
	_, off := os.LookupEnv("NO_COLOR")
	return !off && os.Getenv("TERM") != "dumb"
}

const (
	reset  = "\033[0m"
	dim    = "\033[2m"
	bold   = "\033[1m"
	red    = "\033[31m"
	green  = "\033[32m"
	yellow = "\033[33m"
	cyan   = "\033[36m"
)

type pretty struct {
	mu    *sync.Mutex // shared by every derived handler: they all write to the one w
	w     io.Writer
	level slog.Leveler
	color bool
	pre   []byte // WithAttrs attrs, already rendered under the group prefix they were added at
	group string // dotted prefix for the attrs on the record itself
}

func (h *pretty) Enabled(_ context.Context, l slog.Level) bool { return l >= h.level.Level() }

func (h *pretty) Handle(_ context.Context, r slog.Record) error {
	buf := make([]byte, 0, 256)
	if !r.Time.IsZero() {
		buf = h.paint(buf, dim, r.Time.Format("15:04:05.000"))
		buf = append(buf, ' ')
	}
	// Level from String rather than a switch, so -log-level INFO+2 prints as itself instead of
	// being rounded to the nearest name.
	name := r.Level.String()
	buf = h.paint(buf, levelColor(r.Level), name)
	buf = append(buf, strings.Repeat(" ", max(1, 6-len(name)))...)

	buf = h.paint(buf, bold, r.Message)
	buf = append(buf, strings.Repeat(" ", max(0, msgColumn-len(r.Message)))...)

	buf = append(buf, h.pre...)
	r.Attrs(func(a slog.Attr) bool {
		buf = h.attr(buf, h.group, a)
		return true
	})
	buf = append(buf, '\n')

	h.mu.Lock()
	defer h.mu.Unlock()
	_, err := h.w.Write(buf)
	return err
}

func (h *pretty) WithAttrs(as []slog.Attr) slog.Handler {
	if len(as) == 0 {
		return h
	}
	c := *h
	// Rendered now rather than kept: the group prefix an attr belongs under is the one in force
	// when WithAttrs was called, and storing the attrs raw loses which that was.
	c.pre = slices.Clip(h.pre)
	for _, a := range as {
		c.pre = h.attr(c.pre, h.group, a)
	}
	return &c
}

func (h *pretty) WithGroup(name string) slog.Handler {
	if name == "" {
		return h
	}
	c := *h
	c.group = h.group + name + "."
	return &c
}

func (h *pretty) attr(buf []byte, prefix string, a slog.Attr) []byte {
	a.Value = a.Value.Resolve()
	if a.Equal(slog.Attr{}) {
		return buf
	}
	if a.Value.Kind() == slog.KindGroup {
		if a.Key != "" {
			prefix += a.Key + "."
		}
		for _, g := range a.Value.Group() {
			buf = h.attr(buf, prefix, g)
		}
		return buf // an empty group prints nothing, not a bare key
	}
	buf = append(buf, ' ')
	buf = h.paint(buf, dim, prefix+a.Key+"=")
	// Errors are the reason anyone is reading the line, so the value carries the colour rather
	// than the key the eye already skipped.
	style := ""
	if a.Key == "err" || a.Key == "error" {
		style = red
	}
	return h.paint(buf, style, quote(a.Value.String()))
}

func (h *pretty) paint(buf []byte, style, s string) []byte {
	if !h.color || style == "" {
		return append(buf, s...)
	}
	buf = append(buf, style...)
	buf = append(buf, s...)
	return append(buf, reset...)
}

func levelColor(l slog.Level) string {
	switch {
	case l < slog.LevelInfo:
		return cyan
	case l < slog.LevelWarn:
		return green
	case l < slog.LevelError:
		return yellow
	default:
		return red
	}
}

// quote matches what slog's text handler quotes, so the two formats stay greppable the same way.
func quote(s string) string {
	if s == "" || strings.ContainsAny(s, ` "=`) {
		return strconv.Quote(s)
	}
	return s
}
