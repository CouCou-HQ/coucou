<h1 align="center">coucou</h1>

<p align="center"><b>It waits in the wings. Then, from nowhere: <i>coucou!</i></b></p>

<p align="center">
  <a href="go.mod"><img src="https://img.shields.io/badge/Go-1.26-00ADD8?logo=go&logoColor=white" alt="Go 1.26"></a>
  <a href="https://github.com/be-sandaa/coucou/releases/latest"><img src="https://img.shields.io/badge/release-v0.1.0-E4572E" alt="release v0.1.0"></a>
  <a href="https://github.com/be-sandaa/coucou/pkgs/container/coucou"><img src="https://img.shields.io/badge/image-ghcr.io%2Fbe-sandaa%2Fcoucou-2496ED?logo=docker&logoColor=white" alt="image ghcr.io/be-sandaa/coucou"></a>
  <a href="#memory"><img src="https://img.shields.io/badge/RSS%20target-25%20MB%20%40%201.6k%20guilds-4E5058" alt="RSS target 25 MB at 1.6k guilds"></a>
</p>

*Coucou!* is French for "peekaboo", or a cheeky "hi there". That is the entire product.

coucou is the application behind Discord bots that pop into a busy voice channel, play one short
sound and leave. It is not a character itself: the bot people meet has its own name, Discord app and
sounds, and coucou is what runs it.

Every 5 minutes a bot rolls the dice for each server that has given it a chance. When it wins, it
slips into the busiest voice channel with a human in it, maybe sits there in suspicious silence for
a few seconds, plays one short sound, and leaves before anyone can react. Then everybody blames
each other.

Once it is in your server: `/chance` sets how often it visits, `/optout` hides you from it,
`/stats` and `/leaderboard` show who suffers the most, and `/help` explains the rest. Every command
is in [Commands](#commands).

Some sounds are **rare**: they turn up a tenth as often, can't be asked for with `/play`, and count
towards your collection in `/stats` once a bot catches you with one.

## Bring your own bot

coucou doesn't care who it plays. Give it a [profile](#profile) — a character with its own sounds —
and you have a new bot: one binary, your character, your Discord app, your database. Nothing in the
code knows which bot it is.

1. **Write the character.** Copy [`profile.example.toml`](profile.example.toml) into a profile
   directory as `profile.toml`: an id, an optional nickname, the tagline, lore and traits `/about`
   shows, and the settings a server starts with. See [Profile](#profile).
2. **Make the sounds.** Run `scripts/sound` on a folder of short clips: it encodes them to Ogg Opus
   and names them in snake_case (`Wet Fart 3.mp3` becomes `wet_fart_3.ogg`, shown as
   **Wet Fart 3**), into the profile's `sounds/`. Add `--rare` for the special ones. See [Sounds](#sounds).
3. **Run it.** A [release binary](#install), the [container image](#container) or the
   [Helm chart](deployment), with `profile` pointing at that directory and a token from your own
   Discord application.
4. **Get it listed.** Open a [**List my bot**](https://github.com/be-sandaa/coucou/issues/new?template=list_my_bot.yml)
   issue, and it joins the table below.

To be listed, a bot has to:

- run on your own Discord application, with sounds you have the right to use;
- follow Discord's [Terms of Service](https://discord.com/terms) and
  [Developer Policy](https://support-dev.discord.com/hc/en-us/articles/8563934450327-Discord-Developer-Policy).

> [!NOTE]
> The repository, the image and the release binaries are private for now, so this is the plan for
> when they open up rather than something you can do today.

### Community bots

| Bot | What it does | Maintainer | |
|---|---|---|---|
| *yours here* | | | |

---

Everything from here down is for running a bot on coucou yourself, or hacking on coucou.

## Install

Every `v*` tag publishes static binaries for linux and macOS, amd64 and arm64:

```sh
tag=v0.1.0
base=https://github.com/be-sandaa/coucou/releases/download/$tag
curl -fsSLO "$base/coucou_${tag}_linux_amd64.tar.gz"
curl -fsSLO "$base/coucou_${tag}_SHA256SUMS"
sha256sum --check --ignore-missing "coucou_${tag}_SHA256SUMS"
tar -xzf "coucou_${tag}_linux_amd64.tar.gz"
./coucou -h
```

There is nothing to install beside it: CGO is off and the timezone database is embedded, so the
binary is the entire dependency.

The same tag also publishes a multi-arch image, `linux/amd64` and `linux/arm64`. Image tags drop
the leading `v`, so the git tag `v0.1.0` is the image tag `0.1.0`:

```sh
docker pull ghcr.io/be-sandaa/coucou:0.1.0
```

And the Helm chart, as an OCI artifact on the same registry and at the same version:

```sh
helm install coucou oci://ghcr.io/be-sandaa/charts/coucou --version 0.1.0
```

For running it under compose or Kubernetes, see [deployment/](deployment).

## Build and run

Requires Go 1.26 (see [NOTES.md](NOTES.md) — the pinned dependency set forces it).

```sh
make build          # vet + test, then CGO_ENABLED=0 static binary into bin/coucou
make build-fast     # skip the checks
make test           # both store backends; add TEST_DATABASE_URL for the Postgres half
make lint           # go vet + golangci-lint
make gen            # regenerate internal/store/{pg,sqlite}/gen — commit the result
make image          # Podman build of the scratch image
make help           # everything else
```

Running it:

```sh
./bin/coucou -config config.toml   # migrate, register the slash commands, load settings, watch the sounds dir, connect
./bin/coucou                       # installed: reads /etc/coucou/config.toml
```

Migrations, command registration and the version stamp are not separate modes: the bot applies its
migrations and registers its commands on the way up, and prints the build it came from in the
banner once Discord answers.

Or via make: `make dev`, which runs the working tree as a development build — its commands go to
`DISCORD_DEV_GUILD` rather than to every server the bot is in. `make run SERVICE=coucou TAGS=dev`
is the same thing the long way round.

### Configuration

Everything is set in one TOML file: `-config <path>`, else `COUCOU_CONFIG`, else
`/etc/coucou/config.toml`. The working directory is never searched, so a service can't pick up a
stray file. `make dev` points at the checkout's `config.toml`. Copy
[`config.example.toml`](config.example.toml), which lists every key. Secrets stay out of the
file: any string value may reference the environment as `${VAR}`, or `${VAR:-default}` to fall back
when it is unset or empty.

```toml
discord_token = "${DISCORD_BOT_TOKEN}"
database_url  = "${DATABASE_URL}"
```

An unset `${VAR}` with no default stops startup rather than running with an empty token. So does an
unknown key. Expansion runs on the decoded values, not the raw file, so a secret containing a quote
or a backslash arrives intact, a bare `$` is literal, and a `${VAR}` in a comment is never read. It
also means numbers and booleans cannot come from the environment: write them out.

| key | default | what |
|---|---|---|
| `database_url` | — (required) | connection string; the scheme picks the backend |
| `discord_token` | — (required) | bot token |
| `owner_ids` | `[]` | user ids, as strings, that unlock the servers leaderboard |
| `siblings` | `""` | `Name=application-id` pairs of other coucou bots, comma separated; `/help` links each one but itself under **More from coucou**, and about 1 in 100 `/play` replies plugs one at random |
| `profile` | `/var/lib/coucou/profile` | the character: a directory holding `profile.toml` and `sounds/`; see [Profile](#profile) |
| `sounds.poll` | `0s` | rescan interval for the profile's `sounds/`; `0s` uses inotify |
| `ops.http_addr` | `:9090` | `/healthz`, `/readyz` and `/metrics`; `""` disables them |
| `ops.log_level` | `info` | `debug` \| `info` \| `warn` \| `error`; audit records are stored regardless |
| `ops.pprof` | `false` | serve `/debug/pprof` on the ops listener |
| `ops.otlp` | `""` | `host:port` of an OTLP/gRPC collector; `""` disables tracing |
| `gateway.shard_count` | `0` | shards to run; `0` lets Discord decide, which is what a deploy should do |

One setting stays an environment variable: `DISCORD_DEV_GUILD`, read only by a `-tags dev` build,
is the guild that build registers its commands to.

### Profile

Who the bot is lives in one directory, the `profile` key above: `profile.toml` and the `sounds/` it
plays. Copy [`profile.example.toml`](profile.example.toml), which lists every key. Nothing in it is
pushed to Discord — the bot's username, avatar and banner stay whatever the developer portal says.

| key | default | what |
|---|---|---|
| `id` | — (required) | lowercase letters, digits and dashes |
| `nickname` | `""` | what replies call the bot. Wins over its nickname in a server, which wins over its Discord name |
| `emoji` | `""` | in front of the `/help` and `/about` titles |
| `color` | `#E4572E` | embed accent for reports and confirmations, `#RRGGBB` |
| `tagline`, `lore` | `""` | shown by `/about` |
| `traits` | `[]` | character quirks, listed by `/about`. Something to read; they never change what the bot does |
| `status.text` | `""` | shown under the bot's name, up to 128 characters. Emoji go in the text: bots get no separate emoji slot or server emoji |
| `status.activity` | `custom` | `custom` shows the text as is; `playing`, `listening`, `watching`, `competing` prefix it the way Discord does |
| `status.online` | `online` | `online`, `idle` or `dnd` |
| `defaults.chance` | `5` | join chance a server starts with, 0–100 |
| `defaults.suspense` | `0` | seconds of silence before the sound, 0–20 |
| `defaults.fakeout` | `0` | % of visits that leave without a sound, 0–50 |
| `defaults.encore` | `0` | % of visits that come back for a second sound, 0–50 |
| `[[chains]]` | none | sounds played in one visit, `steps` in order with `after` seconds of silence (0–20) before each; `chance` % (default 100) that the rest follow the first |
| `[[links]]` | none | what an encore plays after `from`: one of `to`, by weight, instead of any other sound |

`defaults.chance` is a percentage rolled once every 5 minutes, and only on ticks where somebody is
actually in a voice channel — so 5 works out to roughly one visit per 1.5–2 hours of active voice.
Set it to `0` to make the bot opt-in. The defaults seed servers that have no settings yet; a server
overrides each with its command, and seeding never touches a server that already has settings, so a
changed default only reaches servers the bot joins afterwards.

`profile.toml` is read once at startup and an invalid one stops the bot; a change needs a restart.
Only `sounds/` reloads live. From a checkout, `profile/` is git-ignored: start it with
`mkdir -p profile/sounds && cp profile.example.toml profile/profile.toml`, and set
`profile = "profile"` in your `config.toml`.

### Container

```sh
make image                                            # or:
docker build -t coucou -f docker/Dockerfile .
docker run --rm -e DATABASE_URL=... -e DISCORD_BOT_TOKEN=... -v ./profile:/var/lib/coucou/profile:ro coucou
```

The image carries a minimal config at the default path, `/etc/coucou/config.toml` (from
[`docker/config.toml`](docker/config.toml)): token and DSN from the environment, the profile at the
default `/var/lib/coucou/profile`. `/var/lib/coucou` is owned by the runtime user, so a volume there
can hold a SQLite database too.
Mount your own file over it for anything else.

`FROM scratch`, non-root (65534), ~31 MiB. The zoneinfo database is embedded in the binary via
`time/tzdata`, so quiet hours work with no files in the image and there is nothing to copy but the
binary and the TLS roots.

Run it against a PostgreSQL 17 container, one database and role per bot, with the profile directory
mounted at whatever `profile` points to. Set `GOMEMLIMIT=48MiB` in the unit.

### Sounds

Pre-encoding a sound (the bot does no encoding at runtime — no ffmpeg, no Opus encoder) is one
command. `scripts/sound` takes files or folders, encodes everything it finds, and names the results
in snake_case for you:

```sh
scripts/sound "Wet Fart 3.mp3" ~/Downloads/farts/   # -> profile/sounds/wet_fart_3.ogg, ...
scripts/sound --rare "Perfect Fart.wav"             # -> profile/sounds/perfect_fart.rare.ogg
scripts/sound --nsfw --rare "Ufufu.wav"             # -> profile/sounds/ufufu.rare.nsfw.ogg
scripts/sound -o /srv/lenore/sounds -n ~/Downloads/farts/  # dry run into another profile
```

It writes to the checkout's `profile/sounds` (or `-o`), skips anything that already exists unless `-f`, and needs
only bash and ffmpeg. Underneath it is this, with `-vn` because an mp3's embedded cover art would
otherwise ride along as a video stream and the bot would reject the file:

```sh
ffmpeg -i in.mp3 -vn -map_metadata -1 -c:a libopus -b:a 64k -ar 48000 -ac 2 out.ogg
```

Drop the `.ogg` into the profile's `sounds/` and it is playable within ~1.5 s. No restart. Half-copied or
non-Opus files are ignored until they are valid.

Name files in lowercase snake_case: `wet_fart_2.ogg` is the sound `wet_fart_2`, and Discord
shows it as **Wet Fart 2** — underscores become spaces and each word gets a capital. The file name
is the sound's identity in the stats, so renaming a file starts a new history; migration 00018
moved the old `Wet Fart 2` / `big-burp` spellings over once.

Tags are in the filename, between the name and `.ogg`, in any order: `<name>.rare.ogg`,
`<name>.nsfw.ogg`, and `<name>.rare.nsfw.ogg` (the same as `<name>.nsfw.rare.ogg`) all load as the
sound `<name>`, so tagging or untagging one is a rename and its history stays under one name.
`scripts/sound` keeps the tags a source already has (`-r`/`--rare`, `-x`/`--nsfw` add them) and
always writes them as `.rare.nsfw`. Wherever a sound is shown, its tags follow the name as ✨ for
rare and 🔞 for nsfw: `perfect_fart.rare.ogg` is **Perfect Fart ✨**.

- **rare** turns up a tenth as often, only by roll: it is left out of `/play`'s autocomplete and
  cannot be asked for by name.
- **nsfw** plays only where Discord has both the server and the voice channel age-restricted (a
  server at the *age-restricted* or *explicit* level). Everywhere else it is never rolled, never in
  the autocomplete, and cannot be asked for by name. There is no bot setting for it; `/help` says
  whether it is on in a server. A sound tagged both follows both rules.

Any other segment is part of the name (`foo.v2.ogg` is `foo.v2`), and one that looks like a typo of
a tag (`foo.nswf.ogg`) loads that way with a warning. Tags do not make a name unique: if several
files back one name, the most-tagged one wins (`nsfw` outranks `rare`), the others are ignored with
a warning, and deleting the winner falls back to the next. `/stats scope:user` shows your
collection: the distinct sounds the bot's own visits have caught you with, out of what is loaded
now and can play in this server. `/play` does not count toward it.

#### Sound emojis

An app emoji named exactly like a sound is that sound's emoji: upload `perfect_fart` under the
application's **Emojis** in the developer portal and the sound `perfect_fart` shows it, tags and all
(the emoji is named after the sound, never after `.rare` or `.nsfw`). It is shown in the `/play` reply
in place of the speaker, before the top sound in `/stats`, and on the `/leaderboard` sounds board.
`/sounds` and the autocomplete stay text: fifty mentions would overrun a page, and autocomplete
cannot render one.

A sound with no emoji of its name is shown exactly as before, so they can be added one at a time.
Discord tells the bot nothing when an app emoji is added or deleted, so it lists them every ten
minutes. A new emoji appears within that, and a deleted one may still be mentioned until then.

## Commands

| command | who | does |
|---|---|---|
| `/play [sound]` | anyone | play now in your channel; autocompletes over the live registry |
| `/sounds [page]` | anyone | every sound `/play` will take from you, 50 a page, past the autocomplete's 25 (only you see it) |
| `/leave` | anyone | cut a play short |
| `/help` | anyone | the command list, and which switch actually keeps the bot out |
| `/about` | anyone | who the bot is: its tagline, lore and traits from the profile (only you see it) |
| `/optout on\|for\|schedule\|rrule\|off` | anyone, per person | stop being counted when the bot picks a channel: indefinitely, `for <hours>`, or on a repeating `schedule` (an RFC 5545 rule, typed directly with `rrule`) |
| `/chance <0-100>` | Manage Server | odds of a drop-in every 5 minutes (0 = never) |
| `/quiet on\|for\|schedule\|rrule\|off` | Manage Server | leave the server alone: indefinitely, `for <hours>`, or on a repeating `schedule` — the same shapes as `/optout` |
| `/suspense <0-20>` | Manage Server | seconds of silence before the sound |
| `/status` | anyone | what the bot thinks about this server |
| `/stats user\|guild\|bot` | anyone | play statistics with 30-day ranks and charts — plays a day, a week-by-hour heatmap in the server's zone, how visits began and ended: yours (only you see it; here and across every server), this server's, or bot-wide (both posted in the channel) |
| `/leaderboard board period public` | anyone (`guilds` board: owner) | who suffers the most |

## How a play happens

The loop decides *what*; the bus carries it; the voice worker decides *when it can*. The handler
returns as soon as it has one of eight slots, so the bus is never held open for the length of a
play — which is also why shutdown has to drain the detached bodies rather than just the router.

```mermaid
sequenceDiagram
    autonumber
    participant Sched as five-minute loop
    participant Cache as disgo cache
    participant Bus as bus
    participant Worker as voice worker
    participant Discord

    Sched->>Sched: roll the dice, check quiet hours and busy
    Sched->>Cache: busiest usable channel (no REST)
    Cache-->>Sched: channel + headcount
    Sched->>Bus: PlayRequested
    Bus->>Worker: deliver
    Note over Bus,Worker: the handler takes one of 8 slots and returns —<br/>the play itself runs detached
    Worker->>Discord: join
    Discord-->>Worker: DAVE handshake complete
    Worker->>Worker: suspense, then re-check the room is populated
    Worker->>Discord: one Ogg Opus clip, then leave
    Worker->>Bus: PlayFinished
```

Choosing where to drop in is cache-only and spread over three places — the dice and the clock in
`internal/bot/loop.go`, the channel rules in `voice.Usable`, the policy in `voice.Best`:

```mermaid
flowchart TD
    T[five-minute tick] --> C{chance roll won?}
    C -- no --> S[skip this guild]
    C -- yes --> Q{inside quiet hours?}
    Q -- yes --> S
    Q -- no --> P{already playing here?}
    P -- yes --> S
    P -- no --> E[for each cached channel]

    E --> A{the guild's AFK channel?}
    A -- yes --> N[not a candidate]
    A -- no --> U{"voice channel? (never a stage)"}
    U -- no --> N
    U -- yes --> H{anyone in it?}
    H -- no --> N
    H -- yes --> M{"bot has View + Connect + Speak?"}
    M -- no --> N
    M -- yes --> O[subtract people who ran /optout on]
    O --> W[busiest channel left wins]
```

Two of those are deliberate policy rather than capability, and both are in `Best` rather than
`Usable` so that `/play` and `/status` are unaffected: the AFK channel is skipped because a full
one means nobody is listening, and opted-out people are subtracted from the headcount but not
hidden — a room where somebody else is present is still fair game.

## Layout

```
cmd/coucou               one func: run the app, turn its error into an exit code
internal/app             composition root: TOML config, boot order, the bus fan-out map
internal/ops             /healthz, /readyz and /metrics
internal/store           Store interface + DATABASE_URL scheme registry + WaitAndMigrate
internal/store/pg        pgx/v5, goose under pg_advisory_lock, sqlc postgresql engine
internal/store/sqlite    modernc.org/sqlite (pure Go, WAL), goose sqlite3, sqlc sqlite engine
internal/profile         the character: profile.toml, its defaults and limits, and where its sounds are
internal/settings        per-guild chance / suspense / zone, in-memory over the store
internal/silence         per-user opt-outs and per-guild quiet: always or on an RFC 5545 rule, either with an end
internal/sounds          live Ogg Opus registry (fsnotify + rescan, OpusHead sniff, settle check)
internal/voice           join -> DAVE-ready -> suspense -> play -> leave, and channel selection
internal/events          buffered stats log, batched through the store
internal/rollup          refreshes the store's rolled-up analytics every 15 minutes
internal/bus             typed events, in-process gochannel bus, JSON codec, middleware
internal/bus/handlers    the bus consumers: one constructor per handler, no Discord in sight
internal/bot             the Discord client: intents, cache policy, DAVE, thin gateway listeners,
                         and the five-minute scheduler that asks the bus to play somewhere
internal/commands        slash commands: definitions, dispatcher, handlers, and their deploy
internal/validate        struct-tag validation, applied to every event on both sides of the wire
internal/metrics         Prometheus metrics, served on -http-addr with /healthz + /readyz
internal/tracing         OpenTelemetry setup; off unless -otlp-endpoint is given
pkg/run                  process runner: start concurrently, stop in reverse on signal or error
docker/                  the scratch image build
```

### Database

`internal/store` is one interface; the backend is chosen by the `DATABASE_URL` scheme:

- `postgres://` / `postgresql://` → `store/pg`
- `sqlite://`, `sqlite:`, `file:` → `store/sqlite`

Backends register themselves in `init()`; `internal/app` blank-imports the ones to compile in.
**goose** owns each backend's schema (embedded, applied at boot, Postgres under an advisory lock).
**sqlc** owns each backend's queries, one engine per backend. The SQL is allowed to differ
completely — Postgres batches with `unnest` + `COPY`, SQLite loops single-row inserts in one
transaction. Adding a column means two migrations and two query edits; the reward is
`DATABASE_URL=sqlite:///var/lib/coucou/coucou.db` for a single-box deploy with no database to run.

`make gen` after touching any `.sql`, and commit `gen/`. CI fails if regenerating produces a diff.

**Analytics.** On Postgres the `stats` schema carries views for ad-hoc SQL and Grafana as well as
for the bot: `stats.listens` (a listener joined to their play), `stats.play_outcomes` (played /
fakeout / failed), and `stats.plays_hourly` / `stats.listeners_hourly`, which read UTC-hour rollups
held in materialized views (`*_mv`) plus a live count of every play the rollup has not seen yet —
so they are exact whenever you query them, and `internal/rollup` only decides how much is counted
on read. SQLite counts the same series on read.

Both backends are held to the same behavioural suite in `internal/store/store_test.go`. SQLite runs
always; Postgres runs when `TEST_DATABASE_URL` is set:

```sh
TEST_DATABASE_URL='postgres://user:pass@localhost:5432/scratch?sslmode=disable' go test ./internal/store/
```

### Event bus

Every state change is a typed event on an in-process bus (`internal/bus`): `PlayRequested`,
`PlayFinished`, `CommandInvoked`, `GuildJoined`/`GuildLeft`, `SettingsChanged`,
`SoundAdded`/`SoundRemoved`. Producers — gateway handlers, the loop, the sounds registry — know
nothing about consumers. Consumers are registered in `internal/bus/handlers/handlers.go`; that file
*is* the architecture diagram.

`bus.New()` takes no configuration: the bus is watermill's gochannel — in-process, at-most-once,
zero infra. **Handler names label the bus metrics and spans**, so keep them stable across releases.
Nothing may publish before `<-bus.Running()`.

Middleware: correlation IDs, panic recovery, 3× retry with backoff, 90 s handler timeout.

### Memory

The whole design serves the RSS target: intents are `Guilds | GuildVoiceStates` only; the cache
holds guilds, roles, voice states, **voice-type channels only**, and only members that are the bot
itself or currently in voice; gateway compression is off and `large_threshold` is 50; channel
selection is cache-only with no REST calls; sounds are pre-encoded so there is no encoder in the
process. `GOMEMLIMIT` and `GOGC` are read from the environment.

## Provenance

Started from a Go service template. The build plumbing (`makefile`, CI, lint config) still follows
it, and coucou's own make targets live in `makefile.local` so a sync from the template has nothing of
ours to overwrite.

[NOTES.md](NOTES.md) also records every library API mismatch found while getting this to compile,
and why each was resolved the way it was.

## License

[MIT](LICENSE).
