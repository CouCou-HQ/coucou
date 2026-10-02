package bus

import (
	"testing"
	"time"

	"github.com/disgoorg/snowflake/v2"
	"github.com/google/uuid"
)

// atNano is deliberately not a round second: an encoding that rounds timestamps looks fine until a
// stats row lands in the wrong second, and nothing ever flags it.
var atNano = time.Date(2026, 9, 21, 8, 38, 34, 792842064, time.UTC)

const testUser = snowflake.ID(300000000000000001)

func TestCodecRoundTripsPlayFinished(t *testing.T) {
	user := testUser
	in := PlayFinished{
		Guild: 8000001, Channel: 9000001, Sound: sndAirhorn, Trigger: trigCmd, User: &user,
		Listeners: []snowflake.ID{4000001, 4000002}, Fled: []snowflake.ID{4000002}, OK: false, Reason: rsnJoinIt,
		StartedAt: atNano, Duration: 3 * time.Second,
	}
	var out PlayFinished
	roundTrip(t, in, &out)

	if out.User == nil || *out.User != user {
		t.Errorf("User = %v, want %d", out.User, user)
	}
	if !sameIDs(out.Listeners, in.Listeners) {
		t.Errorf("Listeners = %v, want %v", out.Listeners, in.Listeners)
	}
	if !sameIDs(out.Fled, in.Fled) {
		t.Errorf("Fled = %v, want %v", out.Fled, in.Fled)
	}
	if !out.StartedAt.Equal(atNano) {
		t.Errorf("StartedAt = %s, want %s — the codec is losing precision", out.StartedAt, atNano)
	}
	if out.Duration != 3*time.Second {
		t.Errorf("Duration = %s, want 3s", out.Duration)
	}
	if out.Sound != sndAirhorn || out.Reason != rsnJoinIt || out.OK {
		t.Errorf("round trip changed a scalar: %+v", out)
	}
}

// A nil *snowflake.ID must come back nil, not as a zero id — a zero User would be recorded as a
// real user having triggered the play.
func TestCodecRoundTripsAbsentUser(t *testing.T) {
	in := PlayFinished{Guild: 8000001, Channel: 9000001, Sound: soundBoom, Trigger: trigLoop, Duration: 5 * time.Second, StartedAt: atNano}
	var out PlayFinished
	roundTrip(t, in, &out)

	if out.User != nil {
		t.Errorf("User = %v, want nil", out.User)
	}
	if !out.StartedAt.Equal(atNano) {
		t.Errorf("StartedAt = %s, want %s", out.StartedAt, atNano)
	}
	if out.Duration != 5*time.Second {
		t.Errorf("Duration = %s, want 5s", out.Duration)
	}
}

func TestCodecRoundTripsTimestampedEvents(t *testing.T) {
	t.Run("CommandInvoked", func(t *testing.T) {
		in := CommandInvoked{Guild: 8000001, User: testUser, Name: "play", At: atNano}
		var out CommandInvoked
		roundTrip(t, in, &out)
		if out.Guild != in.Guild || out.User != in.User || out.Name != in.Name || !out.At.Equal(atNano) {
			t.Errorf("got %+v, want %+v", out, in)
		}
	})
}

func TestCodecRoundTripsScalarEvents(t *testing.T) {
	roundTripEqual(t, GuildLeft{Guild: 8000001, Reconciled: true})
	roundTripEqual(t, SettingsChanged{Guild: 8000001, Field: "chance", By: testUser})
	roundTripEqual(t, SoundAdded{Name: sndAirhorn})
	roundTripEqual(t, SoundRemoved{Name: sndAirhorn})
}

func sameIDs(a, b []snowflake.ID) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func roundTripEqual[T comparable](t *testing.T, in T) {
	t.Helper()
	var out T
	roundTrip(t, in, &out)
	if out != in {
		t.Errorf("got %+v, want %+v", out, in)
	}
}

// roundTrip goes through the real marshaler, checking the envelope on the way: the name metadata is
// what routes an event, so a payload that survives under the wrong name is still broken.
func roundTrip(t *testing.T, in, out any) {
	t.Helper()
	m := newMarshaler()
	msg, err := m.Marshal(in)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if got, want := m.NameFromMessage(msg), m.Name(in); got != want {
		t.Errorf("name metadata = %q, want %q", got, want)
	}
	if err := m.Unmarshal(msg, out); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
}

// Marshal must refuse a malformed event at the publisher, where the log line still says who
// published it — not three hops later in a handler.
func TestCodecRejectsInvalidOnMarshal(t *testing.T) {
	for _, tc := range []struct {
		name string
		ev   any
	}{
		{"zero guild", PlayFinished{Channel: 9000001, Sound: soundBoom}},
		{"zero channel", PlayFinished{Guild: 8000001, Sound: soundBoom}},
		{"negative duration", PlayFinished{Guild: 8000001, Channel: 9000001, Sound: soundBoom, Duration: -time.Second}},
		{"finished without sound", PlayFinished{Guild: 8000001, Channel: 9000001}},
		{"command without name", CommandInvoked{Guild: 8000001, User: testUser}},
		{"settings without field", SettingsChanged{Guild: 8000001, By: testUser}},
		{"sound without name", SoundAdded{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := newMarshaler().Marshal(tc.ev); err == nil {
				t.Fatalf("Marshal(%+v) = nil error, want a rejection", tc.ev)
			}
		})
	}
}

// And on the way back in: a payload that decodes but breaks an event's invariants must not reach a
// handler.
func TestCodecRejectsInvalidOnUnmarshal(t *testing.T) {
	m := newMarshaler()
	// Marshal a valid event, then hand its payload back as a type whose invariants it breaks:
	// SoundAdded{Name: ""} is what a GuildLeft payload decodes to.
	msg, err := m.Marshal(GuildLeft{Guild: 8000001, Reconciled: true})
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	var out SoundAdded
	if err := m.Unmarshal(msg, &out); err == nil {
		t.Fatal("Unmarshal accepted a payload that decodes to an invalid event, want a rejection")
	}
}

// Message ids must be UUIDv7: time-ordered, so ids issued later sort later.
func TestCodecIssuesSortableV7Ids(t *testing.T) {
	m := newMarshaler()
	var prev string
	for i := range 50 {
		msg, err := m.Marshal(SoundAdded{Name: sndAirhorn})
		if err != nil {
			t.Fatalf("Marshal: %v", err)
		}
		id, err := uuid.Parse(msg.UUID)
		if err != nil {
			t.Fatalf("message id %q is not a uuid: %v", msg.UUID, err)
		}
		if got := id.Version(); got != 7 {
			t.Fatalf("message id version = %d, want 7", got)
		}
		if i > 0 && msg.UUID < prev {
			t.Fatalf("id %q sorts before the one issued earlier (%q); v7 must be monotonic", msg.UUID, prev)
		}
		prev = msg.UUID
	}
}
