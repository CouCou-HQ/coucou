# Commands

| command | who | does |
|---|---|---|
| `/play [sound]` | anyone | play now in your channel; autocompletes over the live registry |
| `/sounds [page]` | anyone | every sound `/play` will take from you, 50 a page, past the autocomplete's 25 (only you see it) |
| `/leave` | anyone | cut a play short |
| `/help` | anyone | the command list, and which switch actually keeps the bot out |
| `/about` | anyone | who the bot is: its tagline, lore and traits from the profile (only you see it) |
| `/optout on\|for\|schedule\|rrule\|off` | anyone, per person | stop being counted when the bot picks a channel: indefinitely, `for <hours>`, or on a repeating `schedule` (an RFC 5545 rule, typed directly with `rrule`) |
| `/chance <0-100>` | Manage Server | odds of a drop-in every 5 minutes (0 = never) |
| `/quiet on\|for\|schedule\|rrule\|off` | Manage Server | leave the server alone: indefinitely, `for <hours>`, or on a repeating `schedule` — the same shapes as `/optout` |
| `/suspense <0-20>` | Manage Server | seconds of silence before the sound |
| `/nsfw [off\|restricted\|on]` | Manage Server, unless changed under Integrations | where 18+ sounds play: never, where Discord has age-restricted the server or channel (default), or every voice channel after a confirmation; without a mode, shows the current one |
| `/status` | anyone | what the bot thinks about this server |
| `/stats user\|guild\|bot` | anyone | play statistics with 30-day ranks and charts — plays a day, a week-by-hour heatmap in the server's zone, how visits began and ended: yours (only you see it; here and across every server), this server's, or bot-wide (both posted in the channel) |
| `/leaderboard board period public` | anyone (`guilds` board: owner) | who suffers the most |
