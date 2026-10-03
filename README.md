<p align="center">
  <img src="docs/assets/banner.svg" alt="coucou! A speech bubble with two eyes peeking over its edge" width="640">
</p>

<p align="center"><b>It waits in the wings. Then, from nowhere: <i>coucou!</i></b></p>

<p align="center">
  <a href="go.mod"><img src="https://img.shields.io/badge/Go-1.26-00ADD8?logo=go&logoColor=white" alt="Go 1.26"></a>
  <a href="https://github.com/be-sandaa/coucou/releases/latest"><img src="https://img.shields.io/badge/release-v0.1.0-E4572E" alt="release v0.1.0"></a>
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
   🦢  Honk joined
   ⋯   (a long, suspicious silence)
   💨  ▶ Wet Fart 3
   🦢  Honk left

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
| 🔞 **18+ sounds** | Only in voice channels Discord has labelled age-restricted. |
| 📊 **Stats** | Charts, heatmaps and leaderboards of who suffers most. |
| 🙈 **Escape hatches** | `/optout` for a person, `/quiet` for a server, both on a schedule if you like. |

## 🎭 Bring your own bot

coucou isn't a character. Give it one and you have a new bot: one binary, your character, your
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

> [!NOTE]
> The repository, the image and the release binaries are private for now, so this is the plan for
> when they open up rather than something you can do today.

### 🏆 Community bots

| Bot | What it does | Maintainer | |
|---|---|---|---|
| *yours here* | | | |

## 🚀 Quick start

```sh
docker run --rm \
  -e DISCORD_BOT_TOKEN=... \
  -e DATABASE_URL=sqlite:///var/lib/coucou/coucou.db \
  -v coucou-data:/var/lib/coucou \
  -v "$PWD/profile:/var/lib/coucou/profile:ro" \
  ghcr.io/be-sandaa/coucou:0.1.0
```

That needs a Discord application and a profile folder first; [Running the bot](docs/wiki/guide/Running-the-bot.md)
walks through both in three steps.

## 📚 Documentation

| | Page | For |
|---|---|---|
| 📦 | [Installation](docs/wiki/guide/Installation.md) | binary, systemd, container, Compose, Helm |
| ▶️ | [Running the bot](docs/wiki/guide/Running-the-bot.md) | the Discord app, sounds, first start, "is it working?" |
| ⚙️ | [Configuration](docs/wiki/reference/Configuration.md) | every key in `config.toml` |
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

[MIT](LICENSE).
