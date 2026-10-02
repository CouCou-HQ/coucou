// Command coucou is a Discord voice drop-in bot. Everything it does lives in internal/app; this
// file exists to turn an error into an exit code and to carry the build stamp.
package main

import (
	"errors"
	"flag"
	"log/slog"
	"os"

	// Quiet hours are evaluated in each guild's IANA zone and the scratch image has no
	// /usr/share/zoneinfo. Embedding the database costs ~450 KB and removes the file dependency.
	_ "time/tzdata"

	"github.com/be-sandaa/coucou/internal/app"
)

// Stamped at build time with -ldflags "-X main.version=... -X main.commit=... -X main.date=...".
// The release workflow sets all three from the tag and the checkout, the makefile from git; these
// defaults are what an unstamped `go build` says, and the banner prints them as-is.
var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

func main() {
	switch err := app.Run(os.Args[1:], app.Build{Version: version, Commit: commit, Date: date}); {
	case err == nil, errors.Is(err, flag.ErrHelp):
		// -h has already printed the usage; nothing more to say.
	case errors.Is(err, app.ErrUsage):
		slog.Error("configuration", slog.Any("err", err))
		os.Exit(2)
	default:
		slog.Error("coucou", slog.Any("err", err))
		os.Exit(1)
	}
}
