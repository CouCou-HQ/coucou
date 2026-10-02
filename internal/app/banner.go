package app

import (
	"fmt"
	"io"
	"log/slog"
	"runtime"
	"strings"
	"unicode"

	dbot "github.com/disgoorg/disgo/bot"

	"github.com/be-sandaa/coucou/internal/sounds"
)

// Build is the stamp the linker puts on a binary. Version also reaches the trace resource; all
// three reach the banner, which is the only place a running process says which commit it is.
type Build struct {
	Version string
	Commit  string
	Date    string
}

// glyphs is a 3x5 block font, one uint16 per letter: five rows of three bits, most significant bit
// on the left. Discord lets a bot be called anything, so the name is drawn at runtime — and a
// figlet library plus its font files is a dependency for 26 glyphs that fit in 26 lines.
var glyphs = map[rune]uint16{
	'A': 0o2575_5, 'B': 0o6565_6, 'C': 0o3444_3, 'D': 0o6555_6, 'E': 0o7464_7,
	'F': 0o7464_4, 'G': 0o3455_3, 'H': 0o5575_5, 'I': 0o7222_7, 'J': 0o1115_2,
	'K': 0o5565_5, 'L': 0o4444_7, 'M': 0o5775_5, 'N': 0o5777_5, 'O': 0o2555_2,
	'P': 0o6564_4, 'Q': 0o2557_3, 'R': 0o6565_5, 'S': 0o3421_6, 'T': 0o7222_2,
	'U': 0o5555_7, 'V': 0o5552_2, 'W': 0o5577_5, 'X': 0o5525_5, 'Y': 0o5522_2,
	'Z': 0o7124_7,
}

// wordmark draws name in block letters, or returns false for anything the font cannot spell —
// digits, emoji, a name in another script. The caller then prints it as plain text rather than
// rendering half a word.
func wordmark(name string) ([]string, bool) {
	cols := make([]uint16, 0, len(name))
	for _, r := range strings.ToUpper(name) {
		if unicode.IsSpace(r) {
			cols = append(cols, 0)
			continue
		}
		g, ok := glyphs[r]
		if !ok {
			return nil, false
		}
		cols = append(cols, g)
	}
	if len(cols) == 0 {
		return nil, false
	}

	rows := make([]string, 5)
	for row := range rows {
		var sb strings.Builder
		for _, g := range cols {
			// Row 0 is the top, so it lives in the highest three bits of the fifteen.
			bits := g >> uint(3*(4-row))
			for col := 2; col >= 0; col-- {
				if bits&(1<<uint(col)) != 0 {
					sb.WriteString("██")
				} else {
					sb.WriteString("  ")
				}
			}
			sb.WriteString("  ") // one blank column between letters
		}
		rows[row] = " " + strings.TrimRight(sb.String(), " ")
	}
	return rows, true
}

// status is what the process can only know once Discord has answered: who it is, how much of the
// gateway it holds, and whether there is anything to play.
type status struct {
	Name   string
	Shards int
	Guilds int
	Sounds int
}

// printBanner prints the build stamp and what the process woke up to. serve calls it once, after
// the reconcile, which is the first point at which every count is final — on a GuildsReady listener
// the guild count could be one shard's worth. It is the only place the build stamp is printed.
func printBanner(w io.Writer, b Build, c *dbot.Client, reg *sounds.Registry) {
	s := status{Name: "coucou", Guilds: c.Caches.GuildsLen(), Sounds: reg.Len()}
	if u, ok := c.Caches.SelfUser(); ok {
		s.Name = u.Username
	}
	if c.HasShardManager() {
		for range c.ShardManager.Shards() {
			s.Shards++
		}
	}
	if err := banner(w, b, s); err != nil {
		slog.Debug("banner", slog.Any("err", err))
	}
}

// banner prints who this process is, which build it came from, and what it woke up to. A dead
// stdout costs the art and nothing else, so the error goes to the log rather than up the stack.
//
// It runs once per process, so there is nothing here worth the cost of a health probe printing it.
func banner(w io.Writer, b Build, s status) error {
	art, ok := wordmark(s.Name)
	if !ok {
		art = []string{" " + s.Name}
	}
	_, err := fmt.Fprintf(w, "\n%s\n\n %s · %s · built %s\n %s %s/%s · %s · %s · %s\n\n",
		strings.Join(art, "\n"), b.Version, b.Commit, b.Date,
		runtime.Version(), runtime.GOOS, runtime.GOARCH, plural(s.Shards, "shard", "shards"),
		plural(s.Guilds, "guild", "guilds"), plural(s.Sounds, "sound", "sounds"))
	return err
}

// plural keeps "1 guilds" out of the one line people actually read.
func plural(n int, one, many string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, one)
	}
	return fmt.Sprintf("%d %s", n, many)
}
