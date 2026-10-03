# Running the bot

Three things make a coucou: a **Discord application** for it to log in as, a **folder of sounds**
for it to play, and a **database** for its settings and stats. This page sets up all three and
starts it. Get the binary or image first: see [Installation](Installation).

## 1. Create the Discord application

1. Open the [Discord Developer Portal](https://discord.com/developers/applications) and choose
   **New Application**. The name is the name people see in their member list.
2. Under **Bot**, choose **Reset Token** and copy the token. It's shown once, and it's a password:
   anyone holding it *is* your bot.
3. No privileged gateway intents are needed. Leave all three off.
4. Invite it with this link, putting in your application ID from **General Information**:

   ```
   https://discord.com/oauth2/authorize?client_id=<APPLICATION_ID>&scope=bot+applications.commands&permissions=3146752
   ```

   `3146752` is View Channels, Connect and Speak, which is everything the bot does.

## 2. Make the character and its sounds

Your bot's character lives in one folder, its **profile**: a `profile.toml` and a `sounds/` folder
beside it. Copy `profile.example.toml` from the repository as `profile.toml` and fill it in: an id,
optionally a nickname, the tagline, lore and traits `/about` shows, and the settings a server starts
with. Nothing in it changes the bot's name or picture in Discord; those stay what you set in step 1.

The bot plays pre-encoded **Ogg Opus** files and does no encoding itself. `scripts/sound` in the
repository turns any audio file into one, named the way the bot wants (it needs bash and ffmpeg):

```sh
scripts/sound "Wet Fart 3.mp3" ~/Downloads/farts/   # -> profile/sounds/wet_fart_3.ogg, ...
scripts/sound --rare "Perfect Fart.wav"             # -> profile/sounds/perfect_fart.rare.ogg
scripts/sound --nsfw "Ufufu.wav"                    # -> profile/sounds/ufufu.nsfw.ogg
```

- **Names** are lowercase snake_case. `wet_fart_3.ogg` shows in Discord as **Wet Fart 3**. The file
  name is the sound's identity in the stats, so renaming a file starts a new history.
- **Rares** are named `<name>.rare.ogg`. They turn up a tenth as often, only by chance, and can't be
  asked for with `/play`. Catching someone with one counts towards their collection in `/stats`.
- **18+ sounds** are named `<name>.nsfw.ogg`. They only play where Discord has both the server and
  the voice channel age-restricted, and nowhere else can anyone roll, see or ask for them. Tags
  combine in any order: `<name>.rare.nsfw.ogg` is both.
- **Markers** follow a tagged name wherever Discord shows it: ✨ for rare, 🔞 for 18+, so
  `perfect_fart.rare.ogg` shows as **Perfect Fart ✨**.
- **Keep them short.** A drop-in is one clip and gone; a few seconds lands better than a song.

Without the script, this is the command it runs. `-vn` matters: an mp3's embedded cover art would
otherwise come along as a video stream, and the bot would quietly reject the file.

```sh
ffmpeg -i in.mp3 -vn -map_metadata -1 -c:a libopus -b:a 64k -ar 48000 -ac 2 out.ogg
```

New sounds are picked up within about 1.5 seconds, with no restart; a change to `profile.toml`
needs one. An empty sounds folder starts a bot that never plays anything, and a missing or invalid
`profile.toml` stops it with an error saying what is wrong.

## 3. Configure and start

Settings live in one file, `/etc/coucou/config.toml` (or wherever `-config` points). Copy
`config.example.toml` from the repository there; it lists every setting with a comment. Secrets
don't go in the file. It reads them from the environment instead:

```toml
discord_token = "${DISCORD_BOT_TOKEN}"   # the token from step 1
database_url  = "${DATABASE_URL}"        # sqlite:///path/to/coucou.db, or postgres://user:pass@host:5432/db

profile       = "/var/lib/coucou/profile"   # the folder from step 2
```

```sh
DISCORD_BOT_TOKEN=... DATABASE_URL=sqlite://$PWD/coucou.db ./coucou            # reads /etc/coucou/config.toml
DISCORD_BOT_TOKEN=... DATABASE_URL=sqlite://$PWD/coucou.db ./coucou -config ./config.toml
```

On the way up it applies its database migrations, registers its slash commands, loads the sounds,
and connects. There are no separate setup steps.

**Slash commands can take up to an hour to appear** the first time, or after an update changes them.
The registration is immediate; Discord's propagation isn't.

A few settings worth knowing about. `config.example.toml` lists them all, and so does the
[README](https://github.com/be-sandaa/coucou#configuration):

| Key | Default | What |
|---|---|---|
| `owner_ids` | `[]` | your Discord user ID, in quotes; unlocks the leaderboard of servers |
| `ops.log_level` | `info` | `debug`, `info`, `warn` or `error` |

How often the bot visits a new server is the profile's `defaults.chance`: a percentage rolled every 5
minutes while someone is in voice, so the default 5 works out to about one visit per 1.5–2 hours of
active voice. Set `0` to make servers opt in with `/chance`.
| `GOMEMLIMIT` (environment) | — | set `48MiB`; the bot is built to sit well under it |

## Is it working?

- **In Discord:** `/status` says what the bot thinks of your server, and `/play` plays a sound in
  your channel right now.
- **Over HTTP**, on `ops.http_addr` (default `:9090`): `/readyz` answers 200 once it's connected to
  Discord and the database answers, `/healthz` answers 200 while the process runs, and `/metrics`
  serves Prometheus metrics.

## Commands

| Command | Who | Does |
|---|---|---|
| `/play [sound]` | anyone | play a sound in your channel now |
| `/sounds [page]` | anyone | every sound `/play` will take from you, 50 a page |
| `/leave` | anyone | cut a play short |
| `/help` | anyone | the command list, and which switch actually keeps the bot out |
| `/optout` | anyone, for themselves | stop being picked: indefinitely, for some hours, or on a schedule |
| `/chance <0-100>` | Manage Server | how often it visits; 0 is never |
| `/quiet` | Manage Server | leave the server alone: indefinitely, for some hours, or on a schedule |
| `/suspense <0-20>` | Manage Server | seconds of awkward silence before the sound |
| `/status` | anyone | what the bot thinks about this server |
| `/stats user\|guild\|bot` | anyone | play statistics, rankings and charts |
| `/leaderboard` | anyone | who suffers the most |

## Running more than one

Each coucou is the same binary with its own Discord application, its own sounds folder and its own
database. Run one process per bot, and never two processes with the same token: a second instance is
a second bot that joins the same channels and doubles every stats row.

`siblings` in `config.toml` lets your bots advertise each other: a comma-separated list of `Name=application-id`.
`/help` shows every one but itself as a card with its avatar and invite link, and now and then a
`/play` reply plugs one. A Unicode emoji may lead a name (`🖤 Lenore=123…`) and shows with it.
Give every bot the same list; each leaves itself out.
