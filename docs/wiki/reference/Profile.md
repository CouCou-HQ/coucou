# Profile

Who the bot is lives in one directory per character under the `profiles` key in
[Configuration](Configuration): `profiles/<id>/profile.toml` and the `sounds/` beside it. One
character is the bot as it has always been; with several, `default_profile` names the one a server
gets until it picks, and without it a server stays quiet until someone picks with `/character`. The deprecated `profile` key still points at a single character's directory. Copy [`profile.example.toml`](https://github.com/be-sandaa/coucou/blob/main/profile.example.toml), which lists every key. Nothing in it is
pushed to Discord — the bot's username, avatar and banner stay whatever the developer portal says.

| key | default | what |
|---|---|---|
| `id` | — (required) | lowercase letters, digits and dashes |
| `nickname` | `""` | what replies call the bot. Wins over its nickname in a server, which wins over its Discord name |
| `avatar.*` | none | not a key: an image file beside `profile.toml` (PNG, JPEG, GIF or WebP). The bot uses it as its avatar in every server playing this character |
| `preview` | `[]` | up to 3 sound names `/character preview` plays first; random sounds fill the rest |
| `keywords` | `[]` | words that, found in a server's name, description or channel names when the bot joins, suggest this character |
| `application_id` | `""` | the character's own bot, if it runs as one too. Adds it to the friends `/help` advertises |
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

An invalid `profile.toml` stops the bot at startup. While it runs, changes to `profile.toml` and
`avatar.*` are picked up within about 40 seconds of the last edit: added, removed and edited
characters, `/character` appearing or going, and every server's nickname and avatar. An edit that
does not load is logged and the running characters stay. The embed `color` and the `status` of the
default character still need a restart. `sounds/` reloads on its own. Every `id` must be unique. From a checkout, `profiles/` is git-ignored:
start a character with `mkdir -p profiles/lenore/sounds && cp profile.example.toml
profiles/lenore/profile.toml`, and set `profiles = "profiles"` in your `config.toml`.
