package commands

import (
	"context"
	"log/slog"
	"time"

	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/events"
	"github.com/disgoorg/disgo/rest"
	"github.com/disgoorg/snowflake/v2"
)

const (
	cmdNameForget = "forget"
	// The custom ids of the buttons under /forget's confirmation.
	forgetConfirm = "forget:me"
	forgetCancel  = "forget:cancel"
)

var forgetDefinition = discord.SlashCommandCreate{Name: cmdNameForget, Description: "Erase what the bot has recorded about you"}

const forgetWarning = "In every server this bot is in: the times you were caught or fled, the sounds you collected, " +
	"and your place on the leaderboards all go. Plays you started stay in each server's totals, with no one named.\n\n" +
	"This can't be undone."

// cmdForget only asks; the erasing is behind the button. The question is ephemeral, so only whoever
// ran /forget can press it, and it erases them.
func (c *Commands) cmdForget(_ context.Context, e *events.ApplicationCommandInteractionCreate, _ snowflake.ID, _ discord.SlashCommandInteractionData) error {
	return e.CreateMessage(say(bad("Forget you?", forgetWarning)).AddActionRow(
		discord.NewDangerButton("Forget me", forgetConfirm),
		discord.NewSecondaryButton("Keep me", forgetCancel),
	))
}

func (c *Commands) confirmForget(e *events.ComponentInteractionCreate) error {
	if err := e.DeferUpdateMessage(); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	user := e.User().ID
	reply := info("Forgotten", "Nothing the bot has recorded names you anymore.")
	err := c.events.Forget(ctx, user)
	switch _, opted := c.optouts.Get(user); {
	case err != nil:
		reply = bad("Couldn't forget you", "The database isn't answering. Nothing was erased; try again in a minute.")
	case opted:
		reply.Description += "\n\nYour `/optout` stays, so the bot still isn't counting you. `/optout off` ends it."
	}
	if _, uerr := c.client.Rest.UpdateInteractionResponse(c.client.ApplicationID, e.Token(),
		discord.NewMessageUpdate().WithEmbeds(reply).ClearComponents(), rest.WithCtx(ctx)); uerr != nil {
		slog.Error("answering the forget confirmation", slog.Any("err", uerr))
	}
	return err
}
