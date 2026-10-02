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

## 2. Make the sounds

The bot plays pre-encoded **Ogg Opus** files and does no encoding itself. `scripts/sound` in the
repository turns any audio file into one, named the way the bot wants (it needs bash and ffmpeg):

```sh
scripts/sound "Wet Fart 3.mp3" ~/Downloads/farts/   # -> sounds/wet_fart_3.ogg, ...
scripts/sound --rare "Perfect Fart.wav"             # -> sounds/perfect_fart.rare.ogg
```

- **Names** are lowercase snake_case. `wet_fart_3.ogg` shows in Discord as **Wet Fart 3**. The file
  name is the sound's identity in the stats, so renaming a file starts a new history.
- **Rares** are named `<name>.rare.ogg`. They turn up a tenth as often, only by chance, and can't be
  asked for with `/play`. Catching someone with one counts towards their collection in `/stats`.
- **Keep them short.** A drop-in is one clip and gone; a few seconds lands better than a song.

Without the script, this is the command it runs. `-vn` matters: an mp3's embedded cover art would
otherwise come along as a video stream, and the bot would quietly reject the file.

```sh
ffmpeg -i in.mp3 -vn -map_metadata -1 -c:a libopus -b:a 64k -ar 48000 -ac 2 out.ogg
```

New files are picked up within about 1.5 seconds, with no restart. An empty or missing sounds folder
starts a bot that never plays anything.

## 3. Configure and start

Every setting is both a flag and an environment variable; the flag wins when both are given.
Three are all you need to start:

| Variable | Flag | What |
|---|---|---|
| `DISCORD_BOT_TOKEN` | `-discord-token` | the token from step 1 |
| `DATABASE_URL` | `-database-url` | `sqlite:///path/to/coucou.db` or `file:coucou.db` for a SQLite file; `postgres://user:pass@host:5432/db` for PostgreSQL |
| `SOUNDS_DIR` | `-sounds-dir` | the folder from step 2; default `sounds` |

```sh
DISCORD_BOT_TOKEN=... DATABASE_URL=sqlite://$PWD/coucou.db SOUNDS_DIR=./sounds ./coucou
```

On the way up it applies its database migrations, registers its slash commands, loads the sounds,
and connects. There are no separate setup steps.

**Slash commands can take up to an hour to appear** the first time, or after an update changes them.
The registration is immediate; Discord's propagation isn't.

A few settings worth knowing about. `./coucou -h` lists them all, and so does the
[README](https://github.com/be-sandaa/coucou#configuration):

| Variable | Default | What |
|---|---|---|
| `DEFAULT_CHANCE` | `5` | the chance of a visit, in percent, rolled every 5 minutes while someone is in voice. 5 works out to about one visit per 1.5–2 hours of active voice. Set `0` to make servers opt in with `/chance`. |
| `OWNER_IDS` | — | your Discord user ID; unlocks the leaderboard of servers |
| `LOG_LEVEL` | `info` | `debug`, `info`, `warn` or `error` |
| `GOMEMLIMIT` | — | set `48MiB`; the bot is built to sit well under it |

## Is it working?

- **In Discord:** `/status` says what the bot thinks of your server, and `/play` plays a sound in
  your channel right now.
- **Over HTTP**, on `HTTP_ADDR` (default `:9090`): `/readyz` answers 200 once it's connected to
  Discord and the database answers, `/healthz` answers 200 while the process runs, and `/metrics`
  serves Prometheus metrics.

## Commands

| Command | Who | Does |
|---|---|---|
| `/play [sound]` | anyone | play a sound in your channel now |
| `/leave` | anyone | cut a play short |
| `/help` | anyone | the command list, and which switch actually keeps the bot out |
| `/optout` | anyone, for themselves | stop being picked: indefinitely, for some hours, or on a schedule |
| `/chance <0-100>` | Manage Server | how often it visits; 0 is never |
| `/quiet set\|off` | Manage Server | hours to be left alone, in the server's time zone |
| `/suspense <0-20>` | Manage Server | seconds of awkward silence before the sound |
| `/status` | anyone | what the bot thinks about this server |
| `/stats user\|guild\|bot` | anyone | play statistics, rankings and charts |
| `/leaderboard` | anyone | who suffers the most |

## Running more than one

Each coucou is the same binary with its own Discord application, its own sounds folder and its own
database. Run one process per bot, and never two processes with the same token: a second instance is
a second bot that joins the same channels and doubles every stats row.

`SIBLINGS` lets your bots advertise each other: a comma-separated list of `Name=application-id`.
`/help` links every one but itself, and now and then a `/play` reply plugs one. Give every bot the
same list; each leaves itself out.
