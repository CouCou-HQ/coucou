// Package bus is the event backbone. Everything that happens is published once; anything that cares
// subscribes. In-memory (gochannel), in-process.
package bus

import (
	"time"

	"github.com/disgoorg/snowflake/v2"
)

// Event names double as topic names: "<Event>" → topic "coucou.<event>". One event type per topic.

// PlayFinished: a play attempt ended, well or badly. Consumed by stats, and by anything else later
// (a "last played" cache, a webhook, a Prometheus counter).
type PlayFinished struct {
	Guild     snowflake.ID   `json:"guild" validate:"required"`
	Channel   snowflake.ID   `json:"channel" validate:"required"`
	Sound     string         `json:"sound" validate:"required"`
	Trigger   string         `json:"trigger"`
	User      *snowflake.ID  `json:"user,omitempty"`
	Listeners []snowflake.ID `json:"listeners"`
	Fled      []snowflake.ID `json:"fled,omitempty"`
	OK        bool           `json:"ok"`
	Reason    string         `json:"reason,omitempty"`
	StartedAt time.Time      `json:"started_at"`
	Duration  time.Duration  `json:"duration" validate:"min=0"`
	Character string         `json:"character,omitempty"`
}

// GuildLeft is the boot reconcile only: a guild the bot was in when it went down and is not in now.
// A live departure is a Discord frame and travels on discord.guild_delete instead — there is no
// gateway event for "you left while the process was off", which is why this one stays ours.
type GuildLeft struct {
	Guild      snowflake.ID `json:"guild" validate:"required"`
	Reconciled bool         `json:"reconciled"`
}

type CommandInvoked struct {
	Guild snowflake.ID `json:"guild" validate:"required"`
	User  snowflake.ID `json:"user" validate:"required"`
	Name  string       `json:"name" validate:"required"`
	At    time.Time    `json:"at"`
}

type SettingsChanged struct {
	Guild snowflake.ID `json:"guild" validate:"required"`
	Field string       `json:"field" validate:"required"` // chance | quiet | suspense
	By    snowflake.ID `json:"by"`
}

type SoundAdded struct {
	Name      string `json:"name" validate:"required"`
	Character string `json:"character,omitempty"`
}
type SoundRemoved struct {
	Name      string `json:"name" validate:"required"`
	Character string `json:"character,omitempty"`
}
