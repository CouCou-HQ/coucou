# Design System: coucou

Character: **ambush, then deadpan.** The bot jumps out at you and acts like nothing
happened. Every surface is either a punchline or a readout — never a paragraph.

Derived from the existing embed code (`internal/commands/commands.go`), not invented:
the four colours, the accessibility rule, the board-shape rule and the voice were
already there and are recorded here as-is. The ASCII vocabulary is the new part.

## Color

Four, and no more. Every title states in words what its colour states in hue — a
screen reader is read the title and never the colour, so colour is confirmation,
never the carrier.

- `colBrand` `#E4572E` — reports and confirmations. The signature hue; also the
  release badge in the README, so the bot and its repo match. A character's
  `profile.toml` may replace it with its own `color`; it is the only one of the
  four a profile can change, because the other three carry meaning.
- `colBoard` `#F2B705` — leaderboards only, matching the gold medal that heads them.
- `colBad`   `#C4413B` — refusals, bad input, anything that failed.
- `colMuted` `#4E5058` — nothing to show, which is not the same as failing.

## Typography

Discord gives two typefaces and no control over either: proportional body text, and
monospace inside a code fence. That is the whole type system, so the decision is
only ever *which of the two*, and it is load-bearing:

- **Monospace (fenced)** for anything that must align in a column — bars, meters,
  rulers, drawings. Proportional text cannot align, so a chart outside a fence is
  a broken chart.
- **Proportional** for anything that must *render* — mentions `<@id>`, channel
  links `<#id>`, timestamps `<t:…:R>`, and bold. None of these resolve inside a
  code fence; they come out as raw source.
- The two combine on one line via an inline code span: bar in `` ` ``, mention
  outside it. This is the only way a leaderboard gets both alignment and live
  mentions.

## Spacing

- **Width budget: 40 columns, hard.** Code blocks do not wrap on Discord; they
  scroll sideways. A drop-in is usually triggered from a phone that is already in
  the call, so anything past 40 is a readout the reader has to drag.
- Meter line: 1 space · 8-col label · 1 space · 14-col bar · 1 space · value. The first
  25 columns are fixed, which leaves the value 15 — `TestBlocksFitThePhone` is what
  holds that true.
- Hour strip: 24 cells, one per hour, plus a 24-col ruler above it.
- One blank line between groups inside a block; never two.

## Shape

- Bar fill `█`, bar track `░`. Two characters, both full-cell, so a partly-filled
  bar never looks like a rendering bug.
- Frames use light box-drawing `┌ ─ ┐ └ ┘`, sized to their widest row rather than to
  a fixed width — a fixed frame either strangles the content or draws a box of empty
  air beside it. No frame may carry meaning: a frame is what a narrow client mangles
  first, and nothing may be lost when it does.
- Ranks keep the existing medals 🥇🥈🥉 then `4.` onward.

## Motion

None. Discord does not animate an embed, and editing one to fake it burns rate
limit for a gimmick.

## Components

- **Meter** — label, bar, value. For a bounded setting: chance, suspense, fail rate.
- **Hour strip** — 24 cells with a ruler. For quiet hours only; it is the one
  setting where the shape of the window says more than its numbers.
- **Board row** — `🥇 ` + inline-code(bar + right-aligned count) + label outside the
  span. Boards stay a description list, never fields: a rank label is as wide as
  whoever owns it, and a field grid only survives a phone when every cell is short.
- **Panel** — a framed block of meters, for `/status`.
- **Column chart** — a month of days, one column each, four rows of eighth-blocks
  `▁▂▃▄▅▆▇█` on a single labelled axis (scale on top, zero at the baseline, first day and
  "today" underneath). A non-zero day is never drawn empty.
- **Sparkline** — the column chart in one row, `·` for an empty day. For where a whole
  chart is too much: the "everywhere" side of `/stats user`.
- **Heatmap** — weekday rows (Monday first) by 24 hour columns under the hour-strip
  ruler, in the server's own zone. `·` is never; `░▒▓█` are quartiles of the busiest
  cell, one hue light to dark, with a legend row.
- **Split bar** — one bar cut into parts, each in its own fill `█ ▓ ▒ ░` so identity is
  texture, not colour, with a legend row per part giving share and count.
- Charts are headed by a one-line caption inside their fence, not a frame: their axes
  are frame enough. Trends are said in words beside them — "up 18% on the week
  before" — never as an arrow, which a screen reader announces as a triangle.
- **Mascot** — the `/help` wordmark and the `/play` speaker. Decoration, and the
  only two places decoration is allowed. The wordmark is the bot's own name as the
  server sees it, letter-spaced: coucou is the application, never the character.
- **Avatar** — the bot's avatar as a thumbnail on `/help`, `/about` and `/invite`,
  the replies that say who the bot is. Identity, not decoration, so nowhere else.

## Colour inside blocks (ANSI)

When a reply would pass Discord's length limits (4096 per description, 6000 per
message) the escapes are what give way: `fit` strips colour from the biggest
description first, because every chart already reads without it.

` ```ansi ` fences are used for bars, with SGR 32/33/31/30 mapped to the four
tokens above. **This is undocumented client behaviour, not Discord API** — it is
absent from the developer docs entirely. It renders on desktop and web; where it
does not, the escape sequences may show as literal text rather than degrading to
grey. Every block is therefore designed to read correctly with no colour at all,
and `ansiColour` in `internal/commands/ascii.go` turns all of it off in one line
if the wild disagrees.

## Voice

- Deadpan, short, never cute about its own cleverness. "Gone." "Wasn't here."
  "No rest for anyone." "Give it five minutes and somebody will regret it."
- Sentence case. No exclamation marks except the bot's own name.
- A missing number says so in words (`none yet`), never an em dash: a dash is
  announced as "dash" by a screen reader, or skipped entirely.
- Counts carry their noun and agree with it — "1 channel", never "1 channels".
