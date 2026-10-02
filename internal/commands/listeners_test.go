package commands

import (
	"testing"
	"time"

	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/events"
	"github.com/disgoorg/disgo/rest"
)

// The listener has to return before the interaction is answered. disgo runs it inline on the
// shard's websocket read goroutine, under a mutex shared by every shard, and that goroutine is the
// only thing that reads HEARTBEAT_ACK — so a listener that waits on a REST call takes every shard
// down with it. A DM interaction is the shortest path through the dispatcher that still answers.
func TestCommandListenerReturnsBeforeTheInteractionIsAnswered(t *testing.T) {
	release := make(chan struct{})
	defer close(release)
	answered := make(chan struct{})

	e := &events.ApplicationCommandInteractionCreate{
		Respond: events.InteractionResponderFunc(func(discord.InteractionResponseType, discord.InteractionResponseData, ...rest.RequestOpt) error {
			close(answered)
			<-release // a REST call Discord is making us wait for
			return nil
		}),
	}

	returned := make(chan struct{})
	go func() { defer close(returned); (&Commands{}).OnCommand().OnEvent(e) }()

	select {
	case <-returned:
	case <-time.After(2 * time.Second):
		t.Fatal("the listener blocked until the interaction was answered")
	}
	// And it did reach the responder, so this is not passing on a dispatcher that returned early.
	select {
	case <-answered:
	case <-time.After(2 * time.Second):
		t.Fatal("the dispatcher never answered the interaction")
	}
}
