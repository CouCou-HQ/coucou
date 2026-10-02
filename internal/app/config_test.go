package app

import (
	"errors"
	"flag"
	"log/slog"
	"slices"
	"testing"
	"time"
)

const (
	envDatabaseURL = "DATABASE_URL"
	flagDatabase   = "-database-url"
	flagChance     = "-default-chance"
	flagShards     = "-shard-count"
	flagLogLevel   = "-log-level"
	caseEnvWins    = "environment is the fallback"
	caseFlagWins   = "flag beats environment"
	levelDebug     = "debug"
	levelError     = "error"
	testDSN        = "sqlite://x.db"
	testToken      = "tok"
	envToken       = "DISCORD_BOT_TOKEN" //nolint:gosec // G101: the name of a variable, not a secret
	flagSoundsDir  = "/flag/sounds"
	flagDSN        = "postgres://from-flag/db"
	flagOwners     = "-owner-ids"
	flagToken      = "-discord-token"
	ownerA         = "987654321098765432"
	ownerB         = "876543210987654321"
)

// clearEnv blanks every variable parseConfig reads, so a test only sees what it sets itself and
// never the developer's own shell.
func clearEnv(t *testing.T) {
	t.Helper()
	for _, k := range []string{
		envDatabaseURL, envToken, "DISCORD_DEV_GUILD", "OWNER_IDS",
		"SOUNDS_DIR", "SOUNDS_POLL", "DEFAULT_CHANCE", "SHARD_COUNT", "LOG_LEVEL", "PPROF",
		"SIBLINGS",
	} {
		t.Setenv(k, "")
	}
}

// The whole point of the exercise: a flag wins, the environment is the fallback.
func TestFlagBeatsEnvironment(t *testing.T) {
	clearEnv(t)
	t.Setenv(envDatabaseURL, "postgres://from-env/db")
	t.Setenv(envToken, "token-from-env")
	t.Setenv("SOUNDS_DIR", "/env/sounds")

	c, err := parseConfig([]string{flagDatabase, flagDSN, "-sounds-dir", flagSoundsDir})
	if err != nil {
		t.Fatalf("parseConfig: %v", err)
	}
	if c.DatabaseURL != flagDSN {
		t.Errorf("DatabaseURL = %q, want the flag value", c.DatabaseURL)
	}
	if c.SoundsDir != flagSoundsDir {
		t.Errorf("SoundsDir = %q, want the flag value", c.SoundsDir)
	}
	// Not passed as a flag, so it must come from the environment.
	if c.Token != "token-from-env" {
		t.Errorf("Token = %q, want the environment value", c.Token)
	}
}

func TestEnvironmentIsUsedWhenNoFlagGiven(t *testing.T) {
	clearEnv(t)
	t.Setenv(envDatabaseURL, "sqlite:///tmp/x.db")
	t.Setenv(envToken, testToken)
	t.Setenv("OWNER_IDS", ownerA+", "+ownerB+" ,")
	t.Setenv("SOUNDS_POLL", "15s")
	t.Setenv("DEFAULT_CHANCE", "25")

	c, err := parseConfig(nil)
	if err != nil {
		t.Fatalf("parseConfig: %v", err)
	}
	if c.DatabaseURL != "sqlite:///tmp/x.db" || c.Token != testToken {
		t.Errorf("got %+v", c)
	}
	if c.SoundsPoll != 15*time.Second {
		t.Errorf("SoundsPoll = %s, want 15s", c.SoundsPoll)
	}
	if c.DefaultChance != 25 {
		t.Errorf("DefaultChance = %d, want 25", c.DefaultChance)
	}
}

// Owners are a list: several ids, comma separated, with the blanks and spacing a hand-edited
// environment variable picks up along the way.
func TestOwnerIDsAreAList(t *testing.T) {
	clearEnv(t)
	t.Setenv(envDatabaseURL, testDSN)
	t.Setenv(envToken, testToken)
	t.Setenv("OWNER_IDS", ownerA+", "+ownerB+" ,")

	c, err := parseConfig(nil)
	if err != nil {
		t.Fatalf("parseConfig: %v", err)
	}
	want := []string{ownerA, ownerB}
	got := make([]string, len(c.OwnerIDs))
	for i, id := range c.OwnerIDs {
		got[i] = id.String()
	}
	if !slices.Equal(got, want) {
		t.Errorf("OwnerIDs = %v, want %v", got, want)
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

func TestDefaultsWhenNothingIsSet(t *testing.T) {
	clearEnv(t)
	c, err := parseConfig([]string{flagDatabase, testDSN, flagToken, testToken})
	if err != nil {
		t.Fatalf("parseConfig: %v", err)
	}
	if c.SoundsDir != "sounds" {
		t.Errorf("SoundsDir = %q, want sounds", c.SoundsDir)
	}
	if c.SoundsPoll != 0 {
		t.Errorf("SoundsPoll = %s, want 0 (inotify)", c.SoundsPoll)
	}
	if c.DefaultChance != defaultChance {
		t.Errorf("DefaultChance = %d, want %d", c.DefaultChance, defaultChance)
	}
	if len(c.OwnerIDs) != 0 {
		t.Errorf("OwnerIDs = %v, want none — a bot with no owner has no owner-only reports", c.OwnerIDs)
	}
}

func TestValidationErrors(t *testing.T) {
	withDB := map[string]string{envDatabaseURL: testDSN, envToken: testToken}
	tests := []struct {
		name string
		env  map[string]string
		args []string
	}{
		{"no database url", nil, nil},
		{"no token for a normal run", map[string]string{envDatabaseURL: testDSN}, nil},
		{"chance above 100", withDB, []string{flagChance, "101"}},
		{"chance not a number", withDB, []string{flagChance, "loads"}},
		{"bad duration", withDB, []string{"-sounds-poll", "soon"}},
		{"bad owner id", withDB, []string{flagOwners, "me"}},
		{"one bad owner id among good", withDB, []string{flagOwners, ownerA + ",me"}},
		{"stray argument", withDB, []string{"extra"}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			clearEnv(t)
			for k, v := range tc.env {
				t.Setenv(k, v)
			}
			if _, err := parseConfig(tc.args); err == nil {
				t.Error("expected an error")
			}
		})
	}
}

// 0 is the normal answer and means "let Discord decide", so it has to survive as 0 rather than
// being treated as unset-and-defaulted to something else.
func TestShardCount(t *testing.T) {
	tests := []struct {
		name string
		args []string
		env  string
		want int
	}{
		{"unset lets Discord decide", nil, "", 0},
		{"explicit zero lets Discord decide", []string{flagShards, "0"}, "", 0},
		{"flag overrides", []string{flagShards, "4"}, "", 4},
		{caseEnvWins, nil, "3", 3},
		{caseFlagWins, []string{flagShards, "4"}, "9", 4},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			clearEnv(t)
			t.Setenv(envDatabaseURL, "sqlite://x.db")
			t.Setenv(envToken, "t")
			if tc.env != "" {
				t.Setenv("SHARD_COUNT", tc.env)
			}
			c, err := parseConfig(tc.args)
			if err != nil {
				t.Fatalf("parseConfig: %v", err)
			}
			if c.ShardCount != tc.want {
				t.Errorf("ShardCount = %d, want %d", c.ShardCount, tc.want)
			}
		})
	}
}

func TestShardCountRejectsNegative(t *testing.T) {
	clearEnv(t)
	t.Setenv(envDatabaseURL, "sqlite://x.db")
	t.Setenv(envToken, "t")
	if _, err := parseConfig([]string{flagShards, "-2"}); err == nil {
		t.Error("expected an error for a negative shard count")
	}
}

// slog.Level is a TextUnmarshaler, so the parsing is stdlib's — this pins that the flag reaches it
// and that the default stays info, since raising it quietly would change what a deploy logs today.
func TestLogLevel(t *testing.T) {
	tests := []struct {
		name string
		args []string
		env  string
		want slog.Level
	}{
		{"defaults to info", nil, "", slog.LevelInfo},
		{"flag sets debug", []string{flagLogLevel, levelDebug}, "", slog.LevelDebug},
		{"case does not matter", []string{flagLogLevel, "WARN"}, "", slog.LevelWarn},
		{caseEnvWins, nil, levelError, slog.LevelError},
		{caseFlagWins, []string{flagLogLevel, levelDebug}, levelError, slog.LevelDebug},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			clearEnv(t)
			t.Setenv(envDatabaseURL, testDSN)
			t.Setenv(envToken, testToken)
			if tc.env != "" {
				t.Setenv("LOG_LEVEL", tc.env)
			}
			c, err := parseConfig(tc.args)
			if err != nil {
				t.Fatalf("parseConfig: %v", err)
			}
			if c.LogLevel != tc.want {
				t.Errorf("LogLevel = %v, want %v", c.LogLevel, tc.want)
			}
		})
	}
}

func TestLogLevelRejectsNonsense(t *testing.T) {
	clearEnv(t)
	t.Setenv(envDatabaseURL, testDSN)
	t.Setenv(envToken, testToken)
	if _, err := parseConfig([]string{flagLogLevel, "loud"}); err == nil {
		t.Error("expected an error for an unknown level")
	}
}

// -h is not a failure; main returns quietly after flag has printed the usage.
func TestHelpIsNotAnError(t *testing.T) {
	clearEnv(t)
	if _, err := parseConfig([]string{"-h"}); !errors.Is(err, flag.ErrHelp) {
		t.Fatalf("got %v, want flag.ErrHelp", err)
	}
}

// envOn is the truthy value these tests set; strconv.ParseBool takes several spellings, and "1"
// below is there to prove the parse is not a string comparison.
const envOn = "true"

// Profiling is off unless asked for: the endpoints expose allocation sites and goroutine stacks,
// and /debug/pprof/profile costs 30 s of CPU sampling per request.
func TestPProfDefaultsOff(t *testing.T) {
	clearEnv(t)
	c, err := parseConfig([]string{flagDatabase, flagDSN, flagToken, testToken})
	if err != nil {
		t.Fatalf("parseConfig: %v", err)
	}
	if c.PProf {
		t.Error("PProf is on with neither flag nor environment set")
	}
}

func TestPProfFromFlagAndEnvironment(t *testing.T) {
	tests := []struct {
		name string
		env  string
		args []string
		want bool
	}{
		{"flag on", "", []string{"-pprof=" + envOn}, true},
		{"flag off beats env on", envOn, []string{"-pprof=false"}, false},
		{"env on", envOn, nil, true},
		{"env accepts 1", "1", nil, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			clearEnv(t)
			if tt.env != "" {
				t.Setenv("PPROF", tt.env)
			}
			args := append([]string{flagDatabase, flagDSN, flagToken, testToken}, tt.args...)
			c, err := parseConfig(args)
			if err != nil {
				t.Fatalf("parseConfig: %v", err)
			}
			if c.PProf != tt.want {
				t.Errorf("PProf = %v, want %v", c.PProf, tt.want)
			}
		})
	}
}

// A value that is not a bool is a misconfiguration, not a quiet off.
func TestPProfRejectsNonsense(t *testing.T) {
	clearEnv(t)
	if _, err := parseConfig([]string{flagDatabase, flagDSN, flagToken, testToken, "-pprof=yesplease"}); err == nil {
		t.Fatal("parseConfig accepted -pprof=yesplease")
	}
}
