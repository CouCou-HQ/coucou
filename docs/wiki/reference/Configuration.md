# Configuration

Everything is set in one TOML file: `-config <path>`, else `COUCOU_CONFIG`, else
`/etc/coucou/config.toml`. The working directory is never searched, so a service can't pick up a
stray file. `make dev` points at the checkout's `config.toml`. Copy
[`config.example.toml`](https://github.com/be-sandaa/coucou/blob/main/config.example.toml), which lists every key. Secrets stay out of the
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
| `siblings` | `""` | `Name=application-id` pairs of other coucou bots, comma separated, plus every character with an `application_id`; `/help` shows each one but itself in a "Friends of" grid with its invite, and about 1 in 100 `/play` replies plugs one at random. Put a Unicode emoji in the name (`🖤 Lenore=123…`) to show it with one |
| `profiles` | `""` | the characters: a directory with one `<id>/` per character, each holding `profile.toml` and `sounds/`; see [Profile](Profile) |
| `default_profile` | `""` | the `id` a server gets until it picks one. Unset with more than one character, a server stays quiet until someone picks with `/character` |
| `note` | `""` | your own markdown, under `/about` and at the end of `/help`, up to 1024 characters |
| `color` | `#E4572E` | embed accent, `#RRGGBB`, in servers whose character sets no `color` of its own, and in servers with none picked yet |
| `status.text` | `""` | shown under the bot's name, up to 128 characters. Emoji go in the text: bots get no separate emoji slot or server emoji |
| `status.activity` | `custom` | `custom` shows the text as is; `playing`, `listening`, `watching`, `competing` prefix it the way Discord does |
| `status.online` | `online` | `online`, `idle` or `dnd` |
| `profile` | `/var/lib/coucou/profile` | **deprecated**: one character's directory. Used when `profiles` is unset; setting both is an error |
| `sounds.poll` | `0s` | rescan interval for the profile's `sounds/`; `0s` uses inotify |
| `ops.http_addr` | `:9090` | `/healthz`, `/readyz` and `/metrics`; `""` disables them |
| `ops.log_level` | `info` | `debug` \| `info` \| `warn` \| `error`; audit records are stored regardless |
| `ops.pprof` | `false` | serve `/debug/pprof` on the ops listener |
| `ops.otlp` | `""` | `host:port` of an OTLP/gRPC collector; `""` disables tracing |
| `gateway.shard_count` | `0` | shards to run; `0` lets Discord decide, which is what a deploy should do |

The status belongs to the bot, not a character: Discord shows one per bot account, whoever it is
in a server. Without a `[status]` table the bot falls back to its default character's, from before
it moved here.

One setting stays an environment variable: `DISCORD_DEV_GUILD`, read only by a `-tags dev` build,
is the guild that build registers its commands to.
