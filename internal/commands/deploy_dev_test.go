//go:build dev

package commands

import (
	"strings"
	"testing"
)

// Without a guild it refuses rather than falling back to the global set, which is the whole point
// of the split.
func TestDevBuildRefusesToDeployWithoutAGuild(t *testing.T) {
	t.Setenv(envDevGuild, "")

	err := Deploy(nil, definitions) // returns before it touches the bot
	if err == nil {
		t.Fatal("deployed with no guild on a development build")
	}
	if !strings.Contains(err.Error(), envDevGuild) {
		t.Errorf("error = %q, want it to name the variable that is missing", err)
	}
}
