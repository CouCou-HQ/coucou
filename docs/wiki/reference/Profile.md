# Profile

Who the bot is lives in one directory, the `profile` key in [Configuration](Configuration): `profile.toml` and the `sounds/` it
plays. Copy [`profile.example.toml`](https://github.com/be-sandaa/coucou/blob/main/profile.example.toml), which lists every key. Nothing in it is
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
