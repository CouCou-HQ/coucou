<p align="center">
  <img src="docs/assets/coucou/banner.webp" alt="CouCou, with long pastel rainbow hair, winking and holding up a gilded hand mirror in a Paris dressing room full of gowns at sunset" width="720">
</p>

<p align="center"><b>It waits in the wings. Then, from nowhere: <i>coucou!</i></b></p>

<p align="center">
  <a href="go.mod"><img src="https://img.shields.io/badge/Go-1.26-00ADD8?logo=go&logoColor=white" alt="Go 1.26"></a>
  <a href="https://github.com/be-sandaa/coucou/releases/latest"><img src="https://img.shields.io/badge/release-v0.3.0-E4572E" alt="release v0.3.0"></a>
  <a href="https://github.com/be-sandaa/coucou/pkgs/container/coucou"><img src="https://img.shields.io/badge/image-ghcr.io%2Fbe-sandaa%2Fcoucou-2496ED?logo=docker&logoColor=white" alt="image ghcr.io/be-sandaa/coucou"></a>
  <a href="docs/wiki/dev/Architecture.md#memory"><img src="https://img.shields.io/badge/RSS%20target-25%20MB%20%40%201.6k%20guilds-4E5058" alt="RSS target 25 MB at 1.6k guilds"></a>
  <a href="LICENSE"><img src="https://img.shields.io/badge/license-MIT-F2B705" alt="MIT license"></a>
</p>

*Coucou!* is French for "peekaboo", or a cheeky "hi there". That is the entire product.

coucou runs Discord bots that slip into a busy voice channel, play one short sound, and leave
before anyone can react. Then everybody blames each other.

## 🎬 A visit, as seen from the channel

```text
🔊 General ·  👤 alex   👤 sam   👤 robin

   🎲  the 5-minute roll comes up
   🪞  CouCou looks into her mirror… et hop !
   🍺  Lorelei joined
   ⋯   (a long, suspicious silence)
   🍺  ▶ Prost!
   🍺  Lorelei left

   💬  sam: alex
   💬  alex: that was NOT me
```

## ✨ What it does

| | |
|---|---|
| 🎲 **Drop-ins** | Every 5 minutes it rolls for each server, and on a win picks the busiest voice channel with a human in it. |
| 🤫 **Suspense** | Sits in silence for up to 20 seconds first, if a server wants it to. |
| 🃏 **Fake-outs** | Sometimes joins, says nothing, and leaves. |
| 🔁 **Encores** | Comes back for a second sound, and a sound can name what follows it. |
| ⛓️ **Chains** | *Knock knock… who's there… rimshot*, all in one visit. |
| ✨ **Rares** | A tenth as often, never on request, and collected in `/stats`. |
| 🔞 **18+ sounds** | Where Discord has age-restricted the server or channel, or wherever a server's admins choose with `/nsfw`. |
| 📊 **Stats** | Charts, heatmaps and leaderboards of who suffers most. |
| 🙈 **Escape hatches** | `/optout` for a person, `/quiet` for a server, both on a schedule if you like. |

## 🪞 Meet CouCou

CouCou is the bot run from here, and she has no voice of her own. Until she takes on a shape she can
only smile, wink and wave; so she looks into her gilded hand mirror, it shows her someone else, and
she drops into your voice channel as them.

<p align="center">💌 🌺 💋 🎮 🤠 🖤 🍺 📚 🕶️ 👑 ☀️ 💼 💜 🍷</p>

Each server picks who she becomes with `/character`: a florist who asks who you were talking to, a
tsundere who insults your aim, a beer-garden waitress with two Maß in each hand, a privacy engineer
who warns you about your password… → [all of them](docs/wiki/characters/README.md). Want more than one
in the same server? Each also runs as a bot of its own, and a couple, like Gus 💨, only ever run as
themselves → [standalone bots](docs/wiki/standalone/README.md).

A few things she will not tell you:

- Her eyes are gold and violet. She claims they were both gold once, and won't say what happened.
- She has been asked "wait, who are you?" more times than anyone alive. She has never answered.
- Her mirror has a crack in one corner. She says it's from the last time someone tried to look in it.

## 🎭 Bring your own bot

The project, coucou, isn't a character; CouCou is just the one run from here. Give it one and you have a new bot: one binary, your character, your
Discord app, your sounds.

1. **Write the character.** A `profile.toml`: a name, an emoji, a status, lore for `/about`, and how
   a server starts out. → [Profile](docs/wiki/reference/Profile.md)
2. **Make the sounds.** `scripts/sound ~/Downloads/noises/` encodes a folder of clips and names them
   for you. → [Sounds](docs/wiki/reference/Sounds.md)
3. **Run it.** A release binary, the container image, Compose or the Helm chart, with a token from
   your own Discord application. → [Installation](docs/wiki/guide/Installation.md)
4. **Get it listed.** Open a [**List my bot**](https://github.com/be-sandaa/coucou/issues/new?template=list_my_bot.yml)
   issue and it joins the table below.

To be listed, a bot runs on its own Discord application, with sounds it has the right to use, and
follows Discord's [Terms of Service](https://discord.com/terms) and
[Developer Policy](https://support-dev.discord.com/hc/en-us/articles/8563934450327-Discord-Developer-Policy).

### 🏆 Community bots

| Bot | What it does | Maintainer | |
|---|---|---|---|
| 💨 [Gus](docs/wiki/standalone/Gus.md) | Wanders in, farts, burps, crunches chips into the mic, and leaves you all blaming each other. | [@be-sandaa](https://github.com/be-sandaa) | |
| 😳 [Moan](docs/wiki/standalone/Moan.md) | Lets out one very unfortunate moan, and vanishes before anyone can explain it. | [@be-sandaa](https://github.com/be-sandaa) | |
| *yours here* | | | |

## 🚀 Quick start

```sh
docker run --rm \
  -e DISCORD_BOT_TOKEN=... \
  -e DATABASE_URL=sqlite:///var/lib/coucou/coucou.db \
  -v coucou-data:/var/lib/coucou \
  -v "$PWD/profile:/var/lib/coucou/profile:ro" \
  ghcr.io/be-sandaa/coucou:0.3.0
```

That needs a Discord application and a profile folder first; [Running the bot](docs/wiki/guide/Running-the-bot.md)
walks through both in three steps.

## 📚 Documentation

| | Page | For |
|---|---|---|
| 📦 | [Installation](docs/wiki/guide/Installation.md) | binary, systemd, container, Compose, Helm |
| ▶️ | [Running the bot](docs/wiki/guide/Running-the-bot.md) | the Discord app, sounds, first start, "is it working?" |
| ⚙️ | [Configuration](docs/wiki/reference/Configuration.md) | every key in `config.toml` |
| 🪞 | [Characters](docs/wiki/characters/README.md) | who CouCou can become, and the standalone bots |
| 🎭 | [Profile](docs/wiki/reference/Profile.md) | the character, its status, defaults, chains and links |
| 🔊 | [Sounds](docs/wiki/reference/Sounds.md) | encoding, naming, tags, sound emojis |
| 💬 | [Commands](docs/wiki/reference/Commands.md) | every slash command and who may run it |
| 🛠️ | [Development](docs/wiki/dev/Development.md) | building, testing, the image |
| 🧭 | [Architecture](docs/wiki/dev/Architecture.md) | how a play happens, the database, the bus, memory |

## 🛠️ Hacking on it

```sh
make build   # vet + test, then a static binary in bin/coucou
make dev     # run the checkout against one test server
make help    # everything else
```

Go 1.26, no CGO, both SQLite and PostgreSQL in one binary. Start with
[Architecture](docs/wiki/dev/Architecture.md), and [CONTRIBUTING.md](CONTRIBUTING.md) before a PR.

## 📄 License

[MIT](LICENSE). The bots run from this repository follow its [Terms](TERMS.md) and
[Privacy Policy](PRIVACY.md).
