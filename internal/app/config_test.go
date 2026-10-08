package app

import (
	"errors"
	"flag"
	"log/slog"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/snowflake/v2"

	"github.com/be-sandaa/coucou/internal/characters"
	"github.com/be-sandaa/coucou/internal/commands"
	"github.com/be-sandaa/coucou/internal/profile"
)

const (
	testDSN    = "sqlite://x.db"
	testToken  = "tok"
	ownerA     = "987654321098765432"
	ownerB     = "876543210987654321"
	flagConfig = "-config"
	minimal    = "database_url = \"" + testDSN + "\"\ndiscord_token = \"" + testToken + "\"\n"
)

// writeConfig puts body in a temp config.toml and returns its path.
func writeConfig(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func load(t *testing.T, body string) (config, error) {
	t.Helper()
	return parseConfig([]string{flagConfig, writeConfig(t, body)})
}

func mustLoad(t *testing.T, body string) config {
	t.Helper()
	c, err := load(t, body)
	if err != nil {
		t.Fatalf("parseConfig: %v", err)
	}
	return c
}

// The defaults are pinned whole: raising the log level would quietly change what every deploy
// logs, and turning pprof on would expose allocation sites and goroutine stacks.
func TestDefaultsWhenOnlyTheRequiredKeysAreSet(t *testing.T) {
	want := config{
		DatabaseURL: testDSN,
		Token:       testToken,
		ProfileDir:  defaultProfileDir,
		Color:       profile.DefaultColor,
		HTTPAddr:    defaultHTTPAddr,
		LogLevel:    slog.LevelInfo,
	}
	if got := mustLoad(t, minimal); !reflect.DeepEqual(got, want) {
		t.Errorf("got  %+v\nwant %+v", got, want)
	}
}

func TestEveryKeyIsRead(t *testing.T) {
	got := mustLoad(t, minimal+`
owner_ids      = ["`+ownerA+`", "`+ownerB+`"]
siblings       = "Fart=`+ownerA+`"
profiles       = "/srv/characters"
default_profile = " lenore "
note           = " Run by the Fart people. "
color          = "#4E5058"

[status]
text     = " ✨ around "
activity = "watching"
online   = "idle"

[sounds]
poll = "15s"

[ops]
http_addr = ""
log_level = "WARN"
pprof     = true
otlp      = "collector:4317"

[gateway]
shard_count = 4
`)
	want := config{
		DatabaseURL:    testDSN,
		Token:          testToken,
		OwnerIDs:       []snowflake.ID{snowflake.MustParse(ownerA), snowflake.MustParse(ownerB)},
		Siblings:       []commands.Sibling{{Name: "Fart", App: snowflake.MustParse(ownerA)}},
		ProfilesDir:    "/srv/characters",
		DefaultProfile: "lenore",
		Note:           "Run by the Fart people.",
		Color:          0x4E5058,
		Status:         &profile.Status{Text: "✨ around", Activity: discord.ActivityTypeWatching, Online: discord.OnlineStatusIdle},
		SoundsPoll:     15 * time.Second,
		HTTPAddr:       "", // explicitly empty disables the ops listener, not a fallback to the default
		OTLPEndpoint:   "collector:4317",
		LogLevel:       slog.LevelWarn,
		PProf:          true,
		ShardCount:     4,
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got  %+v\nwant %+v", got, want)
	}
}

func TestEnvInterpolation(t *testing.T) {
	t.Setenv("COUCOU_TEST_TOKEN", `se"cr\et$x`)
	t.Setenv("COUCOU_TEST_EMPTY", "")
	c := mustLoad(t, `
# a commented-out ${COUCOU_TEST_NEVER_SET} is never looked up
database_url  = "postgres://h/db?sslmode=${COUCOU_TEST_EMPTY:-disable}&x=$literal"
discord_token = "${COUCOU_TEST_TOKEN}"
owner_ids     = ["${COUCOU_TEST_NEVER_SET:-`+ownerA+`}"]
[ops]
http_addr = "${COUCOU_TEST_NEVER_SET:-:8080}"
`)
	// A quote or backslash in a secret arrives as-is: expansion runs after decoding.
	if c.Token != `se"cr\et$x` {
		t.Errorf("Token = %q", c.Token)
	}
	// :- takes the default when the variable is empty as well as unset, and a bare $ is literal.
	if c.DatabaseURL != "postgres://h/db?sslmode=disable&x=$literal" {
		t.Errorf("DatabaseURL = %q", c.DatabaseURL)
	}
	if c.HTTPAddr != ":8080" {
		t.Errorf("HTTPAddr = %q", c.HTTPAddr)
	}
	if len(c.OwnerIDs) != 1 || c.OwnerIDs[0].String() != ownerA {
		t.Errorf("OwnerIDs = %v, want the expanded default", c.OwnerIDs)
	}
}

func TestUnsetVariableIsAnError(t *testing.T) {
	_, err := load(t, "database_url = \""+testDSN+"\"\ndiscord_token = \"${COUCOU_TEST_NEVER_SET}\"\n")
	if err == nil {
		t.Fatal("expected an error for an unset variable with no default")
	}
}

func TestConfigPathFromEnvironment(t *testing.T) {
	t.Setenv("COUCOU_CONFIG", writeConfig(t, minimal))
	if _, err := parseConfig(nil); err != nil {
		t.Fatalf("parseConfig: %v", err)
	}
	// -config wins over the environment.
	t.Setenv("COUCOU_CONFIG", filepath.Join(t.TempDir(), "missing.toml"))
	if _, err := parseConfig([]string{flagConfig, writeConfig(t, minimal)}); err != nil {
		t.Fatalf("parseConfig: %v", err)
	}
}

func TestValidationErrors(t *testing.T) {
	tests := []struct{ name, body string }{
		{"no database url", "discord_token = \"t\""},
		{"no token", "database_url = \"" + testDSN + "\""},
		{"unknown key", minimal + "sounds_dir = \"x\""},
		{"unknown nested key", minimal + "[ops]\nlog = \"debug\""},
		{"default_chance moved to the profile", minimal + "default_chance = 5"},
		{"sounds.dir moved to the profile", minimal + "[sounds]\ndir = \"x\""},
		{"bad duration", minimal + "[sounds]\npoll = \"soon\""},
		{"bad owner id", minimal + "owner_ids = [\"me\"]"},
		{"one bad owner id among good", minimal + "owner_ids = [\"" + ownerA + "\", \"me\"]"},
		{"negative shard count", minimal + "[gateway]\nshard_count = -2"},
		{"unknown log level", minimal + "[ops]\nlog_level = \"loud\""},
		{"pprof not a bool", minimal + "[ops]\npprof = \"yesplease\""},
		{"profile and profiles both", minimal + "profile = \"a\"\nprofiles = \"b\""},
		{"note over 1024 characters", minimal + "note = \"" + strings.Repeat("x", maxNote+1) + "\""},
		{"not toml", "database_url ="},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := load(t, tc.body); err == nil {
				t.Error("expected an error")
			}
		})
	}
}

func TestDefaultPathIsEtc(t *testing.T) {
	t.Setenv("COUCOU_CONFIG", "")
	t.Chdir(t.TempDir())
	if err := os.WriteFile("config.toml", []byte(minimal), 0o600); err != nil {
		t.Fatal(err)
	}
	// A config.toml in the working directory is not read; the error names the /etc path instead.
	_, err := parseConfig(nil)
	if err == nil || !strings.Contains(err.Error(), defaultConfigPath) {
		t.Fatalf("got %v, want an error naming %s", err, defaultConfigPath)
	}
}

func TestMissingFileIsAnError(t *testing.T) {
	if _, err := parseConfig([]string{flagConfig, filepath.Join(t.TempDir(), "nope.toml")}); err == nil {
		t.Fatal("expected an error for a missing file")
	}
}

func TestStrayArgumentIsAnError(t *testing.T) {
	if _, err := parseConfig([]string{"extra"}); err == nil {
		t.Fatal("expected an error")
	}
}

// -h is not a failure; main returns quietly after flag has printed the usage.
func TestHelpIsNotAnError(t *testing.T) {
	if _, err := parseConfig([]string{"-h"}); !errors.Is(err, flag.ErrHelp) {
		t.Fatalf("got %v, want flag.ErrHelp", err)
	}
}

// Siblings are Name=id pairs; a name may carry spaces, and anything that is not a pair is refused
// rather than silently dropped from /help.
func TestSiblings(t *testing.T) {
	const fart = "Fart"
	got, err := parseSiblings(" " + fart + "=" + ownerA + ", The Narrator =" + ownerB + " ,")
	if err != nil {
		t.Fatalf("parseSiblings: %v", err)
	}
	if len(got) != 2 || got[0].Name != fart || got[0].App.String() != ownerA ||
		got[1].Name != "The Narrator" || got[1].App.String() != ownerB {
		t.Errorf("parseSiblings = %+v", got)
	}
	for _, bad := range []string{fart, "=" + ownerA, fart + "=notanid"} {
		if _, err := parseSiblings(bad); err == nil {
			t.Errorf("parseSiblings(%q) = nil error, want one", bad)
		}
	}
}

// The shipped examples are what people copy, so they must load as they stand.
func TestExampleConfigsLoad(t *testing.T) {
	t.Setenv("DISCORD_BOT_TOKEN", testToken)
	t.Setenv("DATABASE_URL", testDSN)
	for _, path := range []string{
		"../../config.example.toml",
		"../../deployment/compose/config.example.toml",
		"../../docker/config.toml",
	} {
		t.Run(path, func(t *testing.T) {
			if _, err := parseConfig([]string{flagConfig, path}); err != nil {
				t.Errorf("parseConfig: %v", err)
			}
		})
	}
}

// The deprecated profile key still reads as the one character it always was.
func TestDeprecatedProfile(t *testing.T) {
	if got := mustLoad(t, minimal+`profile = "/srv/lenore"`); got.ProfileDir != "/srv/lenore" || got.ProfilesDir != "" {
		t.Errorf("profile: got ProfileDir %q, ProfilesDir %q", got.ProfileDir, got.ProfilesDir)
	}
	if got := mustLoad(t, minimal+`profiles = "/srv/characters"`); got.ProfileDir != "" {
		t.Errorf("profiles set: ProfileDir = %q, want the default left out", got.ProfileDir)
	}
}

// A character with its own bot joins the friends once; a sibling the config already names keeps
// the config's name.
func TestFriends(t *testing.T) {
	const lisaApp, bartApp = 11, 22
	const lisa, bart = "Lisa", "Bart Simpson"
	chars, err := characters.New([]profile.Profile{
		{ID: "bart", Dir: t.TempDir(), App: bartApp},
		{ID: "lisa", Nickname: lisa, Dir: t.TempDir(), App: lisaApp},
		{ID: "maggie", Dir: t.TempDir()},
	}, "lisa")
	if err != nil {
		t.Fatal(err)
	}
	got := friends([]commands.Sibling{{Name: bart, App: bartApp}}, chars.All())
	want := []commands.Sibling{{Name: bart, App: bartApp}, {Name: lisa, App: lisaApp}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("friends = %+v, want %+v", got, want)
	}
}
