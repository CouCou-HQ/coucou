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
| `siblings` | `""` | `Name=application-id` pairs of other coucou bots, comma separated; `/help` shows each one but itself as a card with its avatar and invite, and about 1 in 100 `/play` replies plugs one at random. Put a Unicode emoji in the name (`🖤 Lenore=123…`) to show it with one |
| `profile` | `/var/lib/coucou/profile` | the character: a directory holding `profile.toml` and `sounds/`; see [Profile](Profile) |
| `sounds.poll` | `0s` | rescan interval for the profile's `sounds/`; `0s` uses inotify |
| `ops.http_addr` | `:9090` | `/healthz`, `/readyz` and `/metrics`; `""` disables them |
| `ops.log_level` | `info` | `debug` \| `info` \| `warn` \| `error`; audit records are stored regardless |
| `ops.pprof` | `false` | serve `/debug/pprof` on the ops listener |
| `ops.otlp` | `""` | `host:port` of an OTLP/gRPC collector; `""` disables tracing |
| `gateway.shard_count` | `0` | shards to run; `0` lets Discord decide, which is what a deploy should do |

One setting stays an environment variable: `DISCORD_DEV_GUILD`, read only by a `-tags dev` build,
is the guild that build registers its commands to.
