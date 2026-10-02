package bus

import (
	"testing"
	"time"

	"github.com/ThreeDotsLabs/watermill/components/cqrs"
	"github.com/disgoorg/snowflake/v2"
)

// A realistic PlayFinished: a busy channel, a command trigger, a failure reason.
func sample() PlayFinished {
	u := snowflake.ID(300000000000000001)
	ls := make([]snowflake.ID, 8)
	for i := range ls {
		ls[i] = snowflake.ID(400000000000000000 + i)
	}
	return PlayFinished{
		Guild: 800000000000000001, Channel: 900000000000000001,
		Sound: sndAirhorn, Trigger: trigCmd, User: &u, Listeners: ls,
		OK: false, Reason: rsnJoinIt,
		StartedAt: time.Now().UTC(), Duration: 3 * time.Second,
	}
}

// benchMarshaler is the round trip a published event actually pays. Run both to justify the codec:
//
//	go test -run XXX -bench 'Marshaler' -benchmem ./internal/bus/
func benchMarshaler(b *testing.B, m cqrs.CommandEventMarshaler) {
	b.Helper()
	v := sample()
	msg, err := m.Marshal(v)
	if err != nil {
		b.Fatal(err)
	}
	b.ReportMetric(float64(len(msg.Payload)), "wireB")
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		msg, err := m.Marshal(v)
		if err != nil {
			b.Fatal(err)
		}
		var out PlayFinished
		if err := m.Unmarshal(msg, &out); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkMarshalerJSON(b *testing.B) {
	benchMarshaler(b, cqrs.JSONMarshaler{GenerateName: cqrs.StructName})
}

func BenchmarkMarshalerCBOR(b *testing.B) { benchMarshaler(b, newMarshaler()) }
