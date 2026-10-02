package bot

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/ThreeDotsLabs/watermill/message"
	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/events"
	"github.com/disgoorg/snowflake/v2"
)

// marshalJSON builds a frame the way the gateway does, for the tests that exercise push directly
// rather than going through a listener.
func marshalJSON(v any) (*message.Message, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	return message.NewMessage("test", b), nil
}

func recv(t *testing.T, ch <-chan *message.Message) *message.Message {
	t.Helper()
	select {
	case m, ok := <-ch:
		if !ok {
			t.Fatal("channel closed before a frame arrived")
		}
		return m
	case <-time.After(2 * time.Second):
		t.Fatal("no frame inside 2s")
		return nil
	}
}

// Two handlers on one topic each need their own copy. Watermill messages carry ack state, so a
// shared one means the first Ack satisfies the second handler's too — silently, and only under
// fanout, which is exactly the case this exists for.
func TestGatewayCopiesPerSubscriber(t *testing.T) {
	g := NewGateway()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	a, err := g.Subscribe(ctx, TopicGuildCreate)
	if err != nil {
		t.Fatalf("subscribe a: %v", err)
	}
	b, err := g.Subscribe(ctx, TopicGuildCreate)
	if err != nil {
		t.Fatalf("subscribe b: %v", err)
	}

	guild := discord.GatewayGuild{}
	guild.ID = snowflake.ID(7)
	go func() {
		msg, err := marshalJSON(guild)
		if err != nil {
			return
		}
		g.push(context.Background(), TopicGuildCreate, msg)
	}()

	ma, mb := recv(t, a), recv(t, b)
	if ma == mb {
		t.Fatal("both subscribers got the same message; ack state is shared")
	}
	if !ma.Ack() {
		t.Error("first subscriber could not ack")
	}
	// The second must still be un-acked: if the copy were skipped this would already be satisfied.
	select {
	case <-mb.Acked():
		t.Error("acking one subscriber's copy acked the other's")
	default:
	}
	if !mb.Ack() {
		t.Error("second subscriber could not ack")
	}
}

// A frame nobody subscribed to is dropped rather than blocking the gateway listener, which runs on
// disgo's dispatch goroutine.
func TestGatewayDropsUnsubscribedTopics(t *testing.T) {
	g := NewGateway()
	done := make(chan struct{})
	go func() {
		defer close(done)
		msg, err := marshalJSON(discord.GatewayGuild{})
		if err != nil {
			return
		}
		g.push(context.Background(), "discord.nobody_listens", msg)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("push blocked on a topic with no subscribers")
	}
}

// A subscriber that never reads must not stall the push: it runs on disgo's websocket read
// goroutine, under a mutex shared by every shard, and blocking there is what stops HEARTBEAT_ACK
// being read and makes every connection go zombie. Overrunning the queue drops frames instead,
// which is the trade the queue exists to make.
func TestGatewayPushDoesNotBlockOnAFullSubscriber(t *testing.T) {
	g := NewGateway()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if _, err := g.Subscribe(ctx, TopicGuildCreate); err != nil {
		t.Fatalf("subscribe: %v", err)
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		for range queueDepth * 2 { // twice the queue, so the back half has nowhere to go
			msg, err := marshalJSON(discord.GatewayGuild{})
			if err != nil {
				return
			}
			g.push(context.Background(), TopicGuildCreate, msg)
		}
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("push blocked on a subscriber that never reads")
	}
}

// Close ends every subscription. The Router relies on the channel closing to know a subscription is
// over, and closing a channel twice panics, so Close has to be idempotent.
func TestGatewayCloseIsIdempotent(t *testing.T) {
	g := NewGateway()
	ch, err := g.Subscribe(context.Background(), TopicGuildDelete)
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	if err := g.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	if err := g.Close(); err != nil {
		t.Fatalf("second close: %v", err)
	}
	if _, ok := <-ch; ok {
		t.Error("channel still open after Close")
	}
	if _, err := g.Subscribe(context.Background(), TopicGuildCreate); err == nil {
		t.Error("subscribing to a closed gateway succeeded")
	}
}

func TestTopicNaming(t *testing.T) {
	if got := Topic("GUILD_CREATE"); got != TopicGuildCreate {
		t.Errorf("Topic(GUILD_CREATE) = %q, want %q", got, TopicGuildCreate)
	}
}

// rawGuild is a GUILD_CREATE cut down to the field that decides the codec: Channels is
// []GuildChannel, an interface, and only disgo's UnmarshalJSON picks the concrete type behind it.
// Type 2 is a voice channel.
const rawGuild = `{"id":"7","name":"test","channels":[{"id":"11","type":2,"name":"General","guild_id":"7"}]}`

// The whole reason the gateway marshals with encoding/json: a frame has to survive the round trip
// with its interface fields intact. CBOR was measurably faster and could not do this — it fails
// with "cannot unmarshal map into ... of type discord.GuildChannel" on every real guild — so this
// is the test that fails if the codec is swapped for a faster one again.
func TestGatewayFrameKeepsDiscordsInterfaceFields(t *testing.T) {
	var guild discord.GatewayGuild
	if err := json.Unmarshal([]byte(rawGuild), &guild); err != nil {
		t.Fatalf("disgo could not decode the fixture: %v", err)
	}
	if len(guild.Channels) != 1 {
		t.Fatalf("fixture has %d channels, want 1", len(guild.Channels))
	}

	g := NewGateway()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ch, err := g.Subscribe(ctx, TopicGuildCreate)
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}

	// Through the real listener, so this covers the marshal the gateway actually does rather than
	// the test's own copy of it. A nil GenericGuild is fine: the listener reads only e.Guild.
	go func() {
		for _, l := range g.Listeners() {
			l.OnEvent(&events.GuildJoin{Guild: guild})
		}
	}()

	var back discord.GatewayGuild
	if err := json.Unmarshal(recv(t, ch).Payload, &back); err != nil {
		t.Fatalf("decoding the frame: %v", err)
	}
	if len(back.Channels) != 1 {
		t.Fatalf("got %d channels back, want 1", len(back.Channels))
	}
	vc, ok := back.Channels[0].(discord.GuildVoiceChannel)
	if !ok {
		t.Fatalf("channel came back as %T, want discord.GuildVoiceChannel", back.Channels[0])
	}
	if vc.ID() != snowflake.ID(11) {
		t.Errorf("channel id is %s, want 11", vc.ID())
	}
}
