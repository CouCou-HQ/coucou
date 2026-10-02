package settings

import (
	"context"
	"testing"

	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/snowflake/v2"

	"github.com/be-sandaa/coucou/internal/store"
)

const (
	tzBrussels = "Europe/Brussels"
	tzParis    = "Europe/Paris"
	tzUTC      = "UTC"
)

func TestValidTZ(t *testing.T) {
	if !ValidTZ(tzBrussels) {
		t.Errorf("%s should be valid", tzBrussels)
	}
	if ValidTZ("Mars/Olympus_Mons") {
		t.Error("a made-up zone should not be valid")
	}
}

// The zone is what is stored; nothing reads the locale at run time any more.
func TestZone(t *testing.T) {
	if got := Zone(Settings{TZ: new(tzBrussels)}); got != tzBrussels {
		t.Errorf("Zone = %q, want the stored %q", got, tzBrussels)
	}
	if got := Zone(Settings{}); got != tzUTC {
		t.Errorf("Zone of a row not filled yet = %q, want %q", got, tzUTC)
	}
}

func TestGuessZone(t *testing.T) {
	tests := []struct {
		locale discord.Locale
		want   string
	}{
		{discord.LocaleFrench, tzParis},
		{discord.LocaleEnglishUS, tzUTC}, // spread across several zones
		{discord.LocaleUnknown, tzUTC},
	}
	for _, tc := range tests {
		if got := GuessZone(tc.locale); got != tc.want {
			t.Errorf("GuessZone(%q) = %q, want %q", tc.locale, got, tc.want)
		}
	}
}

// recording is a store that keeps the last settings written per guild.
type recording struct {
	store.Store
	wrote map[snowflake.ID]store.Settings
}

func (r *recording) UpsertSettings(_ context.Context, st store.Settings) error {
	r.wrote[st.Guild] = st
	return nil
}

// FillZones writes a guess for a guild with no zone, as the bot, and leaves a stored zone alone.
func TestFillZones(t *testing.T) {
	const blank, chosen = snowflake.ID(1), snowflake.ID(2)
	db := &recording{wrote: map[snowflake.ID]store.Settings{}}
	s := New(db)
	s.m[chosen] = Settings{Chance: 7, TZ: new(tzBrussels)}
	s.m[blank] = Settings{Chance: 3}

	n, err := s.FillZones(context.Background(), map[snowflake.ID]discord.Locale{
		blank: discord.LocaleFrench, chosen: discord.LocaleJapanese,
	})
	if err != nil || n != 1 {
		t.Fatalf("FillZones = %d, %v; want 1, nil", n, err)
	}
	w := db.wrote[blank]
	if w.TZ == nil || *w.TZ != tzParis || w.Chance != 3 || w.UpdatedBy != 0 {
		t.Errorf("wrote %+v for the blank guild, want Paris, its chance kept, by the bot", w)
	}
	if _, ok := db.wrote[chosen]; ok {
		t.Error("a guild with a zone had it written over")
	}
}

// Filled once is filled: the zone is stored, so a later locale change moves nothing.
func TestFillZonesOnlyOnce(t *testing.T) {
	const g = snowflake.ID(1)
	s := New(&recording{wrote: map[snowflake.ID]store.Settings{}})
	ctx := context.Background()
	if _, err := s.FillZones(ctx, map[snowflake.ID]discord.Locale{g: discord.LocaleFrench}); err != nil {
		t.Fatal(err)
	}
	n, err := s.FillZones(ctx, map[snowflake.ID]discord.Locale{g: discord.LocaleGerman})
	if err != nil || n != 0 {
		t.Errorf("second FillZones = %d, %v; want 0, nil", n, err)
	}
	if got := Zone(s.Get(g)); got != tzParis {
		t.Errorf("Zone = %q, want the first fill %q", got, tzParis)
	}
}

func TestLocaleZonesAreValid(t *testing.T) {
	for locale, tz := range localeZones {
		if !ValidTZ(tz) {
			t.Errorf("%s maps to %q, which does not load", locale, tz)
		}
	}
}
