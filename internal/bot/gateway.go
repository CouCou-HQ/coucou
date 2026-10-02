package bot

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"sync"

	"github.com/ThreeDotsLabs/watermill"
	"github.com/ThreeDotsLabs/watermill/message"
	"github.com/disgoorg/disgo/bot"
	"github.com/disgoorg/disgo/events"
)

//#region Topics

// Gateway topics are the Discord event name, lowercased, under a namespace that says where the
// payload came from. They are not coucou.* topics: what travels on them is Discord's own shape, not
// ours, so a consumer reading one is reading Discord — including a consumer in another process that
// this bot has no type for.
const (
	TopicGuildCreate = "discord.guild_create"
	TopicGuildDelete = "discord.guild_delete"
)

//#endregion

//#region Subscriber

// Gateway makes the Discord client a watermill subscriber: disgo listeners push frames in, the
// Router consumes them like any other source. It exists so gateway events reach handlers through
// the same middleware — correlation, tracing, metrics, recovery — as everything else.
//
// Two things it deliberately does not do, because a gateway cannot:
//
// Nack means redeliver, and there is no asking Discord to send a frame again. A nacked message is
// dropped with a log line; a handler that must not lose work has to own that itself.
//
// Delivery is at-most-once into a bounded queue, and a push never blocks. It cannot: the listener
// runs on disgo's websocket read goroutine, under a mutex shared by every shard, and that goroutine
// is the only thing that reads HEARTBEAT_ACK. Blocking there for one heartbeat interval makes disgo
// call the connection zombie and reconnect — on every shard, not just the one the event arrived on.
// So a frame that finds the queue full is dropped with a log line, the same as a nacked one.
type Gateway struct {
	mu     sync.Mutex
	subs   map[string][]chan *message.Message
	closed bool
}

func NewGateway() *Gateway { return &Gateway{subs: make(map[string][]chan *message.Message)} }

// queueDepth is how far one subscriber may fall behind before its frames start being dropped. The
// router's Retry×Timeout middleware can sit on a single delivery for minutes, and guild joins are
// rare, so this is sized to ride out a stalled handler rather than to absorb a burst.
const queueDepth = 64

// Subscribe hands the Router a channel for one topic. Two handlers on the same topic get two
// channels and their own copy of every frame, which is what fanout means here.
func (g *Gateway) Subscribe(ctx context.Context, topic string) (<-chan *message.Message, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.closed {
		return nil, fmt.Errorf("gateway subscriber is closed")
	}
	ch := make(chan *message.Message, queueDepth)
	g.subs[topic] = append(g.subs[topic], ch)
	// The Router hands us its own context and expects the subscription to end when it does.
	go func() {
		<-ctx.Done()
		g.remove(topic, ch)
	}()
	return ch, nil
}

func (g *Gateway) remove(topic string, ch chan *message.Message) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.closed {
		return // Close already closed every channel; closing twice panics
	}
	for i, c := range g.subs[topic] {
		if c == ch {
			g.subs[topic] = append(g.subs[topic][:i], g.subs[topic][i+1:]...)
			close(ch)
			return
		}
	}
}

func (g *Gateway) Close() error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.closed {
		return nil
	}
	g.closed = true
	for _, chans := range g.subs {
		for _, ch := range chans {
			close(ch)
		}
	}
	return nil
}

// push sends one frame to every subscriber of a topic, each getting its own copy. The copy is not
// optional: watermill messages carry ack state, so two handlers sharing one message means the first
// ack silently satisfies the second. gochannel copies for the same reason.
//
// The send is non-blocking, which is also what makes it safe to hold the lock across it — and
// holding it is what stops a concurrent Close from closing a channel mid-send, which panics.
func (g *Gateway) push(ctx context.Context, topic string, msg *message.Message) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.closed {
		return
	}
	for _, ch := range g.subs[topic] {
		m := msg.Copy()
		m.SetContext(ctx)
		select {
		case ch <- m:
			go g.settle(topic, m)
		default:
			slog.Warn("gateway: subscriber queue full, frame dropped",
				slog.String("topic", topic), slog.String("id", m.UUID))
		}
	}
}

// settle waits for the Router's verdict. An ack is the normal end; a nack is where the gateway's
// honesty lives — it is reported and the frame is gone, because there is nothing to redeliver from.
func (g *Gateway) settle(topic string, m *message.Message) {
	select {
	case <-m.Acked():
	case <-m.Nacked():
		slog.Warn("gateway: frame dropped, a gateway cannot redeliver",
			slog.String("topic", topic), slog.String("id", m.UUID))
	}
}

//#endregion

//#region Listeners

// Listeners is the disgo half: every gateway event that becomes a topic. Interactions are not here
// and cannot be — a command has three seconds to reply and the Router's retry and timeout
// middleware sit in front of every delivery. commands.OnCommand keeps its own synchronous path.
//
// GuildReady is not here either, and that is the difference between 1,600 full GUILD_CREATE
// payloads at every boot and none: disgo dispatches GuildReady for the startup burst and GuildJoin
// only for a guild actually joined. Boot is reconciled from the cache by SyncGuilds instead.
func (g *Gateway) Listeners() []bot.EventListener {
	publish := func(topic string, payload any) {
		// JSON because the payload is Discord's: its types are defined by that representation, and
		// disgo's own UnmarshalJSON is the only thing that can pick the concrete type behind an
		// interface field like GatewayGuild.Channels. See bus.OnTopic.
		raw, err := json.Marshal(payload)
		if err != nil {
			slog.Error("gateway: marshal", slog.String("topic", topic), slog.Any("err", err))
			return
		}
		g.push(context.Background(), topic, message.NewMessage(watermill.NewUUID(), raw))
	}
	return []bot.EventListener{
		bot.NewListenerFunc(func(e *events.GuildJoin) {
			publish(TopicGuildCreate, e.Guild)
		}),
		bot.NewListenerFunc(func(e *events.GuildLeave) {
			publish(TopicGuildDelete, e.Guild)
		}),
	}
}

// Topic renders a Discord gateway event name as the topic it travels on, so a caller adding one
// does not have to guess the convention.
func Topic(discordEvent string) string { return "discord." + strings.ToLower(discordEvent) }

//#endregion
