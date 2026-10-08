# Privacy Policy

This covers the bots run by the maintainer of this repository. Anyone else running coucou is
responsible for their own bot and its data; the list below is what the software stores, so it is a
fair starting point for theirs.

## What is stored

Only Discord IDs, never names, messages or audio. The bot cannot read messages, and it does not
listen in voice: it only plays.

| What | Why |
|---|---|
| Each play: server, voice channel, sound, character, time, how it went, who asked for it with `/play`, who was in the channel and who left during it | `/stats` and `/leaderboard` |
| Which command you ran, in which server, and when | spotting abuse and broken commands |
| Your `/optout`, and who set a server's `/quiet`, `/chaos` or settings | not counting you when you asked, and a server's change history |
| Servers the bot is in, when it joined, and their member count on joining | running the bot |

Logs on the host carry the same IDs.

## How long

For as long as the bot runs, unless you erase it sooner.

## Erasing yours

Run **`/forget`** in any server the bot is in. Everything that names you goes, in every server:
the times you were caught or fled, your collection and your leaderboard places. Plays you started
and changes you made stay in each server's history with no one named, so its totals still add up.
A live `/optout` is kept, since erasing it would bring the bot back to you; `/optout off` ends it.

## Sharing

Nothing is sold or shared. Discord sees what any bot does on its platform, under
[Discord's Privacy Policy](https://discord.com/privacy).

## Contact

Questions, or anything `/forget` does not cover: open an
[issue](https://github.com/be-sandaa/coucou/issues). Do not post anything you would not want public
there.
