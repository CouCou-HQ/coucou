//go:build !dev

package commands

import (
	"fmt"
	"log/slog"

	"github.com/disgoorg/disgo/bot"
)

// Deploy registers the commands globally, which is all a release build can do: guild-scoping exists
// to keep half-finished commands off every server the bot is in, and only the build that makes them
// needs it — see deploy_dev.go.
//
// It runs on every start. The set is idempotent, and Discord only propagates a change — which it
// takes up to an hour to do, so a new command is not live the moment the bot is.
func Deploy(c *bot.Client) error {
	if _, err := c.Rest.SetGlobalCommands(c.ApplicationID, definitions); err != nil {
		return fmt.Errorf("deploy global commands: %w", err)
	}
	slog.Info("deployed global commands", slog.Int("n", len(definitions)))
	return nil
}
