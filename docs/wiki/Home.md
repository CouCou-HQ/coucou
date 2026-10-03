# coucou

*Coucou!* is French for "peekaboo", or a cheeky "hi there". That is the entire product.

coucou is the application behind Discord bots that pop into a busy voice channel, play one short
sound and leave. It is not a character itself: the bot people meet has its own name, Discord app and
sounds, and coucou is what runs it.

## Run your own

- **[Installation](Installation)**: the binary, the service, the container image, Compose or Helm.
- **[Running the bot](Running-the-bot)**: create the Discord app, add sounds, start it, and check it
  works.

## Reference

- **[Configuration](Configuration)**: every key in `config.toml`.
- **[Profile](Profile)**: the character in `profile.toml`, its status, defaults, chains and links.
- **[Sounds](Sounds)**: encoding, naming, rare and 18+ tags, and sound emojis.
- **[Commands](Commands)**: every slash command and who may run it.

## Hacking on coucou

- **[Development](Development)**: building, testing, and the image.
- **[Architecture](Architecture)**: how a play happens, the layout, the database, the bus and memory.
