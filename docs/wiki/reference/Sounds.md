# Sounds

Pre-encoding a sound (the bot does no encoding at runtime — no ffmpeg, no Opus encoder) is one
command. `scripts/sound` takes files or folders, encodes everything it finds, and names the results
in snake_case for you:

```sh
scripts/sound "Wet Fart 3.mp3" ~/Downloads/farts/   # -> profile/sounds/wet_fart_3.ogg, ...
scripts/sound --rare "Perfect Fart.wav"             # -> profile/sounds/perfect_fart.rare.ogg
scripts/sound --nsfw --rare "Ufufu.wav"             # -> profile/sounds/ufufu.rare.nsfw.ogg
scripts/sound -o /srv/lenore/sounds -n ~/Downloads/farts/  # dry run into another profile
```

It writes to the checkout's `profile/sounds` (or `-o`), skips anything that already exists unless `-f`, and needs
only bash and ffmpeg. Underneath it is this, with `-vn` because an mp3's embedded cover art would
otherwise ride along as a video stream and the bot would reject the file:

```sh
ffmpeg -i in.mp3 -vn -map_metadata -1 -c:a libopus -b:a 64k -ar 48000 -ac 2 out.ogg
```

Drop the `.ogg` into the profile's `sounds/` and it is playable within ~1.5 s. No restart. Half-copied or
non-Opus files are ignored until they are valid.

Name files in lowercase snake_case: `wet_fart_2.ogg` is the sound `wet_fart_2`, and Discord
shows it as **Wet Fart 2** — underscores become spaces and each word gets a capital. The file name
is the sound's identity in the stats, so renaming a file starts a new history; migration 00018
moved the old `Wet Fart 2` / `big-burp` spellings over once.

Tags are in the filename, between the name and `.ogg`, in any order: `<name>.rare.ogg`,
`<name>.nsfw.ogg`, and `<name>.rare.nsfw.ogg` (the same as `<name>.nsfw.rare.ogg`) all load as the
sound `<name>`, so tagging or untagging one is a rename and its history stays under one name.
`scripts/sound` keeps the tags a source already has (`-r`/`--rare`, `-x`/`--nsfw` add them) and
always writes them as `.rare.nsfw`. Wherever a sound is shown, its tags follow the name as ✨ for
rare and 🔞 for nsfw: `perfect_fart.rare.ogg` is **Perfect Fart ✨**.

- **rare** turns up a tenth as often, only by roll: it is left out of `/play`'s autocomplete and
  cannot be asked for by name.
- **nsfw** plays where a server's `/nsfw` mode lets it. **restricted**, the default, is wherever
  Discord has age-restricted the server or the voice channel, which is where its rules put adult
  content. **off** is nowhere. **on** is every voice channel, age-restricted or not: it asks for a
  confirmation first, and who turned it on and when is kept in the settings audit log. Wherever it
  may not play, it is never rolled, never in the autocomplete, and cannot be asked for by name;
  `/help` says where it can. A sound tagged both follows both rules. A bot listed in Discord's App
  Directory may carry no nsfw sounds at all.

Any other segment is part of the name (`foo.v2.ogg` is `foo.v2`), and one that looks like a typo of
a tag (`foo.nswf.ogg`) loads that way with a warning. Tags do not make a name unique: if several
files back one name, the most-tagged one wins (`nsfw` outranks `rare`), the others are ignored with
a warning, and deleting the winner falls back to the next. `/stats scope:user` shows your
collection: the distinct sounds the bot's own visits have caught you with, out of what is loaded
now and can play in this server. `/play` does not count toward it.

## Sound emojis

An app emoji named exactly like a sound is that sound's emoji: upload `perfect_fart` under the
application's **Emojis** in the developer portal and the sound `perfect_fart` shows it, tags and all
(the emoji is named after the sound, never after `.rare` or `.nsfw`). It is shown in the `/play` reply
in place of the speaker, before the top sound in `/stats`, and on the `/leaderboard` sounds board.
`/sounds` and the autocomplete stay text: fifty mentions would overrun a page, and autocomplete
cannot render one.

A sound with no emoji of its name is shown exactly as before, so they can be added one at a time.
Discord tells the bot nothing when an app emoji is added or deleted, so it lists them every ten
minutes. A new emoji appears within that, and a deleted one may still be mentioned until then.
