# Notes

Every API mismatch hit while getting coucou to compile against its pinned library versions, and
how it was resolved. Written so the next person does not repeat the archaeology.

All findings come from reading the module cache
(`$(go env GOMODCACHE)/github.com/disgoorg/disgo@v0.19.6`, `.../godave@v0.2.0`,
`.../dave-go@v0.5.1`), not from guessing.

---

## Go 1.26 is the floor

The pinned dependency set rules out Go 1.25: these all declare `go 1.26.0` in their own `go.mod`, so
the main module's directive is forced to at least 1.26:

| module | requires |
|---|---|
| `github.com/thomas-vilte/dave-go` v0.5.1 | go 1.26.0 |
| `github.com/thomas-vilte/mls-go` v1.6.0 | go 1.26.0 |
| `github.com/pressly/goose/v3` v3.28.0 | go 1.26.0 |
| `golang.org/x/{crypto,sys,text,sync,mod,exp}` | go 1.26.0 |

dave-go is mandatory (DAVE is required for voice), so there is no version of this bot that builds on
Go 1.25. `go.mod` says `go 1.26.0` and the Dockerfile builds on `golang:1.26-alpine`. The build is
still `CGO_ENABLED=0`, a static binary, `FROM scratch`, with no cgo anywhere.

---

## disgo v0.19.6

### `voice.Conn` has no `DAVE()` method

The obvious way to wait out the MLS handshake is `conn.DAVE().Ready()`. That method does not
exist. disgo builds the DAVE session inside `voice.NewConn` (`voice/conn.go:79`) and hands it
straight to the gateway and UDP conn; it is exposed nowhere on the `Conn` interface.

Waiting is genuinely necessary: `udp_conn.go:275` calls `daveSession.Encrypt` unconditionally, and
godave documents that a not-ready session passes frames through **unencrypted** rather than erroring.
So frames sent before the handshake completes go out in the clear.

The one hook disgo does give us is the session constructor, and godave passes it the conn itself as
the `Callbacks` argument — so the conn is usable as a map key. `internal/voice/dave.go` registers
each session as it is created and `waitDave` looks it up. `Play`'s deferred `conn.Close` removes the
entry; `Close` also calls `removeConnFunc`, so disgo drops the conn at the same moment and the two
stay in step.

### `dave-go`'s `session.New` does not match `godave.SessionCreateFunc`

```go
// godave.SessionCreateFunc
func(logger *slog.Logger, userID UserID, callbacks Callbacks) Session
// dave-go session.New
func New(userID godave.UserID, callbacks godave.Callbacks, opts ...Option) *Session
```

Different argument order, and the logger is an option rather than a parameter. So
`voice.WithDaveSessionCreateFunc(session.New)` does not compile. `internal/voice.SessionCreateFunc`
is the adapter (`session.WithLogger(logger)` carries the logger), and it doubles as the registration
point above.

### `omit.Ptr` is the wrong constructor

`discord.SlashCommandCreate.DefaultMemberPermissions` is `omit.Omit[*discord.Permissions]`.
`omit.Ptr(&manageGuild)` is `func Ptr[T any](v T) *T`, which yields `**discord.Permissions`.
The right one is `omit.NewPtr[T any](v T) Omit[*T]` — so `omit.NewPtr(manageGuild)`.

### `GuildsReady` is not per-shard, and cannot be counted

`bot/handlers/guild_handlers.go:83` dispatches `events.GuildsReady` when
`len(client.Caches.UnreadyGuildIDs()) == 0`. That set is client-wide, not per-shard, so with more
than one shard the event has no usable arity:

- it fires **early** — shard 0 drains its guilds before shard 1 has identified, so the unready set
  is momentarily empty and the event fires carrying only shard 0's guilds;
- it fires **fewer times than there are shards** — if every READY lands before any shard finishes
  draining, the set empties once and the event fires once;
- it fires **never** for a shard holding no guilds, which sends no GUILD_CREATE at all.

`GenericEvent.ShardID()` says which shard dispatched it, but no count of those events is right, so
there is nothing to wait for. This matters because the guild reconcile calls
`MarkGuildsLeftExcept`, which treats anything absent from the cache as a guild the bot was removed
from — run it on one shard's worth and it marks every other shard's guilds as left, publishing a
`GuildLeft` for each.

So the reconcile is not an event listener. `ShardManager.Open` blocks until every shard has had its
READY (`gateway.Open` waits on a ready channel, `gateway/gateway.go:286`), so `internal/app` calls
`bot.SyncGuilds` straight after it and that function waits for `UnreadyGuildIDs()` to drain. On
timeout it skips the reconcile rather than running it against a partial cache: not reconciling
costs a stale row until the next boot, reconciling early deletes live ones.

### `disgo.New` does network I/O once a shard manager is configured

`bot/config.go:317` calls `GetGatewayBot()` during construction to learn the shard count, so
`bot.New` is no longer pure setup — a bad token now fails there rather than at open.

### Everything else compiled as written

`ChannelsForGuild`, `VoiceStates`, `VoiceState`, `Member`, `SelfMember`,
`MemberPermissionsInChannel`, `Guilds`, `Guild`, `Channel`; the cache policy signatures;
`gateway.WithCompression(gateway.CompressionNone)` and `WithLargeThreshold`; `events.GuildsReady`,
`events.GuildJoin`, `CacheGuild.JoinedAt`, `Guild.MemberCount`; `SlashCommandInteractionData`'s
`String`/`OptString`/`Int`/`OptBool`/`SubCommandName`; `AutocompleteResult`; `Open`/`SetSpeaking`/
`SetOpusFrameProvider`/`Close` on `voice.Conn`; `rest.UpdateInteractionResponse`.

The member cache policy's forward reference to `b.Client` (assigned after `disgo.New` returns) is
fine at runtime and neither `go vet` nor `staticcheck` objects, so it was left alone.

---

## sqlc v1.31.1

### Multi-argument `unnest` is not in sqlc's Postgres catalog

`select * from unnest($1::timestamptz[], $2::bigint[], ...)` fails with
`function unnest(unknown, unknown, ...) does not exist`. sqlc only knows the single-argument form.

Rewritten as one set-returning call per column in the select list, which Postgres steps in lockstep —
same zip, same input ordering, so `RETURNING id` still lines up with the input arrays.

### sqlc cannot express an array with nullable elements

`plays.user_id`, `plays.reason` and `plays.character` are nullable, but sqlc maps `bigint[]`/`text[]` to
`[]int64`/`[]string` with no way to say "elements may be NULL". Neither a column override nor
`sqlc.narg` changes that.

The columns therefore travel as sentinels and become NULL **in the query**, at the insert, not in
Go: `nullif(r.user_id, 0)`, `nullif(r.reason, '')` and `nullif(r.character, '')`. Neither a
snowflake, a failure reason nor a profile id is ever zero or empty. (`nullif(unnest(...), 0)` is not an option — Postgres rejects a set-returning function
nested inside another expression — hence the inner sub-select.)

### sqlc infers scalar subqueries as NOT NULL

The original `GuildStats`/`GlobalStats` used `(select avg(...) ...)` scalar subqueries. sqlc typed
every one of them as non-null (`float64`, `string`, `int32`), so `emit_pointers_for_null_types` never
fired and an empty guild would have scanned a real NULL into a `float64` and errored — exactly what
the "GuildStats on an empty guild returns zero/nil not an error" test checks.

Casting does not help, and a `column:`/`nullable: true` override does not apply to computed result
columns (verified experimentally). The database-backed analyzer would work but needs a live Postgres
at generate time, which would break offline `make gen`.

Fix: each optional value gets its own CTE that produces **zero rows** when there is nothing to
report, joined with `left join ... on true`. sqlc marks left-joined columns nullable, so the
generated types are `*float64` / `*string` / `*int32` — exactly what the `Store` interface promises,
and still one round-trip.

### sqlc's SQLite grammar is a subset of SQLite's

Four separate rejections, all in `internal/store/sqlite/queries/queries.sql`:

1. **No `FILTER` clause.** `count(*) filter (where ...)` becomes `count(case when ... then 1 end)`.
2. **`key` is a reserved word.** `as key` becomes `as "key"` (the Go helper keys on the field name `Key`).
3. **No mixing `?` and `?N`.** The board queries used both; now fully numbered.
4. **`HAVING` requires `GROUP BY`.** The zero-row guard used above does not parse here — but SQLite's
   `avg()` is *already* inferred nullable by sqlc, so the guard is unnecessary and was dropped. The
   `limit 1` CTEs still need the left-join treatment for `top_sound` / `loudest_hour`.

Also: sqlc resolves all scalar subqueries in one scope, so bare column names collide across them
(`column reference "guild_id" is ambiguous`). Every CTE aliases its table.

**Both engines now generate exactly the pointer types `internal/store.Store` declares**, with no
hand-written SQL in Go outside the lock/ping/tx helpers.

---

## watermill

### One subscriber shared by every handler

The router's `SubscriberConstructor` returns the same subscriber for every handler. On gochannel
that is correct and required — it fans out to all subscribers, so `GuildJoined`'s two handlers
(`stats-guilds-join` and `guild-sync-join`) each see every event.

`Bus.Close` also closes the publisher now; the router already owns and closes the subscribers.

---

## Testability seam

`candidates` called `b.bestChannel` and `voice.Busy` directly, which made a loop test without
Discord impossible. It now takes a two-field `loopDeps{best, busy}`; `RunLoop`
passes `b.deps()`. No interface, no struct field, no mock framework.

---

## Container

- **tzdata.** Quiet hours call `time.LoadLocation` with IANA zone names, and `golang:1.26-alpine`
  has no `/usr/share/zoneinfo` to copy into the scratch image. `cmd/coucou` imports `_ "time/tzdata"`,
  which embeds the database in the binary (~450 KB) and removes the file dependency entirely —
  smaller than `apk add tzdata` (~3 MB of files) and one less thing to go missing.
- **No sqlc in the image build.** `internal/store/*/gen` is committed and CI fails if regenerating it
  produces a diff, so running sqlc again per image build only added a toolchain download.
- **`docker/Dockerfile`, not a root `Containerfile`.** coucou depends on nothing private, so there
  is no credential helper, `GOPRIVATE` build arg or `github_token` secret, and the final stage is
  `scratch` rather than distroless because the binary is static.
- **Size: 31.5 MiB, against a CI gate of 34.** It was 19.39 MiB (binary 20.15 MB) when this was first
  written, with `modernc.org/sqlite` about 3.8 MB of it. Telemetry moved it to 31; the gate was
  raised to match rather than the feature cut.

  Measured per commit, binary only, `CGO_ENABLED=0 go build -trimpath -ldflags="-s -w"`:

  | commit | size |
  |---|---|
  | `ebc0990` perf(bus): CBOR event payloads | 19.59 MiB |
  | `b268a74` feat(telemetry): prometheus metrics and OpenTelemetry tracing | 29.95 MiB |
  | `483c688` refactor(validate): use go-playground/validator | 30.85 MiB |

  The obvious suspect was the OTLP gRPC exporter; it is not. Building with the exporter removed
  outright saves only 2.84 MiB, so roughly 7.5 MiB of that jump is Prometheus plus the OTel SDK —
  `client_golang` brings `client_model` and `expfmt`, which is its own protobuf mass.

  Swapping `otlptracegrpc` for `otlptracehttp` was tried and abandoned: it saves 0.08 MiB, because
  `otlptracehttp/internal/otlpconfig` imports gRPC itself and the generated
  `go.opentelemetry.io/proto/otlp/.../trace/v1` service stub links gRPC and grpc-gateway whatever
  the transport. 66 gRPC packages stay in the binary either way.

  Nothing on the tracing side reaches 20 MiB. Removing all telemetry would land at ~20.5, and it
  would cost `/metrics`. The other real knob is build tags, and it is now taken — but by narrowing,
  never by cutting: both store backends ship by default, and a deploy that knows what it runs
  drops the other. Both store backends stay covered by the conformance suite regardless.

  | build | binary |
  |---|---|
  | default — both backends | 33.0 MB |
  | `-tags pg` | 29.0 MB |
  | `-tags sqlite` | 28.7 MB |

---

## Make targets

Per-service targets live in `makefile.local`, which `makefile` includes before its own defaults, so
anything set there wins and a sync of the shared `makefile` has nothing of ours to overwrite.
`goimports` local-prefixes is `github.com/be-sandaa`.

---

## Dependencies

Both store backends are compiled in by
default and narrowed by build tag: `-tags pg` is 29.0 MB against the default 33.0 MB.
sqlc is a `tool` directive in `go.mod`, so `make gen` needs no Docker daemon and no globally
installed binary. `github.com/disgoorg/godave` is imported directly by `internal/voice/dave.go` —
it is disgo's own DAVE interface package and was already in the module graph, not a new vendor.
