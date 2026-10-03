# Architecture

How coucou is put together, for hacking on it. Running a bot needs none of this.

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
    Worker->>Worker: wait out the join chime (1 s, less any suspense)
    Worker->>Discord: the clip, or a chain's clips, then leave
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

## Database

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

## Event bus

Every state change is a typed event on an in-process bus (`internal/bus`): `PlayRequested`,
`PlayFinished`, `CommandInvoked`, `GuildJoined`/`GuildLeft`, `SettingsChanged`,
`SoundAdded`/`SoundRemoved`. Producers — gateway handlers, the loop, the sounds registry — know
nothing about consumers. Consumers are registered in `internal/bus/handlers/handlers.go`; that file
*is* the architecture diagram.

`bus.New()` takes no configuration: the bus is watermill's gochannel — in-process, at-most-once,
zero infra. **Handler names label the bus metrics and spans**, so keep them stable across releases.
Nothing may publish before `<-bus.Running()`.

Middleware: correlation IDs, panic recovery, 3× retry with backoff, 90 s handler timeout.

## Memory

The whole design serves the RSS target: intents are `Guilds | GuildVoiceStates` only; the cache
holds guilds, roles, voice states, **voice-type channels only**, and only members that are the bot
itself or currently in voice; gateway compression is off and `large_threshold` is 50; channel
selection is cache-only with no REST calls; sounds are pre-encoded so there is no encoder in the
process. `GOMEMLIMIT` and `GOGC` are read from the environment.
