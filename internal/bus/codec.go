package bus

import (
	"encoding/json"
	"fmt"

	"github.com/ThreeDotsLabs/watermill/components/cqrs"
	"github.com/ThreeDotsLabs/watermill/message"
	"github.com/google/uuid"

	"github.com/be-sandaa/coucou/internal/validate"
)

// check runs an event against the `validate` tags on its fields. The codec does this on both
// sides: on the way out so a bug that publishes a zero-valued event fails at the publisher, where
// the log line still names who did it; on the way in because a gateway payload came from Discord
// and has not been trusted since.
//
// The event name goes in front so the error says which type is wrong, since the field names come
// from the json tags and are not unique across events.
func (m jsonMarshaler) check(v any) error {
	if err := validate.Struct(v); err != nil {
		return fmt.Errorf("bus: %s: %w", m.Name(v), err)
	}
	return nil
}

// jsonMarshaler is watermill's JSON codec plus two things it does not do: it validates on both
// sides, and it mints a sortable message id.
//
// JSON, not CBOR, and that is a reversal worth stating. CBOR was roughly 2.8x faster and 238 B on
// the wire against 409 (BenchmarkMarshaler* still measures it on a PlayFinished with 8 listeners).
// What it could not do is carry Discord's own types: disgo defines them by their JSON shape and
// resolves interface fields like GatewayGuild.Channels through custom UnmarshalJSON, which no CBOR
// decoder calls — it fails with "cannot unmarshal map into ... of type discord.GuildChannel" on
// every real GUILD_CREATE. One codec for everything beats a fast one for our events and a second
// for Discord's.
//
// The wire format is now plain JSON and readable: snowflake.ID's MarshalJSON renders ids as quoted
// strings, so a message dumped off a topic makes sense without this binary to decode it.
//
// encoding/gob was tried and is 3x worse than JSON on every axis: a bus message is standalone, so
// each one re-emits gob's whole type descriptor. gob only pays off on a long-lived stream, which
// watermill never hands us.
type jsonMarshaler struct {
	cqrs.JSONMarshaler
}

func (m jsonMarshaler) Marshal(v any) (*message.Message, error) {
	if err := m.check(v); err != nil {
		return nil, err
	}
	b, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	// UUIDv7 rather than watermill.NewUUID's v4: the first 48 bits are a millisecond timestamp, so
	// ids sort by publish time. That makes a message log readable in order
	// and keeps any index built on it from fragmenting the way random v4 does.
	id, err := uuid.NewV7()
	if err != nil {
		return nil, fmt.Errorf("bus: message id: %w", err) // the system entropy source is gone
	}
	msg := message.NewMessage(id.String(), b)
	msg.Metadata.Set("name", m.Name(v))
	return msg, nil
}

func (m jsonMarshaler) Unmarshal(msg *message.Message, v any) error {
	if err := json.Unmarshal(msg.Payload, v); err != nil {
		return err
	}
	return m.check(v)
}

func newMarshaler() jsonMarshaler {
	return jsonMarshaler{cqrs.JSONMarshaler{GenerateName: cqrs.StructName}}
}
