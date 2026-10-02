//go:build dev

package commands

import (
	"fmt"
	"log/slog"
	"os"

	"github.com/disgoorg/disgo/bot"
	"github.com/disgoorg/snowflake/v2"
)

// envDevGuild is where a development build registers its commands. It is read here rather than
// parsed into config, which the release build shares and has no such concept in.
const envDevGuild = "DISCORD_DEV_GUILD"

// Deploy registers the commands to the one guild in DISCORD_DEV_GUILD, where they appear
// immediately. It refuses without one — stopping the start — rather than falling back to the global
// set, which is the whole point of the split.
func Deploy(c *bot.Client) error {
	guild := os.Getenv(envDevGuild)
	if guild == "" {
		return fmt.Errorf("a development build needs %s; it will not deploy globally", envDevGuild)
	}
	id, err := snowflake.Parse(guild)
	if err != nil {
		return fmt.Errorf("%s %q: %w", envDevGuild, guild, err)
	}
	if _, err := c.Rest.SetGuildCommands(c.ApplicationID, id, definitions); err != nil {
		return fmt.Errorf("deploy to guild %s: %w", guild, err)
	}
	slog.Info("deployed commands to guild", slog.String("guild", guild), slog.Int("n", len(definitions)))
	return nil
}
