# Characters

Design for running several characters from one bot. Status: proposal, nothing built yet.

One bot loads every character in `profiles/`. With one character it behaves as a bot does today;
with two or more, each server picks one and the bot changes its server nickname and avatar to match.
The same binary runs coucou with every character and each character as its own bot.

## What Discord allows

Checked against the API docs and disgo v0.19.6, not memory.

| Per server (`PATCH /guilds/{id}/members/@me`, `rest.UpdateCurrentMember`) | Account-wide, so never per character |
|---|---|
| `nick` (needs Change Nickname), `avatar`, `banner`, `bio` | username, the avatar shown in DMs, status, slash commands |

One bot is in one voice channel per server, so characters on one bot never play together. That is
what the separate bots are for.

Limits, measured on two test servers (2026-10-08), not documented by Discord:

| limit | scope | how it shows |
|---|---|---|
| 20 requests per 300 s on the route | per server | normal `X-RateLimit-*` headers and `429`, which disgo handles |
| 2 avatar changes per about 10 minutes | per server, not per account | `400`, code `50035`, `errors.avatar` = `AVATAR_RATE_LIMIT`, no retry time |

- A refused request applies nothing, so nickname and avatar go in separate requests.
- Clearing the avatar (`avatar: null`) was accepted while the limit was on.
- 10 minutes is an upper bound: refused attempts every 60 s may have extended it.

## Configuration

`profiles` replaces `profile`:

```
profiles/
  lisa/profile.toml, avatar.png, sounds/
  bart/profile.toml, avatar.png, sounds/
```

- `profile = "dir"` keeps working for two releases as a one-character `profiles/`, with a
  deprecation warning at startup when the key is set. Leaving both unset still reads the old
  default directory, without a warning, so existing Docker and Helm installs keep working.
- `default_profile = "<id>"` in `config.toml` is the character for new servers and for servers whose
  character was removed. It is required when there is more than one character. It is not "the
  first one alphabetically", because then adding a character could silently change the default.
- No characters at startup is a usage error, as a broken profile is today.
- `siblings` stays. A standalone bot only loads its own character, so it has no other way to know
  about the others or about coucou.
- New `note` in `config.toml`: operator markdown, shown as the last part of `/help` and under the
  character sheet in `/about`, capped like `aboutLimit`. It belongs to whoever runs the deployment,
  not to a character.

New `profile.toml` keys:

| key | what |
|---|---|
| `application_id` | the character's own bot, if it has one. Makes it promotable and invitable on its own |
| `keywords` | matched against a new server to suggest this character |
| `preview` | up to 3 sound names `/character preview` plays; random sounds when left out |

The embed accent (`color`) on replies is the default character's. Status is account-wide, so it
is the default character's too. Everything else a reply shows comes from the server's character.

`avatar.png` beside `profile.toml` is the per-server avatar. Without it only the nickname is pushed.

## Per server

- `guild_settings.character` holds the profile `id`, even on a one-character bot, so a bot that
  gains characters later already knows what every server is.
- `guild_settings.pushed` holds a hash of the nickname and avatar last pushed to that server. Any
  push compares against it, so an unchanged character is never pushed again.
- Switching has a per-server cooldown of 1 hour, well clear of 2 avatar changes per 10 minutes.
- `AVATAR_RATE_LIMIT` is not an error to log and drop: disgo does not retry a `400`, so the push
  schedules its own retry for that server 10 minutes later.
- Both limits are per server, so filling in existing servers is paced only by Discord's global
  request limit: a steady one server per second in the background.
- When filling in servers the bot is already in, a server whose admins set a nickname
  (`SelfMember` has one) gets the avatar only. Nothing chosen by a server is overwritten.

## Commands

`/character` is deployed only when two or more characters are loaded. `Deploy` replaces the whole
command list with `SetGlobalCommands`, so leaving it out also removes it. `/help` lists only what is
deployed.

| subcommand | does |
|---|---|
| (none) | the current character and the others |
| `preview <id>` | plays its preview sounds in the caller's voice channel and shows its avatar, tagline and lore. The bot does not change for a preview, and the play is not recorded |
| `switch <id>` | Manage Server, the 1-hour per-server cooldown, then one push |

When the character has `application_id`, preview and switch replies add a line linking to its own
bot, for servers that want it alongside the others rather than instead of them.

The friends list (`/help` grid, `playAd`) is `siblings` merged with every loaded character that has
`application_id`. Duplicates are removed by app ID, and the bot's own app is left out. A single
character bot whose `application_id` is not its own app logs a warning at startup, which catches a
profile copied into the wrong deployment.

## Joining a server

On `GuildJoin` (not the burst of servers at startup), and only when the server has no settings
row yet:

1. Suggest settings from what the join already carries: name, description, locale, member count,
   channel names and types. The character comes from the best `keywords` match, with the default on
   a tie or no match. `chance` comes from members and voice channels. `nsfw` always stays off. It
   is a pure function with a table test; there is no model behind it.
2. Post an introduction in the system channel, or the first text channel in channel order where
   the bot has View Channel, Send Messages and Embed Links. If there is none, post nothing.
3. Add an Apply button that carries the suggestion in its `custom_id`, so it still works after a
   restart. The message is public, so the handler checks the presser has Manage Server. Not
   pressing it leaves the profile defaults, and the commands still work.

The character is still saved and pushed on join. Only the settings wait for the button.

## Reloading characters

`profiles/` is watched the way `sounds/` already is, with a 30-second debounce.

- An invalid `profile.toml` keeps the characters that are loaded and logs the error. Only startup
  refuses to run.
- Going from one character to two or more, or back, calls `Deploy` again.
- A removed character's servers fall back to the default.
- A changed nickname or avatar is pushed only to servers whose `pushed` hash differs, through the
  same one-server-per-second pace.

## Stats

- `stats_plays` gets a nullable `character` column, in the SQLite and Postgres migrations.
- `NULL` (rows recorded before the column existed) counts as the default character.
- `/stats` shows the total as today, plus a "By character" breakdown when more than one character
  appears in the rows. A one-character bot's `/stats` looks unchanged.
- Leaderboards (`ranks`) stay totals across characters.

## Order

Each step ships on its own:

1. `profiles`, `default`, the `character` columns in settings and stats, `application_id` and the
   merged friends list, `note`
2. Pushing nickname and avatar: the `AVATAR_RATE_LIMIT` retry, the `pushed` hash, filling in existing servers
3. `/character`: preview, switch, the promotion line
4. The introduction on join, with suggestions and the Apply button
5. Reloading characters
