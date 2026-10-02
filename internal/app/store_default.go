//go:build !pg && !sqlite

package app

// Both backends, the way every build shipped before the tags existed. Build with -tags pg or
// -tags sqlite to leave the other one's driver out of the binary (~4 MB either way).
import (
	_ "github.com/be-sandaa/coucou/internal/store/pg"     // postgres:// postgresql://
	_ "github.com/be-sandaa/coucou/internal/store/sqlite" // sqlite:// file:
)
